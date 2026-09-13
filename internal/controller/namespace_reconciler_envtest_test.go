//go:build envtest

// See envtest_integration_test.go's own header comment for why this file
// is gated behind `-tags envtest` rather than part of the default `go test
// ./...` run: it needs a real kube-apiserver's finalizer semantics (an
// object with a non-empty metadata.finalizers can't actually be removed by
// Delete, only marked with a deletionTimestamp), which controller-runtime's
// fake client doesn't faithfully reproduce. envtest starts etcd+
// kube-apiserver only, not kube-controller-manager, so there's no real
// namespace-content-cascade or final Namespace removal here — this test
// doesn't need either: it drives Reconcile directly (not through a running
// manager's watch loop) and only asserts on
// hyvev1alpha1.OrganizationNamespaceFinalizer's own presence/absence on the
// Namespace object, which is pure kube-apiserver behavior.
package controller

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestEnvtest_NamespaceReconciler_FinalizerRemovedOnlyOnceOwnedObjectsGone
// proves Milestone 5's core safety property (see
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md): a Terminating
// organization Namespace still holding a ClusterDefinition keeps its
// hyvev1alpha1.OrganizationNamespaceFinalizer — NamespaceReconciler must
// not remove it eagerly just because DeletionTimestamp is set — and only
// loses it once that ClusterDefinition is actually gone.
func TestEnvtest_NamespaceReconciler_FinalizerRemovedOnlyOnceOwnedObjectsGone(t *testing.T) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "deploy", "helm", "hyve", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	require.NoError(t, hyvev1alpha1.AddToScheme(scheme.Scheme))

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	require.NoError(t, err)

	ctx := context.Background()
	const nsName = "acme-org-test"

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	controllerutil.AddFinalizer(ns, hyvev1alpha1.OrganizationNamespaceFinalizer)
	require.NoError(t, k8sClient.Create(ctx, ns))

	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "still-here", Namespace: nsName},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Region: "local",
			Driver: hyvev1alpha1.DriverRef{Source: "./module", Version: "latest"},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, cd))

	// Issue the delete — kube-apiserver sets DeletionTimestamp but, since
	// our finalizer is present, does not actually remove the object.
	require.NoError(t, k8sClient.Delete(ctx, ns))

	reconciler := &NamespaceReconciler{Client: k8sClient}
	req := ctrl.Request{NamespacedName: k8stypes.NamespacedName{Name: nsName}}

	// First reconcile: the ClusterDefinition is still present, so the
	// finalizer must survive.
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	var afterFirst corev1.Namespace
	require.NoError(t, k8sClient.Get(ctx, k8stypes.NamespacedName{Name: nsName}, &afterFirst))
	require.True(t, controllerutil.ContainsFinalizer(&afterFirst, hyvev1alpha1.OrganizationNamespaceFinalizer),
		"finalizer must not be removed while a ClusterDefinition still exists in the namespace")
	require.NotNil(t, afterFirst.DeletionTimestamp)

	// Now actually remove the ClusterDefinition (it carries no finalizer
	// of its own in this test, so this is an immediate real delete) —
	// simulating ClusterDefinitionFinalizer's own cleanup having already
	// finished.
	require.NoError(t, k8sClient.Delete(ctx, cd))

	var gone hyvev1alpha1.ClusterDefinition
	err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "still-here", Namespace: nsName}, &gone)
	require.Error(t, err, "ClusterDefinition should be fully gone, not just marked for deletion")

	// Second reconcile: every hyve-owned object is gone now, so the
	// finalizer should be removed.
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	var afterSecond corev1.Namespace
	require.NoError(t, k8sClient.Get(ctx, k8stypes.NamespacedName{Name: nsName}, &afterSecond))
	require.False(t, controllerutil.ContainsFinalizer(&afterSecond, hyvev1alpha1.OrganizationNamespaceFinalizer),
		"finalizer should be removed once every hyve-owned object is confirmed gone")
}
