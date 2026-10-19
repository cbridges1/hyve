package operator

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
)

var (
	clusterClassGVK     = schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta2", Kind: "ClusterClass"}
	bundleGVK           = schema.GroupVersionKind{Group: "fleet.cattle.io", Version: "v1alpha1", Kind: "Bundle"}
	bundleDeploymentGVK = schema.GroupVersionKind{Group: "fleet.cattle.io", Version: "v1alpha1", Kind: "BundleDeployment"}
)

// LazyProviders adds a known Cluster API provider (install.EnsureProviders)
// the first time something needs its kinds: a ClusterClass that refers to
// them, or a Fleet bundle that failed to apply for want of their CRDs,
// such as a repo's ClusterClass and its templates. Local mode turns it on,
// so it starts with no providers.
type LazyProviders struct {
	Client client.Client
	Kube   *kube.Client
	GVK    schema.GroupVersionKind
	// Kinds are the kinds obj needs.
	Kinds func(obj *unstructured.Unstructured) []schema.GroupKind
}

func setupLazyProviders(mgr manager.Manager, kc *kube.Client) error {
	statusKinds := func(obj *unstructured.Unstructured) []schema.GroupKind {
		b, _ := json.Marshal(obj.Object["status"])
		return install.MissingKinds(string(b))
	}
	for _, l := range []*LazyProviders{
		{GVK: clusterClassGVK, Kinds: install.RefKinds},
		{GVK: bundleGVK, Kinds: statusKinds},
		{GVK: bundleDeploymentGVK, Kinds: statusKinds},
	} {
		l.Client, l.Kube = mgr.GetClient(), kc
		if err := ctrl.NewControllerManagedBy(mgr).Named("hyve-lazy-providers-" + l.GVK.Kind).
			For(object(l.GVK)).Complete(l); err != nil {
			return err
		}
	}
	return nil
}

func (l *LazyProviders) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	lg := log.FromContext(ctx)
	obj := object(l.GVK)
	if err := l.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	kinds := l.Kinds(obj)
	if len(kinds) == 0 {
		return reconcile.Result{}, nil
	}
	added, unknown, err := install.EnsureProviders(ctx, l.Kube, kinds, install.LeanOptions{
		Log: func(format string, args ...any) { lg.Info(fmt.Sprintf(format, args...)) },
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	}
	for _, p := range added {
		lg.Info("added a Cluster API provider", "provider", p, "for", l.GVK.Kind+" "+req.String())
	}
	if len(unknown) > 0 {
		lg.Info("needs kinds no provider hyve knows serves; add one with 'hyve provider add'", "kinds", fmt.Sprint(unknown))
	}
	return reconcile.Result{}, nil
}
