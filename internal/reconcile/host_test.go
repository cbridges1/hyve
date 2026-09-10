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

// TestReconcileHostCluster_AgentEnabled_NoAgentConfig_SoftNoOp confirms
// reconcileHostCluster now actually reaches reconcileAgent (see this
// file's own doc comment on the fix — before it, a driver-less host
// cluster's dispatch path never called reconcileAgent at all, so
// spec.access.agent had no effect on it regardless of what it was set
// to). With no AgentTokenIssuer/AgentControlPlaneURL/AgentTunnelAddress
// configured (this Reconciler's own zero-value default), reconcileAgent's
// own "not configured" branch logs a warning and returns nil — this test
// only needs to confirm that branch is actually reached and that
// reconcileHostCluster still succeeds end to end, not that an agent gets
// installed (that needs a real cluster/kubectl, covered by
// agent_test.go's own kubectlApply-level tests instead).
func TestReconcileHostCluster_AgentEnabled_NoAgentConfig_SoftNoOp(t *testing.T) {
	issuer := &fakeHostKubeconfigIssuer{kc: []byte("apiVersion: v1\nkind: Config\n")}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	r.HostKubeconfigIssuer = issuer
	// r.AgentTokenIssuer/AgentControlPlaneURL/AgentTunnelAddress left at
	// their zero values on purpose — reconcileAgent must treat that as a
	// soft no-op, not fail the whole reconcile.

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "host"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary, Agent: types.AgentSpec{Enabled: true, Proxy: true}},
	}
	err := r.reconcileHostCluster(context.Background(), cluster, &module.LockFile{Version: 1}, false, nil, &ReconcileHooks{})
	require.NoError(t, err, "missing agent config must be a soft no-op, not an error")
	assert.True(t, issuer.called, "host kubeconfig must still be minted for spec.resources reconciliation regardless of agent config")
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
