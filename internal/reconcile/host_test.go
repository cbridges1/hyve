package reconcile

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

func TestIsHostClusterWithoutDriver(t *testing.T) {
	t.Run("primary marker, no driver", func(t *testing.T) {
		c := types.ClusterDefinition{Spec: types.ClusterSpec{AccessMethod: types.AccessMethodPrimary}}
		assert.True(t, isHostClusterWithoutDriver(c))
	})

	t.Run("primary marker WITH a real driver is not a no-driver host cluster", func(t *testing.T) {
		c := types.ClusterDefinition{Spec: types.ClusterSpec{
			AccessMethod: types.AccessMethodPrimary,
			Driver:       types.DriverRef{Source: "./modules/civo", Version: "local"},
		}}
		assert.False(t, isHostClusterWithoutDriver(c))
	})

	t.Run("no driver but not primary-marked is an ordinary misconfigured cluster, not a host cluster", func(t *testing.T) {
		c := types.ClusterDefinition{}
		assert.False(t, isHostClusterWithoutDriver(c))
	})
}

// fakeHostKubeconfigIssuer records whether it was called and returns a
// canned kubeconfig (or error).
type fakeHostKubeconfigIssuer struct {
	called bool
	kc     []byte
	err    error
}

func (f *fakeHostKubeconfigIssuer) MintHostKubeconfig(ctx context.Context) ([]byte, error) {
	f.called = true
	return f.kc, f.err
}

func TestReconcileHostCluster_DeleteMarked_NoOpSuccess(t *testing.T) {
	issuer := &fakeHostKubeconfigIssuer{}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	r.HostKubeconfigIssuer = issuer

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "host"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary, Delete: true},
	}
	err := r.reconcileHostCluster(context.Background(), cluster, &module.LockFile{Version: 1}, false, nil, &ReconcileHooks{})
	require.NoError(t, err)
	assert.False(t, issuer.called, "delete-marked host cluster must not attempt to mint a kubeconfig at all")
}

func TestReconcileHostCluster_NoIssuerConfigured_SoftNoOp(t *testing.T) {
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	// r.HostKubeconfigIssuer left nil — local/file mode's own default.

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "host"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary},
	}
	err := r.reconcileHostCluster(context.Background(), cluster, &module.LockFile{Version: 1}, false, nil, &ReconcileHooks{})
	require.NoError(t, err, "nil issuer must be a soft no-op, not an error")
}

func TestReconcileHostCluster_MintError_PropagatesAsError(t *testing.T) {
	issuer := &fakeHostKubeconfigIssuer{err: fmt.Errorf("boom")}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	r.HostKubeconfigIssuer = issuer

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "host"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary},
	}
	err := r.reconcileHostCluster(context.Background(), cluster, &module.LockFile{Version: 1}, false, nil, &ReconcileHooks{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

// TestReconcileHostCluster_NoResources_MintsAndSucceeds confirms the
// no-module path actually reaches reconcileResources with a working
// KUBECONFIG env var set from the minted kubeconfig — with zero
// spec.resources declared, reconcileResources is a fast no-op, so this
// exercises the full mint-kubeconfig-write-temp-file-set-env sequence
// without needing a real cluster or kubectl/helm to actually run against.
func TestReconcileHostCluster_NoResources_MintsAndSucceeds(t *testing.T) {
	issuer := &fakeHostKubeconfigIssuer{kc: []byte("apiVersion: v1\nkind: Config\n")}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	r.HostKubeconfigIssuer = issuer

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "host"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary},
	}
	err := r.reconcileHostCluster(context.Background(), cluster, &module.LockFile{Version: 1}, false, nil, &ReconcileHooks{})
	require.NoError(t, err)
	assert.True(t, issuer.called)
}
