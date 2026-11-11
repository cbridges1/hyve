package operator

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/templates"
)

var clusterGroupGVK = delivery.ClusterGroupGVR.GroupVersion().WithKind("ClusterGroup")

// annotGroupSource on a copied group names the one it's a copy of.
const annotGroupSource = "hyve.io/copy-of"

// groupsKey is the one key the groups reconciler works under: it makes
// every group at once, as templates and environments come and go.
var groupsKey = reconcile.Request{NamespacedName: types.NamespacedName{Name: "groups"}}

func setupGroups(mgr manager.Manager, workspace string) error {
	g := &Groups{Client: mgr.GetClient(), Workspace: workspace}
	all := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{groupsKey}
	})
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-groups").
		Watches(object(templateGVK), all).
		Watches(&corev1.Namespace{}, all).
		Watches(object(clusterGroupGVK), all).
		Complete(g); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).Named("hyve-cluster-labels").For(object(clusterGVK)).
		Complete(&ClusterLabels{Client: mgr.GetClient()})
}

// Groups keeps a Fleet ClusterGroup for each ClusterTemplate
// (template-NAME) and each environment (env-NAME), in the template
// namespaces and every environment, and copies the template namespace's
// other groups to the environments: Deployments target them
// (internal/delivery).
type Groups struct {
	Client client.Client
	// Workspace is the default template namespace, which gets environment
	// groups even before it holds a template.
	Workspace string
}

// Reconcile implements reconcile.Reconciler.
func (g *Groups) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	tl := &unstructured.UnstructuredList{}
	tl.SetGroupVersionKind(templateGVK.GroupVersion().WithKind("ClusterTemplateList"))
	if err := g.Client.List(ctx, tl); err != nil {
		return reconcile.Result{}, err
	}
	var want []*unstructured.Unstructured
	nsl := &corev1.NamespaceList{}
	if err := g.Client.List(ctx, nsl, client.HasLabels{install.LabelEnvironment}); err != nil {
		return reconcile.Result{}, err
	}
	// Fleet matches a bundle's clusterGroup target against the groups in
	// each cluster's own namespace (target.ClusterGroupsForCluster), so a
	// group must exist wherever clusters are: in every environment, as
	// well as in the template namespaces Deployments live in.
	var envs []string
	for _, ns := range nsl.Items {
		if ns.DeletionTimestamp == nil {
			envs = append(envs, ns.Name)
		}
	}
	homes := map[string]bool{g.Workspace: true}
	for _, e := range envs {
		homes[e] = true
	}
	var specs []*unstructured.Unstructured
	for i := range tl.Items {
		t := &tl.Items[i]
		if t.GetDeletionTimestamp() != nil {
			continue
		}
		homes[t.GetNamespace()] = true
		specs = append(specs, group("", delivery.TemplateGroup(t.GetName()), delivery.GroupKindTemplate,
			map[string]any{templates.LabelTemplate: t.GetName(), templates.LabelTemplateNamespace: t.GetNamespace()}))
	}
	for _, e := range envs {
		specs = append(specs, group("", delivery.EnvGroup(e), delivery.GroupKindEnvironment,
			map[string]any{delivery.LabelEnvironment: e}))
	}
	// Groups of the template namespace hyve doesn't keep itself (CAAPF's
	// for each ClusterClass, and people's own) are copied to the
	// environments, so Deployments there can target them too.
	src := &unstructured.UnstructuredList{}
	src.SetGroupVersionKind(clusterGroupGVK.GroupVersion().WithKind("ClusterGroupList"))
	if err := g.Client.List(ctx, src, client.InNamespace(g.Workspace)); err != nil {
		return reconcile.Result{}, err
	}
	for i := range src.Items {
		o := &src.Items[i]
		if o.GetLabels()[delivery.LabelGroupKind] != "" {
			continue
		}
		sel, ok, _ := unstructured.NestedMap(o.Object, "spec", "selector")
		if !ok {
			continue
		}
		cp := group("", o.GetName(), delivery.GroupKindCopy, nil)
		cp.Object["spec"] = map[string]any{"selector": sel}
		cp.SetAnnotations(map[string]string{annotGroupSource: g.Workspace + "/" + o.GetName()})
		for _, e := range envs {
			c := cp.DeepCopy()
			c.SetNamespace(e)
			want = append(want, c)
		}
	}
	for ns := range homes {
		for _, sp := range specs {
			c := sp.DeepCopy()
			c.SetNamespace(ns)
			want = append(want, c)
		}
	}
	// A group of the same name that isn't hyve's (CAAPF's own, say) is
	// left alone.
	theirs := map[string]bool{}
	all := &unstructured.UnstructuredList{}
	all.SetGroupVersionKind(clusterGroupGVK.GroupVersion().WithKind("ClusterGroupList"))
	if err := g.Client.List(ctx, all); err != nil {
		return reconcile.Result{}, err
	}
	for i := range all.Items {
		if all.Items[i].GetLabels()[delivery.LabelGroupKind] == "" {
			theirs[all.Items[i].GetNamespace()+"/"+all.Items[i].GetName()] = true
		}
	}
	kept := want[:0]
	for _, w := range want {
		if !theirs[w.GetNamespace()+"/"+w.GetName()] {
			kept = append(kept, w)
		}
	}
	want = kept
	// What's there now, to tell whether anything changed.
	before := map[string]string{}
	for i := range all.Items {
		sel, _, _ := unstructured.NestedFieldNoCopy(all.Items[i].Object, "spec", "selector")
		before[all.Items[i].GetNamespace()+"/"+all.Items[i].GetName()] = fmt.Sprint(sel)
	}
	changed := false
	keep := map[string]bool{}
	for _, w := range want {
		sel, _, _ := unstructured.NestedFieldNoCopy(w.Object, "spec", "selector")
		if old, ok := before[w.GetNamespace()+"/"+w.GetName()]; !ok || old != fmt.Sprint(sel) {
			changed = true
		}
		keep[w.GetNamespace()+"/"+w.GetName()] = true
		w.SetManagedFields(nil)
		if err := g.Client.Patch(ctx, w, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership); err != nil {
			if apierrors.IsNotFound(err) {
				continue // the namespace is going away
			}
			return reconcile.Result{}, err
		}
	}
	have := &unstructured.UnstructuredList{}
	have.SetGroupVersionKind(clusterGroupGVK.GroupVersion().WithKind("ClusterGroupList"))
	if err := g.Client.List(ctx, have, client.HasLabels{delivery.LabelGroupKind}, client.MatchingLabels{clusters.LabelManagedBy: clusters.ManagedBy}); err != nil {
		return reconcile.Result{}, err
	}
	for i := range have.Items {
		h := &have.Items[i]
		if keep[h.GetNamespace()+"/"+h.GetName()] {
			continue
		}
		log.FromContext(ctx).Info("removing ClusterGroup", "namespace", h.GetNamespace(), "name", h.GetName())
		if err := g.Client.Delete(ctx, h); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
		changed = true
	}
	// Fleet works a bundle's targets out when the bundle or a cluster
	// changes, not a group: touch the bundles that may target one.
	if changed {
		for ns := range homes {
			if err := touchBundles(ctx, g.Client, ns, time.Now()); err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{RequeueAfter: 10 * time.Minute}, nil
}

func group(ns, name, kind string, matchLabels map[string]any) *unstructured.Unstructured {
	u := object(clusterGroupGVK)
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(map[string]string{clusters.LabelManagedBy: clusters.ManagedBy, delivery.LabelGroupKind: kind})
	if matchLabels != nil {
		u.Object["spec"] = map[string]any{"selector": map[string]any{"matchLabels": matchLabels}}
	}
	return u
}

// ClusterLabels labels each Cluster with its environment and name, which
// CAAPF copies to its Fleet cluster, so environment groups and single-
// cluster Deployments in a template namespace can select it.
type ClusterLabels struct {
	Client client.Client
}

// Reconcile implements reconcile.Reconciler.
func (r *ClusterLabels) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c := object(clusterGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, c); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if c.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	l := c.GetLabels()
	if l[delivery.LabelEnvironment] == c.GetNamespace() && l[delivery.LabelCluster] == c.GetName() {
		return reconcile.Result{}, nil
	}
	patch := client.MergeFrom(c.DeepCopy())
	if l == nil {
		l = map[string]string{}
	}
	l[delivery.LabelEnvironment] = c.GetNamespace()
	l[delivery.LabelCluster] = c.GetName()
	c.SetLabels(l)
	return reconcile.Result{}, client.IgnoreNotFound(r.Client.Patch(ctx, c, patch))
}

// touchBundles annotates every bundle in ns, so Fleet works out its targets
// again: it doesn't when a group or a BundleNamespaceMapping appears.
func touchBundles(ctx context.Context, c client.Client, ns string, now time.Time) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(bundleGVK.GroupVersion().WithKind("BundleList"))
	if err := c.List(ctx, list, client.InNamespace(ns)); err != nil {
		return err
	}
	for i := range list.Items {
		b := &list.Items[i]
		patch := client.MergeFrom(b.DeepCopy())
		ann := b.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann["hyve.io/retargeted"] = now.UTC().Format(time.RFC3339Nano)
		b.SetAnnotations(ann)
		if err := c.Patch(ctx, b, patch); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
