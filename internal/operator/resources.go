package operator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// How often a template's or ClusterResource's status is refreshed from
// Fleet, and a missing ref or Secret looked for again, without an event.
const resyncPeriod = 2 * time.Minute

func setupResources(mgr manager.Manager, dc dynamic.Interface) error {
	r := &Resources{Client: mgr.GetClient(), Secrets: mgr.GetAPIReader(), Dynamic: dc}
	fleetKinds := []string{"HelmOp", "Bundle", "GitRepo"}

	tb := ctrl.NewControllerManagedBy(mgr).Named("hyve-template-resources").For(object(templateGVK))
	for _, k := range fleetKinds {
		tb = tb.Owns(object(resources.FleetGVRs[k].GroupVersion().WithKind(k)))
	}
	// A shared Resource changing re-renders every template in its namespace
	// that refers to it.
	tb = tb.Watches(object(resourceGVK), handler.EnqueueRequestsFromMapFunc(r.templatesUsing))
	if err := tb.Complete(reconcile.Func(r.ReconcileTemplate)); err != nil {
		return err
	}

	cb := ctrl.NewControllerManagedBy(mgr).Named("hyve-cluster-resources").For(object(clusterResourceGVK))
	for _, k := range fleetKinds {
		cb = cb.Owns(object(resources.FleetGVRs[k].GroupVersion().WithKind(k)))
	}
	cb = cb.Watches(object(resourceGVK), handler.EnqueueRequestsFromMapFunc(r.clusterResourcesUsing))
	return cb.Complete(reconcile.Func(r.ReconcileClusterResource))
}

// Resources reconciles ClusterTemplates' resources and ClusterResources
// into Fleet objects.
type Resources struct {
	Client client.Client
	// Secrets reads Secrets uncached, so the controller needs only "get"
	// on them and holds no copy of any.
	Secrets client.Reader
	Dynamic dynamic.Interface
}

func (r *Resources) lookup(ctx context.Context, ns, name string) (*resources.Entry, error) {
	u := object(resourceGVK)
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		return nil, err
	}
	e, err := resources.EntryFromSpec(u)
	return &e, err
}

func (r *Resources) templatesUsing(ctx context.Context, o client.Object) []reconcile.Request {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(templateGVK.GroupVersion().WithKind("ClusterTemplateList"))
	if err := r.Client.List(ctx, list); err != nil {
		return nil
	}
	var out []reconcile.Request
	for i := range list.Items {
		t, err := templates.FromUnstructured(&list.Items[i])
		if err != nil {
			continue
		}
		for _, e := range t.Spec.Resources {
			ns := e.RefNamespace
			if ns == "" {
				ns = t.Namespace
			}
			if e.Ref == o.GetName() && ns == o.GetNamespace() {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: t.Namespace, Name: t.Name}})
				break
			}
		}
	}
	return out
}

func (r *Resources) clusterResourcesUsing(ctx context.Context, o client.Object) []reconcile.Request {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(clusterResourceGVK.GroupVersion().WithKind("ClusterResourceList"))
	if err := r.Client.List(ctx, list); err != nil {
		return nil
	}
	var out []reconcile.Request
	for i := range list.Items {
		if ref, _, _ := unstructured.NestedString(list.Items[i].Object, "spec", "ref"); ref == o.GetName() {
			out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: list.Items[i].GetNamespace(), Name: list.Items[i].GetName()}})
		}
	}
	return out
}

// ReconcileTemplate makes a template's Fleet objects match its resources.
func (r *Resources) ReconcileTemplate(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	u := object(templateGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, u); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if u.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil // the Fleet objects are owned; GC removes them
	}
	t, err := templates.FromUnstructured(u)
	if err != nil {
		return reconcile.Result{}, r.setStatus(ctx, u, nil, nil, err)
	}
	owner := resources.Owner{Kind: resources.OwnerTemplate, Name: t.Name, Namespace: t.Namespace, UID: u.GetUID()}
	want := map[string]bool{}
	var problems []string
	for _, e := range t.Spec.Resources {
		want[resources.ObjectName(owner, e.Name)] = true
		if err := r.apply(ctx, e, owner, t.Namespace); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if err := r.prune(ctx, owner, want); err != nil {
		return reconcile.Result{}, err
	}
	var perr error
	if len(problems) > 0 {
		perr = fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return reconcile.Result{RequeueAfter: resyncPeriod}, r.setStatus(ctx, u, &owner, r.missingSecrets(ctx, t.Spec.Resources, t.Namespace), perr)
}

// ReconcileClusterResource makes a ClusterResource's Fleet object, and
// ties the ClusterResource to its cluster so it goes when the cluster does.
func (r *Resources) ReconcileClusterResource(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	u := object(clusterResourceGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, u); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if u.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	clusterName, _, _ := unstructured.NestedString(u.Object, "spec", "clusterName")
	e, err := resources.EntryFromSpec(u)
	if err != nil || clusterName == "" {
		if err == nil {
			err = fmt.Errorf("spec.clusterName is required")
		}
		return reconcile.Result{}, r.setStatus(ctx, u, nil, nil, err)
	}
	// The entry's own name (`hyve cluster resource add --name`), which is
	// also the Helm release's, is on the label; the object is named
	// <cluster>-<name>.
	if n := u.GetLabels()[resources.LabelResource]; n != "" {
		e.Name = n
	}
	if err := r.adopt(ctx, u, clusterName); err != nil {
		return reconcile.Result{}, err
	}
	// A ref without a namespace is looked up beside the cluster's template,
	// where shared Resources live, and then in the ClusterResource's own.
	refNS := u.GetNamespace()
	cluster := object(clusterGVK)
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: u.GetNamespace(), Name: clusterName}, cluster); err == nil {
		if name, ns := templates.Ref(cluster); name != "" && e.RefNamespace == "" && e.Ref != "" {
			if _, err := r.lookup(ctx, ns, e.Ref); err == nil {
				refNS = ns
			}
		}
	}
	owner := resources.Owner{Kind: resources.OwnerClusterResource, Name: u.GetName(), Namespace: u.GetNamespace(), UID: u.GetUID(), ClusterName: clusterName}
	aerr := r.apply(ctx, e, owner, refNS)
	if err := r.prune(ctx, owner, map[string]bool{resources.ObjectName(owner, e.Name): true}); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: resyncPeriod}, r.setStatus(ctx, u, &owner, r.missingSecrets(ctx, []resources.Entry{e}, u.GetNamespace()), aerr)
}

// adopt labels a ClusterResource with its cluster and, once the cluster
// exists, makes the cluster its owner.
func (r *Resources) adopt(ctx context.Context, u *unstructured.Unstructured, clusterName string) error {
	patch := client.MergeFrom(u.DeepCopy())
	changed := false
	l := u.GetLabels()
	if l == nil {
		l = map[string]string{}
	}
	if l[resources.LabelCluster] != clusterName || l[resources.ClusterIndexedLabel] != "true" {
		l[resources.LabelCluster] = clusterName
		l[resources.ClusterIndexedLabel] = "true"
		u.SetLabels(l)
		changed = true
	}
	cluster := object(clusterGVK)
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: u.GetNamespace(), Name: clusterName}, cluster)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err == nil {
		owned := false
		for _, o := range u.GetOwnerReferences() {
			if o.UID == cluster.GetUID() {
				owned = true
			}
		}
		if !owned {
			u.SetOwnerReferences(append(u.GetOwnerReferences(), metav1.OwnerReference{
				APIVersion: clusterGVK.GroupVersion().String(), Kind: "Cluster", Name: clusterName, UID: cluster.GetUID(),
			}))
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return r.Client.Patch(ctx, u, patch)
}

// apply resolves an entry and server-side-applies its Fleet object.
func (r *Resources) apply(ctx context.Context, e resources.Entry, owner resources.Owner, refNS string) error {
	resolved, err := resources.Resolve(ctx, e, refNS, r.lookup)
	if err != nil {
		return err
	}
	obj, err := resources.Object(resolved, owner)
	if err != nil {
		return err
	}
	obj.SetManagedFields(nil)
	return r.Client.Patch(ctx, obj, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership)
}

// prune deletes an owner's Fleet objects that aren't wanted any more.
func (r *Resources) prune(ctx context.Context, owner resources.Owner, want map[string]bool) error {
	items, err := resources.List(ctx, r.Dynamic, resources.Selector{Namespace: owner.Namespace, OwnerKind: owner.Kind, Owner: owner.Name})
	if err != nil {
		return err
	}
	for _, it := range items {
		if want[it.Object.GetName()] {
			continue
		}
		log.FromContext(ctx).Info("removing resource", "kind", it.Kind, "name", it.Object.GetName())
		gvr := resources.FleetGVRs[it.Kind]
		if err := r.Dynamic.Resource(gvr).Namespace(owner.Namespace).Delete(ctx, it.Object.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// missingSecrets returns the Secrets the entries name that don't exist in
// ns.
func (r *Resources) missingSecrets(ctx context.Context, entries []resources.Entry, ns string) []string {
	var missing []string
	seen := map[string]bool{}
	for _, e := range entries {
		names := e.Secrets
		if e.Ref != "" {
			if shared, err := resources.Resolve(ctx, e, ns, r.lookup); err == nil {
				names = shared.Secrets
			}
		}
		for _, s := range names {
			if seen[s] {
				continue
			}
			seen[s] = true
			if err := r.Secrets.Get(ctx, types.NamespacedName{Namespace: ns, Name: s}, &metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}}); apierrors.IsNotFound(err) {
				missing = append(missing, s)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// setStatus writes the owner's status: each Fleet object's state, Secrets
// that are missing, and a Ready condition.
func (r *Resources) setStatus(ctx context.Context, u *unstructured.Unstructured, owner *resources.Owner, missing []string, problem error) error {
	orig := u.DeepCopy()
	var rows []any
	if owner != nil {
		items, err := resources.List(ctx, r.Dynamic, resources.Selector{Namespace: owner.Namespace, OwnerKind: owner.Kind, Owner: owner.Name})
		if err != nil {
			return err
		}
		for _, it := range items {
			ready, state := it.Ready()
			rows = append(rows, map[string]any{"name": it.Name, "kind": it.Kind, "object": it.Object.GetName(), "ready": ready, "state": state})
		}
	}
	cond := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Synced", Message: "", ObservedGeneration: u.GetGeneration()}
	switch {
	case problem != nil:
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "Error", problem.Error()
	case len(missing) > 0:
		cond.Status, cond.Reason = metav1.ConditionFalse, "SecretsMissing"
		cond.Message = "Secrets to copy to clusters don't exist in " + u.GetNamespace() + ": " + strings.Join(missing, ", ")
	}
	var conds []metav1.Condition
	if raw, ok, _ := unstructured.NestedSlice(u.Object, "status", "conditions"); ok {
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
	status := map[string]any{"observedGeneration": u.GetGeneration(), "conditions": condsRaw}
	if rows != nil {
		status["resources"] = rows
	}
	if len(missing) > 0 {
		ms := make([]any, len(missing))
		for i, m := range missing {
			ms[i] = m
		}
		status["missingSecrets"] = ms
	}
	u.Object["status"] = status
	if err := r.Client.Status().Patch(ctx, u, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
