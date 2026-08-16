//go:build envtest

// This file only builds with `-tags envtest` (see Taskfile.yml/README for
// the exact invocation) — it spins up a real etcd+kube-apiserver pair via
// controller-runtime's envtest package, which most CI/dev environments
// don't have the binaries for by default (`setup-envtest use` fetches
// them). Kept out of the default `go test ./...` run for that reason, the
// same way this project already gates other environment-dependent
// verification (kind smoke tests) behind a manual step rather than making
// it part of the standard suite.
package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// TestEnvtest_ReconcileOneDrivesClusterDefinitionThroughCreate is the
// highest-value verification for this phase: it proves the exact same
// ReconcileOne path a file-based `hyve reconcile` run takes also correctly
// drives a real ClusterDefinition CR against a real (if minimal)
// kube-apiserver — "same engine, different source of truth," proven
// end-to-end rather than just asserted in the doc comments.
func TestEnvtest_ReconcileOneDrivesClusterDefinitionThroughCreate(t *testing.T) {
	modulesDir := t.TempDir()
	writeFakeModule(t, modulesDir)

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "deploy", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	require.NoError(t, hyvev1alpha1.AddToScheme(scheme.Scheme))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme.Scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0", // disabled
	})
	require.NoError(t, err)

	stateProvider := &CRDStateProvider{
		Client:         mgr.GetClient(),
		Namespace:      "default",
		ConfigName:     "hyve-config",
		ModulesDirPath: modulesDir,
	}
	reconciler := &ClusterDefinitionReconciler{
		Client:        mgr.GetClient(),
		Reconciler:    reconcile.NewReconciler(stateProvider),
		StateProvider: stateProvider,
		Namespace:     "default",
	}
	require.NoError(t, reconciler.SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = mgr.Start(ctx)
	}()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	// Use the manager's own client (cache-backed, matching what the
	// reconciler actually reads) to apply the ClusterDefinition, mirroring
	// `kubectl apply` in spirit.
	k8sClient := mgr.GetClient()

	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Region: "local",
			Driver: hyvev1alpha1.DriverRef{Source: "./module", Version: "latest"},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, cr))

	// Poll for the create to actually happen — the fake module's
	// create.sh writes a marker file and HYVE_FAKE_ID output, which
	// createCluster (internal/reconcile/manager.go) persists into
	// .status.driverOutputs via CRDStateProvider.SaveClusterDefinition.
	require.Eventually(t, func() bool {
		var got hyvev1alpha1.ClusterDefinition
		if err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: "default", Name: "demo"}, &got); err != nil {
			return false
		}
		return got.Status.DriverOutputs["HYVE_FAKE_ID"] == "abc123"
	}, 30*time.Second, 200*time.Millisecond, "ClusterDefinition never reached created state")

	_, err = os.Stat(filepath.Join(modulesDir, ".created-demo"))
	require.NoError(t, err, "fake module's create.sh should have run against the real apiserver-driven reconcile")

	// Confirm the finalizer was added on the create path (added on first
	// reconcile of any non-deleting object — see Reconcile's own logic).
	require.Eventually(t, func() bool {
		var got hyvev1alpha1.ClusterDefinition
		if err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: "default", Name: "demo"}, &got); err != nil {
			return false
		}
		for _, f := range got.Finalizers {
			if f == hyvev1alpha1.ClusterDefinitionFinalizer {
				return true
			}
		}
		return false
	}, 10*time.Second, 200*time.Millisecond, "finalizer was never added")

	// Now delete it for real — confirm the finalizer-gated cleanup path
	// runs delete.yaml (via the fake module) before the object actually
	// disappears from etcd.
	require.NoError(t, k8sClient.Delete(ctx, cr))

	require.Eventually(t, func() bool {
		var got hyvev1alpha1.ClusterDefinition
		err := k8sClient.Get(ctx, k8stypes.NamespacedName{Namespace: "default", Name: "demo"}, &got)
		return client.IgnoreNotFound(err) == nil && err != nil // true once genuinely gone (NotFound)
	}, 30*time.Second, 200*time.Millisecond, "ClusterDefinition was never actually deleted")

	_, err = os.Stat(filepath.Join(modulesDir, ".deleted-demo"))
	require.NoError(t, err, "fake module's delete.yaml/create.sh delete path should have run before the finalizer was removed")
}

// writeFakeModule writes a minimal local hyve module (status/create/delete
// only — no auth/scale, matching what a bare-minimum module needs) into
// dir/module, using plain shell scripts so this test has zero external
// tool dependencies.
func writeFakeModule(t *testing.T, dir string) {
	t.Helper()
	moduleDir := filepath.Join(dir, "module")
	require.NoError(t, os.MkdirAll(moduleDir, 0755))

	files := map[string]string{
		"module.yaml": "apiVersion: v1\nkind: ModuleManifest\nmetadata:\n  name: fake\n  type: driver\nspec:\n  params: []\n",
		"status.sh": fmt.Sprintf(`#!/bin/sh
if [ -f "%s/.created-$HYVE_CLUSTER_NAME" ]; then
  echo "HYVE_CLUSTER_STATUS=ACTIVE"
else
  echo "HYVE_CLUSTER_STATUS=NOT_FOUND"
fi
`, dir),
		"create.sh": fmt.Sprintf(`#!/bin/sh
touch "%s/.created-$HYVE_CLUSTER_NAME"
echo "HYVE_FAKE_ID=abc123"
`, dir),
		"delete.sh": fmt.Sprintf(`#!/bin/sh
rm -f "%s/.created-$HYVE_CLUSTER_NAME"
touch "%s/.deleted-$HYVE_CLUSTER_NAME"
`, dir, dir),
	}
	for name, content := range files {
		path := filepath.Join(moduleDir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0755))
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "hyve.lock"), []byte("version: 1\nmodules: {}\nworkflows: {}\n"), 0644))
}
