package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClusterAccess is hyve's equivalent of Rancher's
// ClusterRoleTemplateBinding (HYVE-PROPOSAL.md, "Access to child
// clusters"): a role for a
// hyve user or group on one cluster, or every cluster in an environment.
//
// For each cluster it covers, hyve-controller writes the role's rules as a
// ClusterRole on the child and binds hyve:<user> (or hyve:<group>) to it,
// and on the management cluster a Role limited by resourceNames to the
// cluster's own CAPI Cluster. The role's rules come from the ClusterRole
// hyve-cluster-<role> on the management cluster, which is labelled
// hyve.io/role-template and never bound there.

var accessGVK = schema.GroupVersionKind{Group: "hyve.io", Version: "v1alpha1", Kind: "ClusterAccess"}

const (
	accessFinalizer = "hyve.io/access"
	// LabelRoleTemplate marks the ClusterRoles whose rules ClusterAccess
	// copies to children.
	LabelRoleTemplate = "hyve.io/role-template"
	// labelAccess on downstream and management objects names the
	// ClusterAccess (<namespace>.<name>) they were written for.
	labelAccess = "hyve.io/access"
	// AnnotationCreator on a Cluster: the hyve user who created it, who
	// becomes its owner.
	AnnotationCreator = "hyve.io/creator"
)

// Roles a ClusterAccess may name, and the template ClusterRole for each.
var accessRoles = map[string]string{
	"owner": "hyve-cluster-owner", "member": "hyve-cluster-member", "read-only": "hyve-cluster-readonly",
}

// AccessSpec is a ClusterAccess's spec.
type AccessSpec struct {
	Role        string `json:"role"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
	ClusterName string `json:"clusterName,omitempty"`
}

func accessSpec(u *unstructured.Unstructured) (AccessSpec, error) {
	var s AccessSpec
	raw, _, _ := unstructured.NestedMap(u.Object, "spec")
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &s); err != nil {
		return s, err
	}
	if _, ok := accessRoles[s.Role]; !ok {
		return s, fmt.Errorf("role %q: want owner, member or read-only", s.Role)
	}
	if (s.User == "") == (s.Group == "") {
		return s, fmt.Errorf("give exactly one of user or group")
	}
	return s, nil
}

func (s AccessSpec) subject() rbacv1.Subject {
	if s.User != "" {
		return rbacv1.Subject{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: "hyve:" + s.User}
	}
	return rbacv1.Subject{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: "hyve:" + s.Group}
}

func accessKey(u client.Object) string { return u.GetNamespace() + "." + u.GetName() }

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:10]
}

// Access reconciles ClusterAccess objects, and makes each new cluster's
// creator its owner.
type Access struct {
	Client client.Client
	// NewChildClient builds a client for a child from its CAPI kubeconfig.
	NewChildClient func(kubeconfig []byte) (client.Client, error)
}

func defaultChildCtrlClient(kubeconfig []byte) (client.Client, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 30 * time.Second
	return client.New(cfg, client.Options{})
}

func setupAccess(mgr manager.Manager) error {
	a := &Access{Client: mgr.GetClient()}
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-access").For(object(accessGVK)).
		// A cluster appearing, or becoming available, needs every
		// environment-wide ClusterAccess written to it.
		Watches(object(clusterGVK), handler.EnqueueRequestsFromMapFunc(a.accessFor)).
		Complete(reconcile.Func(a.Reconcile)); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).Named("hyve-creator").For(object(clusterGVK)).
		Complete(reconcile.Func(a.ReconcileCreator))
}

func (a *Access) accessFor(ctx context.Context, o client.Object) []reconcile.Request {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(accessGVK.GroupVersion().WithKind("ClusterAccessList"))
	if err := a.Client.List(ctx, list, client.InNamespace(o.GetNamespace())); err != nil {
		return nil
	}
	var out []reconcile.Request
	for _, ca := range list.Items {
		if cn, _, _ := unstructured.NestedString(ca.Object, "spec", "clusterName"); cn == "" || cn == o.GetName() {
			out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ca.GetNamespace(), Name: ca.GetName()}})
		}
	}
	return out
}

// ReconcileCreator creates an owner ClusterAccess for the user recorded as
// a cluster's creator.
func (a *Access) ReconcileCreator(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c := object(clusterGVK)
	if err := a.Client.Get(ctx, req.NamespacedName, c); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	creator := strings.TrimPrefix(c.GetAnnotations()[AnnotationCreator], "hyve:")
	if creator == "" || c.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	ca := object(accessGVK)
	ca.SetNamespace(c.GetNamespace())
	ca.SetName(c.GetName() + "-creator")
	ca.Object["spec"] = map[string]any{"role": "owner", "user": creator, "clusterName": c.GetName()}
	ca.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: clusterGVK.GroupVersion().String(), Kind: "Cluster", Name: c.GetName(), UID: c.GetUID()}})
	err := a.Client.Create(ctx, ca)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// Reconcile writes a ClusterAccess's bindings to every cluster it covers.
func (a *Access) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	lg := log.FromContext(ctx)
	ca := object(accessGVK)
	if err := a.Client.Get(ctx, req.NamespacedName, ca); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	key := accessKey(ca)
	clusters, err := a.covered(ctx, ca)
	if err != nil {
		return reconcile.Result{}, err
	}
	if ca.GetDeletionTimestamp() != nil {
		for _, c := range clusters {
			if err := a.unbind(ctx, c, key); err != nil {
				lg.Info("removing access from a cluster failed; retrying", "cluster", c.GetName(), "error", err.Error())
				return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
			}
		}
		if controllerutil.RemoveFinalizer(ca, accessFinalizer) {
			return reconcile.Result{}, a.Client.Update(ctx, ca)
		}
		return reconcile.Result{}, nil
	}
	if controllerutil.AddFinalizer(ca, accessFinalizer) {
		if err := a.Client.Update(ctx, ca); err != nil {
			return reconcile.Result{}, err
		}
	}
	spec, err := accessSpec(ca)
	if err != nil {
		return reconcile.Result{}, a.setAccessStatus(ctx, ca, nil, err)
	}
	tmpl := &rbacv1.ClusterRole{}
	if err := a.Client.Get(ctx, types.NamespacedName{Name: accessRoles[spec.Role]}, tmpl); err != nil {
		return reconcile.Result{}, a.setAccessStatus(ctx, ca, nil, fmt.Errorf("role template %s: %w", accessRoles[spec.Role], err))
	}
	var done, pending []string
	for _, c := range clusters {
		if err := a.management(ctx, c, ca, spec); err != nil {
			return reconcile.Result{}, err
		}
		if err := a.bind(ctx, c, key, spec, tmpl); err != nil {
			lg.Info("writing access to a cluster failed; retrying", "cluster", c.GetName(), "error", err.Error())
			pending = append(pending, c.GetName())
			continue
		}
		done = append(done, c.GetName())
	}
	var perr error
	if len(pending) > 0 {
		perr = fmt.Errorf("not yet written to %s (waiting for the cluster to be available)", strings.Join(pending, ", "))
	}
	return reconcile.Result{RequeueAfter: 10 * time.Minute}, a.setAccessStatus(ctx, ca, done, perr)
}

// covered returns the clusters a ClusterAccess applies to.
func (a *Access) covered(ctx context.Context, ca *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	cn, _, _ := unstructured.NestedString(ca.Object, "spec", "clusterName")
	if cn != "" {
		c := object(clusterGVK)
		err := a.Client.Get(ctx, types.NamespacedName{Namespace: ca.GetNamespace(), Name: cn}, c)
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return []*unstructured.Unstructured{c}, err
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(clusterGVK.GroupVersion().WithKind("ClusterList"))
	if err := a.Client.List(ctx, list, client.InNamespace(ca.GetNamespace())); err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		out[i] = &list.Items[i]
	}
	return out, nil
}

func (a *Access) child(ctx context.Context, c *unstructured.Unstructured) (client.Client, error) {
	sec := &corev1.Secret{}
	if err := a.Client.Get(ctx, types.NamespacedName{Namespace: c.GetNamespace(), Name: c.GetName() + "-kubeconfig"}, sec); err != nil {
		return nil, err
	}
	newClient := a.NewChildClient
	if newClient == nil {
		newClient = defaultChildCtrlClient
	}
	return newClient(sec.Data["value"])
}

// bind writes the role and its binding on the child.
func (a *Access) bind(ctx context.Context, c *unstructured.Unstructured, key string, spec AccessSpec, tmpl *rbacv1.ClusterRole) error {
	if !ConditionTrue(c, "ControlPlaneAvailable") && !ConditionTrue(c, "Available") {
		return fmt.Errorf("cluster isn't available yet")
	}
	ch, err := a.child(ctx, c)
	if err != nil {
		return err
	}
	role := &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: metav1.ObjectMeta{Name: tmpl.Name, Labels: map[string]string{"app.kubernetes.io/managed-by": "hyve"}},
		Rules:      tmpl.Rules,
	}
	if err := applyTyped(ctx, ch, role); err != nil {
		return err
	}
	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: "hyve-access-" + shortHash(key),
			Labels:      map[string]string{"app.kubernetes.io/managed-by": "hyve", labelAccess: shortHash(key)},
			Annotations: map[string]string{labelAccess: key}},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: tmpl.Name},
		Subjects: []rbacv1.Subject{spec.subject()},
	}
	return applyTyped(ctx, ch, crb)
}

func (a *Access) unbind(ctx context.Context, c *unstructured.Unstructured, key string) error {
	ch, err := a.child(ctx, c)
	if apierrors.IsNotFound(err) {
		return nil // the cluster's kubeconfig is gone with it
	}
	if err != nil {
		return err
	}
	err = ch.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "hyve-access-" + shortHash(key)}})
	return client.IgnoreNotFound(err)
}

// management writes the Role on the management cluster that gives the
// subject the cluster's own CAPI Cluster: owners may change and delete it,
// members and read-only users may read it. Owned by the ClusterAccess, so
// it goes with it.
func (a *Access) management(ctx context.Context, c, ca *unstructured.Unstructured, spec AccessSpec) error {
	verbs := []string{"get", "watch"}
	if spec.Role == "owner" {
		verbs = []string{"get", "watch", "update", "patch", "delete"}
	}
	name := "hyve-access-" + shortHash(accessKey(ca)+"/"+c.GetName())
	owner := metav1.OwnerReference{APIVersion: accessGVK.GroupVersion().String(), Kind: "ClusterAccess", Name: ca.GetName(), UID: ca.GetUID()}
	role := &rbacv1.Role{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.GetNamespace(), Labels: map[string]string{labelAccess: shortHash(accessKey(ca))}, OwnerReferences: []metav1.OwnerReference{owner}},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"cluster.x-k8s.io"}, Resources: []string{"clusters"}, ResourceNames: []string{c.GetName()}, Verbs: verbs},
		},
	}
	if err := applyTyped(ctx, a.Client, role); err != nil {
		return err
	}
	rb := &rbacv1.RoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.GetNamespace(), Labels: role.Labels, OwnerReferences: []metav1.OwnerReference{owner}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name},
		Subjects:   []rbacv1.Subject{spec.subject()},
	}
	return applyTyped(ctx, a.Client, rb)
}

func applyTyped(ctx context.Context, c client.Client, obj client.Object) error {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return err
	}
	u := &unstructured.Unstructured{Object: raw}
	unstructured.RemoveNestedField(u.Object, "metadata", "creationTimestamp")
	return c.Patch(ctx, u, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership)
}

// ConditionTrue reports whether a condition is True.
func ConditionTrue(u *unstructured.Unstructured, typ string) bool {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, x := range conds {
		if m, ok := x.(map[string]any); ok && m["type"] == typ {
			return m["status"] == "True"
		}
	}
	return false
}

func (a *Access) setAccessStatus(ctx context.Context, ca *unstructured.Unstructured, done []string, problem error) error {
	orig := ca.DeepCopy()
	sort.Strings(done)
	ds := make([]any, len(done))
	for i, d := range done {
		ds[i] = d
	}
	cond := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Synced", ObservedGeneration: ca.GetGeneration()}
	if problem != nil {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "Pending", problem.Error()
	}
	// SetStatusCondition keeps lastTransitionTime unless the status
	// changes, so an unchanged ClusterAccess writes an identical status
	// and doesn't wake itself up again.
	var conds []metav1.Condition
	if raw, ok, _ := unstructured.NestedSlice(ca.Object, "status", "conditions"); ok {
		for _, c := range raw {
			if m, ok := c.(map[string]any); ok {
				if mc, err := conditionFromMap(m); err == nil {
					conds = append(conds, mc)
				}
			}
		}
	}
	meta.SetStatusCondition(&conds, cond)
	condsRaw := make([]any, 0, len(conds))
	for _, c := range conds {
		m, err := conditionToMap(c)
		if err != nil {
			return err
		}
		condsRaw = append(condsRaw, m)
	}
	ca.Object["status"] = map[string]any{"clusters": ds, "conditions": condsRaw, "observedGeneration": ca.GetGeneration()}
	if err := a.Client.Status().Patch(ctx, ca, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
