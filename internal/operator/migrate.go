package operator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// Before Deployments (internal/delivery), hyve kept what it put on
// clusters in a ClusterTemplate's spec.resources, in ClusterResources (one
// cluster's extras) and in shared Resources they referred to, and made a
// Fleet object owned by each. The migration turns each into a plain Fleet
// object a Deployment is: the same object (same name, so Fleet redeploys
// nothing), no longer owned by hyve and aimed at the template's group or
// the cluster. The template's spec.resources is then emptied, and the
// ClusterResource deleted. A template that still lists resources (from
// git, say) is migrated again; shared Resources are left as they are,
// since nothing reads them any more.

// How soon a migration that failed (a missing ref, say) is tried again.
const resyncPeriod = 2 * time.Minute

func setupMigrate(mgr manager.Manager) error {
	m := &Migrate{Client: mgr.GetClient()}
	hasResources := predicate.NewPredicateFuncs(func(o client.Object) bool {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			return false
		}
		rs, _, _ := unstructured.NestedSlice(u.Object, "spec", "resources")
		return len(rs) > 0
	})
	if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-migrate-template-resources").
		For(object(templateGVK), builder.WithPredicates(hasResources)).
		Complete(reconcile.Func(m.Template)); err != nil {
		return err
	}
	if !hasKind(mgr, clusterResourceGVK) || !hasKind(mgr, resourceGVK) {
		return nil
	}
	return ctrl.NewControllerManagedBy(mgr).Named("hyve-migrate-cluster-resources").
		For(object(clusterResourceGVK)).
		Complete(reconcile.Func(m.ClusterResource))
}

// Migrate turns template resources and ClusterResources into Deployments.
type Migrate struct {
	Client client.Client
}

func (m *Migrate) lookup(ctx context.Context, ns, name string) (*resources.Entry, error) {
	u := object(resourceGVK)
	if err := m.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		return nil, err
	}
	e, err := resources.EntryFromSpec(u)
	return &e, err
}

// Template migrates a ClusterTemplate's spec.resources.
func (m *Migrate) Template(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	u := object(templateGVK)
	if err := m.Client.Get(ctx, req.NamespacedName, u); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if u.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	t, err := templates.FromUnstructured(u)
	if err != nil || len(t.Spec.Resources) == 0 {
		return reconcile.Result{}, nil
	}
	owner := resources.Owner{Kind: resources.OwnerTemplate, Name: t.Name, Namespace: t.Namespace}
	target := []any{map[string]any{"clusterGroup": delivery.TemplateGroup(t.Name)}}
	var problems []string
	for _, e := range t.Spec.Resources {
		r, err := resources.Resolve(ctx, e, t.Namespace, m.lookup)
		if err == nil {
			err = m.apply(ctx, r, resources.ObjectName(owner, e.Name), t.Namespace, target)
		}
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		// Keep the list, and try again: nothing is lost meanwhile, since the
		// objects already made stay where they were.
		return reconcile.Result{RequeueAfter: resyncPeriod}, fmt.Errorf("ClusterTemplate %s/%s: %s", t.Namespace, t.Name, strings.Join(problems, "; "))
	}
	patch := []byte(`{"spec":{"resources":null}}`)
	if err := m.Client.Patch(ctx, u, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	status := []byte(`{"status":{"resources":null,"missingSecrets":null}}`)
	_ = m.Client.Status().Patch(ctx, u, client.RawPatch(types.MergePatchType, status))
	log.FromContext(ctx).Info("migrated template resources to Deployments", "template", req.NamespacedName.String(), "count", len(t.Spec.Resources))
	return reconcile.Result{}, nil
}

// ClusterResource migrates a cluster's extra.
func (m *Migrate) ClusterResource(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	u := object(clusterResourceGVK)
	if err := m.Client.Get(ctx, req.NamespacedName, u); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if u.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	clusterName, _, _ := unstructured.NestedString(u.Object, "spec", "clusterName")
	e, err := resources.EntryFromSpec(u)
	if err != nil || clusterName == "" {
		log.FromContext(ctx).Info("can't migrate ClusterResource", "name", req.NamespacedName.String(), "error", err)
		return reconcile.Result{}, nil
	}
	if n := u.GetLabels()[resources.LabelResource]; n != "" {
		e.Name = n
	}
	// A ref without a namespace was looked up beside the cluster's template,
	// then in the ClusterResource's own namespace.
	refNS := u.GetNamespace()
	cluster := object(clusterGVK)
	if err := m.Client.Get(ctx, types.NamespacedName{Namespace: u.GetNamespace(), Name: clusterName}, cluster); err == nil {
		if name, ns := templates.Ref(cluster); name != "" && e.RefNamespace == "" && e.Ref != "" {
			if _, err := m.lookup(ctx, ns, e.Ref); err == nil {
				refNS = ns
			}
		}
	}
	r, err := resources.Resolve(ctx, e, refNS, m.lookup)
	if err == nil {
		err = m.apply(ctx, r, u.GetName(), u.GetNamespace(), []any{map[string]any{"clusterName": clusterName}})
	}
	if err != nil {
		return reconcile.Result{RequeueAfter: resyncPeriod}, fmt.Errorf("ClusterResource %s: %w", req.NamespacedName, err)
	}
	if err := m.Client.Delete(ctx, u); client.IgnoreNotFound(err) != nil {
		return reconcile.Result{}, err
	}
	log.FromContext(ctx).Info("migrated ClusterResource to a Deployment", "name", req.NamespacedName.String())
	return reconcile.Result{}, nil
}

// apply writes the Fleet object as its own Deployment. Applying it with the
// same field manager that made it owned drops hyve's labels, annotation and
// owner reference, which only that manager set.
func (m *Migrate) apply(ctx context.Context, e resources.Entry, name, ns string, targets []any) error {
	obj, err := resources.FleetObject(e, name, ns, targets)
	if err != nil {
		return err
	}
	return m.Client.Patch(ctx, obj, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership)
}

// hasKind reports whether the API server serves a kind.
func hasKind(mgr manager.Manager, gvk schema.GroupVersionKind) bool {
	_, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	return err == nil
}
