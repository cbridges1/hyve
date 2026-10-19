package operator

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/resources"
)

// With Rancher installed (HYVE-PROPOSAL.md, "With Rancher: Rancher's own
// model, plus Turtles' two gaps filled"), hyve has no permission model of
// its own; it fills the gaps Turtles leaves and fits environments into
// Rancher's Projects:
//
//   - the creator of a CAPI Cluster becomes its Cluster Owner once Turtles
//     has imported it (Turtles sets no-creator-rbac);
//   - each ClusterRoleTemplateBinding on an imported cluster reaches its
//     CAPI Cluster and ClusterResources through a Role limited to them by
//     resourceNames, as Rancher does for clusters it provisions;
//   - each environment namespace gets a FleetWorkspace and a Project, and
//     template namespaces a Project of their own;
//   - the "hyve Environment Member" RoleTemplate grants what environment
//     users need, without Secrets.

var (
	rancherClusterGVK = schema.GroupVersionKind{Group: "management.cattle.io", Version: "v3", Kind: "Cluster"}
	crtbGVK           = schema.GroupVersionKind{Group: "management.cattle.io", Version: "v3", Kind: "ClusterRoleTemplateBinding"}
	projectGVK        = schema.GroupVersionKind{Group: "management.cattle.io", Version: "v3", Kind: "Project"}
	fleetWorkspaceGVK = schema.GroupVersionKind{Group: "management.cattle.io", Version: "v3", Kind: "FleetWorkspace"}
	roleTemplateGVK   = schema.GroupVersionKind{Group: "management.cattle.io", Version: "v3", Kind: "RoleTemplate"}

	// Rancher user IDs, as Rancher's proxy impersonates them.
	rancherUserRE = regexp.MustCompile(`^(u|user)-[a-z0-9]+$`)
)

const (
	turtlesOwnerLabel   = "cluster-api.cattle.io/capi-cluster-owner"
	turtlesOwnerNSLabel = "cluster-api.cattle.io/capi-cluster-owner-ns"
	labelCRTB           = "hyve.io/crtb"
	annotProject        = "hyve.io/project"
	// EnvironmentMemberRoleTemplate is the RoleTemplate hyve ships.
	EnvironmentMemberRoleTemplate = "hyve-environment-member"
)

// IsRancherUser reports whether a username is a Rancher user ID.
func IsRancherUser(name string) bool { return rancherUserRE.MatchString(name) }

// Rancher is hyve-controller's Rancher mode.
type Rancher struct {
	Client client.Client
	// Reader reads uncached (the Turtles check), so the controller doesn't
	// watch every Deployment.
	Reader client.Reader
}

// RancherInstalled reports whether Rancher's management API is served.
func RancherInstalled(ctx context.Context, c client.Client) bool {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(rancherClusterGVK.GroupVersion().WithKind("ClusterList"))
	return c.List(ctx, list, client.Limit(1)) == nil
}

func setupRancher(ctx context.Context, mgr manager.Manager) error {
	r := &Rancher{Client: mgr.GetClient(), Reader: mgr.GetAPIReader()}
	lg := log.FromContext(ctx)
	if err := r.ensureRoleTemplate(ctx); err != nil {
		lg.Error(err, "creating the hyve Environment Member RoleTemplate")
	}
	if ok, err := r.useCAAPF(ctx); err != nil {
		lg.Info("couldn't check Turtles' use-caapf feature", "error", err.Error())
	} else if !ok {
		lg.Info("Turtles' use-caapf feature is off: template resources reach clusters only through CAAPF, so turn it on (it reinstalls fleet-agent on clusters already imported)")
	}
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-rancher-creator").For(object(clusterGVK)).
		Watches(object(rancherClusterGVK), handler.EnqueueRequestsFromMapFunc(capiClusterFor)).
		Complete(reconcile.Func(r.ReconcileCreator)); err != nil {
		return err
	}
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-rancher-crtb").For(object(crtbGVK)).
		Watches(object(clusterResourceGVK), handler.EnqueueRequestsFromMapFunc(r.crtbsForClusterResource)).
		Complete(reconcile.Func(r.ReconcileCRTB)); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).Named("hyve-rancher-namespaces").For(&corev1.Namespace{}).
		Watches(object(templateGVK), handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: o.GetNamespace()}}}
		})).
		Complete(reconcile.Func(r.ReconcileNamespace))
}

// capiClusterFor maps a Rancher cluster Turtles imported to its CAPI
// Cluster.
func capiClusterFor(_ context.Context, o client.Object) []reconcile.Request {
	l := o.GetLabels()
	if l[turtlesOwnerLabel] == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: l[turtlesOwnerNSLabel], Name: l[turtlesOwnerLabel]}}}
}

// rancherClusterFor finds the Rancher cluster Turtles imported a CAPI
// Cluster as, or nil.
func (r *Rancher) rancherClusterFor(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(rancherClusterGVK.GroupVersion().WithKind("ClusterList"))
	if err := r.Client.List(ctx, list, client.MatchingLabels{turtlesOwnerLabel: name, turtlesOwnerNSLabel: ns}); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

// ReconcileCreator makes a CAPI Cluster's creator its Cluster Owner, once
// Turtles has imported it.
func (r *Rancher) ReconcileCreator(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c := object(clusterGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, c); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	creator := c.GetAnnotations()[AnnotationCreator]
	if !IsRancherUser(creator) || c.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	rc, err := r.rancherClusterFor(ctx, c.GetNamespace(), c.GetName())
	if err != nil {
		return reconcile.Result{}, err
	}
	if rc == nil {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil // not imported yet
	}
	crtb := object(crtbGVK)
	crtb.SetNamespace(rc.GetName())
	crtb.SetName("hyve-creator-" + shortHash(creator))
	crtb.Object["clusterName"] = rc.GetName()
	crtb.Object["roleTemplateName"] = "cluster-owner"
	crtb.Object["userName"] = creator
	crtb.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "hyve"})
	if err := r.Client.Create(ctx, crtb); err != nil && !apierrors.IsAlreadyExists(err) {
		return reconcile.Result{}, fmt.Errorf("making %s owner of %s: %w", creator, rc.GetName(), err)
	}
	return reconcile.Result{}, nil
}

func (r *Rancher) crtbsForClusterResource(ctx context.Context, o client.Object) []reconcile.Request {
	cn, _, _ := unstructured.NestedString(o.(*unstructured.Unstructured).Object, "spec", "clusterName")
	if cn == "" {
		return nil
	}
	rc, err := r.rancherClusterFor(ctx, o.GetNamespace(), cn)
	if err != nil || rc == nil {
		return nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(crtbGVK.GroupVersion().WithKind("ClusterRoleTemplateBindingList"))
	if err := r.Client.List(ctx, list, client.InNamespace(rc.GetName())); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, b := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: b.GetNamespace(), Name: b.GetName()}})
	}
	return out
}

// ReconcileCRTB gives a CRTB's subject its CAPI Cluster and
// ClusterResources on the management cluster: a Role limited to them by
// resourceNames, full for owners, read for everyone else.
func (r *Rancher) ReconcileCRTB(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	key := req.Namespace + "." + req.Name
	crtb := object(crtbGVK)
	err := r.Client.Get(ctx, req.NamespacedName, crtb)
	if apierrors.IsNotFound(err) || (err == nil && crtb.GetDeletionTimestamp() != nil) {
		return reconcile.Result{}, r.deleteCRTBRoles(ctx, key)
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	rc := object(rancherClusterGVK)
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Namespace}, rc); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	capiNS, capiName := rc.GetLabels()[turtlesOwnerNSLabel], rc.GetLabels()[turtlesOwnerLabel]
	if capiName == "" {
		return reconcile.Result{}, nil // not a cluster Turtles imported
	}
	var subjects []rbacv1.Subject
	if u, _ := crtb.Object["userName"].(string); u != "" {
		subjects = append(subjects, rbacv1.Subject{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: u})
	}
	if g, _ := crtb.Object["groupPrincipalName"].(string); g != "" {
		subjects = append(subjects, rbacv1.Subject{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: g})
	}
	if g, _ := crtb.Object["groupName"].(string); g != "" {
		subjects = append(subjects, rbacv1.Subject{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: g})
	}
	if len(subjects) == 0 {
		return reconcile.Result{}, nil
	}
	verbs := []string{"get", "watch"}
	if rt, _ := crtb.Object["roleTemplateName"].(string); rt == "cluster-owner" {
		verbs = []string{"get", "watch", "update", "patch", "delete"}
	}
	crList := &unstructured.UnstructuredList{}
	crList.SetGroupVersionKind(clusterResourceGVK.GroupVersion().WithKind("ClusterResourceList"))
	_ = r.Client.List(ctx, crList, client.InNamespace(capiNS), client.MatchingLabels{resources.LabelCluster: capiName})
	var crNames []string
	for _, cr := range crList.Items {
		crNames = append(crNames, cr.GetName())
	}
	sort.Strings(crNames)
	rules := []rbacv1.PolicyRule{{APIGroups: []string{"cluster.x-k8s.io"}, Resources: []string{"clusters"}, ResourceNames: []string{capiName}, Verbs: verbs}}
	if len(crNames) > 0 {
		rules = append(rules, rbacv1.PolicyRule{APIGroups: []string{"hyve.io"}, Resources: []string{"clusterresources"}, ResourceNames: crNames, Verbs: verbs})
	}
	name := "hyve-crtb-" + shortHash(key)
	labels := map[string]string{"app.kubernetes.io/managed-by": "hyve", labelCRTB: shortHash(key)}
	role := &rbacv1.Role{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: capiNS, Labels: labels, Annotations: map[string]string{labelCRTB: key}},
		Rules:      rules,
	}
	if err := applyTyped(ctx, r.Client, role); err != nil {
		return reconcile.Result{}, err
	}
	rb := &rbacv1.RoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: capiNS, Labels: labels, Annotations: map[string]string{labelCRTB: key}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name},
		Subjects:   subjects,
	}
	return reconcile.Result{}, applyTyped(ctx, r.Client, rb)
}

func (r *Rancher) deleteCRTBRoles(ctx context.Context, key string) error {
	sel := client.MatchingLabels{labelCRTB: shortHash(key)}
	rbs := &rbacv1.RoleBindingList{}
	if err := r.Client.List(ctx, rbs, sel); err != nil {
		return err
	}
	for i := range rbs.Items {
		if err := r.Client.Delete(ctx, &rbs.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	roles := &rbacv1.RoleList{}
	if err := r.Client.List(ctx, roles, sel); err != nil {
		return err
	}
	for i := range roles.Items {
		if err := r.Client.Delete(ctx, &roles.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// ReconcileNamespace puts environment namespaces in a FleetWorkspace and a
// Project of their own, and template namespaces in a Project.
func (r *Rancher) ReconcileNamespace(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, req.NamespacedName, ns); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if ns.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}
	_, isEnv := ns.Labels["hyve.io/environment"]
	tl := &unstructured.UnstructuredList{}
	tl.SetGroupVersionKind(templateGVK.GroupVersion().WithKind("ClusterTemplateList"))
	_ = r.Client.List(ctx, tl, client.InNamespace(ns.Name), client.Limit(1))
	isTemplates := len(tl.Items) > 0
	if !isEnv && !isTemplates {
		return reconcile.Result{}, nil
	}
	if isEnv {
		fw := object(fleetWorkspaceGVK)
		fw.SetName(ns.Name)
		fw.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "hyve"})
		if err := r.Client.Create(ctx, fw); err != nil && !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, fmt.Errorf("FleetWorkspace %s: %w", ns.Name, err)
		}
	}
	if ns.Annotations[annotProject] != "" || ns.Annotations["field.cattle.io/projectId"] != "" {
		return reconcile.Result{}, nil // already in a Project (ours, or one an admin chose)
	}
	display := "hyve environment " + ns.Name
	if isTemplates && !isEnv {
		display = "hyve templates " + ns.Name
	}
	p := object(projectGVK)
	p.SetNamespace("local")
	p.SetGenerateName("p-")
	p.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "hyve"})
	p.Object["spec"] = map[string]any{"clusterName": "local", "displayName": display, "description": "Created by hyve for namespace " + ns.Name}
	if err := r.Client.Create(ctx, p); err != nil {
		return reconcile.Result{}, fmt.Errorf("Project for %s: %w", ns.Name, err)
	}
	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Annotations == nil {
		ns.Annotations = map[string]string{}
	}
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	ns.Annotations[annotProject] = p.GetName()
	ns.Annotations["field.cattle.io/projectId"] = "local:" + p.GetName()
	ns.Labels["field.cattle.io/projectId"] = p.GetName()
	return reconcile.Result{}, r.Client.Patch(ctx, ns, patch)
}

// ensureRoleTemplate creates or updates the hyve Environment Member
// RoleTemplate (project context): create and list CAPI Clusters and
// ClusterResources, read templates; no Secrets, unlike Rancher's Project
// Member.
func (r *Rancher) ensureRoleTemplate(ctx context.Context) error {
	rt := object(roleTemplateGVK)
	rt.SetName(EnvironmentMemberRoleTemplate)
	rt.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "hyve"})
	rt.Object["displayName"] = "hyve Environment Member"
	rt.Object["description"] = "Create and manage clusters and their extras in a hyve environment, and read templates. No Secrets."
	rt.Object["context"] = "project"
	rules := []any{
		map[string]any{"apiGroups": []any{"cluster.x-k8s.io"}, "resources": []any{"clusters"}, "verbs": []any{"get", "list", "watch", "create"}},
		map[string]any{"apiGroups": []any{"cluster.x-k8s.io"}, "resources": []any{"clusterclasses"}, "verbs": []any{"get", "list", "watch"}},
		map[string]any{"apiGroups": []any{"hyve.io"}, "resources": []any{"clusterresources"}, "verbs": []any{"get", "list", "watch", "create", "update", "patch", "delete"}},
		map[string]any{"apiGroups": []any{"hyve.io"}, "resources": []any{"clustertemplates", "resources"}, "verbs": []any{"get", "list", "watch"}},
		map[string]any{"apiGroups": []any{"fleet.cattle.io"}, "resources": []any{"clusters", "helmops", "bundles", "bundledeployments", "gitrepos"}, "verbs": []any{"get", "list", "watch"}},
	}
	rt.Object["rules"] = rules
	return r.Client.Patch(ctx, rt, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership)
}

// useCAAPF reports whether Turtles runs with its use-caapf feature on.
func (r *Rancher) useCAAPF(ctx context.Context) (bool, error) {
	list := &appsv1.DeploymentList{}
	if err := r.Reader.List(ctx, list); err != nil {
		return false, err
	}
	for _, d := range list.Items {
		if !strings.HasPrefix(d.Name, "rancher-turtles-controller") {
			continue
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			for _, a := range c.Args {
				if strings.HasPrefix(a, "--feature-gates=") {
					return strings.Contains(a, "use-caapf=true"), nil
				}
			}
		}
	}
	return false, fmt.Errorf("no rancher-turtles Deployment found")
}
