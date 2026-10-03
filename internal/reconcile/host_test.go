package reconcile

import (
	"context"
	"fmt"
	"os"
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

func TestMgmtKubeconfigLocator_HostClusterMintsKubeconfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	issuer := &fakeHostKubeconfigIssuer{kc: []byte("minted")}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir(), defs: []types.ClusterDefinition{{
		Metadata: types.ClusterMetadata{Name: "unraid-k3s"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary},
	}}})
	r.HostKubeconfigIssuer = issuer

	path, err := r.mgmtKubeconfigLocatorFor(types.ClusterDefinition{})(context.Background(), "unraid-k3s")
	require.NoError(t, err)
	assert.True(t, issuer.called)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "minted", string(data))
	authPath, _ := module.KubeconfigPathForCluster("unraid-k3s")
	assert.NotEqual(t, authPath, path, "must not overwrite the cluster's own auth kubeconfig")
}

func TestMgmtKubeconfigLocator_NonHostUsesAuthKubeconfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	issuer := &fakeHostKubeconfigIssuer{kc: []byte("minted")}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir(), defs: []types.ClusterDefinition{{
		Metadata: types.ClusterMetadata{Name: "capi-mgmt"},
	}}})
	r.HostKubeconfigIssuer = issuer

	authPath, err := module.KubeconfigPathForCluster("capi-mgmt")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(authPath, []byte("auth"), 0o600))

	path, err := r.mgmtKubeconfigLocatorFor(types.ClusterDefinition{})(context.Background(), "capi-mgmt")
	require.NoError(t, err)
	assert.Equal(t, authPath, path)
	assert.False(t, issuer.called)
}

func TestCheckDependencyStatus_DriverlessHostIsActive(t *testing.T) {
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	host := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "unraid-k3s"},
		Spec:     types.ClusterSpec{AccessMethod: types.AccessMethodPrimary},
	}
	assert.Equal(t, "ACTIVE", r.checkDependencyStatus(context.Background(), host, &module.LockFile{Version: 1}, nil))
}

// envDef builds a cluster-mode ClusterDefinition as crdconv produces it:
// real name "<env>-<short>", Environment from the hyve.io/environment label.
func envDef(env, short string, spec types.ClusterSpec) types.ClusterDefinition {
	return types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: env + "-" + short, Environment: env}, Spec: spec}
}

func TestResolveClusterRef(t *testing.T) {
	host := envDef("default", "unraid-k3s", types.ClusterSpec{AccessMethod: types.AccessMethodPrimary})
	other := envDef("staging", "unraid-k3s", types.ClusterSpec{})
	exact := types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "unraid-k3s"}}
	from := envDef("default", "gke-1", types.ClusterSpec{})

	got, ok := resolveClusterRef([]types.ClusterDefinition{other, host}, from, "unraid-k3s")
	require.True(t, ok, "a short name resolves within the referencing cluster's environment")
	assert.Equal(t, "default-unraid-k3s", got.Metadata.Name)

	got, ok = resolveClusterRef([]types.ClusterDefinition{host, exact}, from, "unraid-k3s")
	require.True(t, ok)
	assert.Equal(t, "unraid-k3s", got.Metadata.Name, "an exact name match wins")

	_, ok = resolveClusterRef([]types.ClusterDefinition{other}, from, "unraid-k3s")
	assert.False(t, ok, "never resolves into a different environment")

	_, ok = resolveClusterRef([]types.ClusterDefinition{host}, types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "gke-1"}}, "unraid-k3s")
	assert.False(t, ok, "no environment (local mode): exact names only")
}

func TestValidateMgmtClusterRequirement_EnvironmentPrefixedName(t *testing.T) {
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir(), defs: []types.ClusterDefinition{
		envDef("default", "unraid-k3s", types.ClusterSpec{AccessMethod: types.AccessMethodPrimary}),
	}})
	assert.NoError(t, r.validateMgmtClusterRequirement(context.Background(), envDef("default", "gke-1", types.ClusterSpec{}), "unraid-k3s"))
	err := r.validateMgmtClusterRequirement(context.Background(), envDef("staging", "gke-1", types.ClusterSpec{}), "unraid-k3s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "doesn't exist")
}

func TestMgmtKubeconfigLocator_EnvironmentPrefixedHostMints(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	issuer := &fakeHostKubeconfigIssuer{kc: []byte("minted")}
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir(), defs: []types.ClusterDefinition{
		envDef("default", "unraid-k3s", types.ClusterSpec{AccessMethod: types.AccessMethodPrimary}),
	}})
	r.HostKubeconfigIssuer = issuer

	path, err := r.mgmtKubeconfigLocatorFor(envDef("default", "gke-1", types.ClusterSpec{}))(context.Background(), "unraid-k3s")
	require.NoError(t, err)
	assert.True(t, issuer.called)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "minted", string(data))

	_, err = r.mgmtKubeconfigLocatorFor(envDef("staging", "gke-1", types.ClusterSpec{}))(context.Background(), "unraid-k3s")
	assert.Error(t, err, "a cluster in another environment doesn't see it")
}

func TestUnmetDependency_EnvironmentPrefixedHost(t *testing.T) {
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir(), defs: []types.ClusterDefinition{
		envDef("default", "unraid-k3s", types.ClusterSpec{AccessMethod: types.AccessMethodPrimary}),
	}})
	unmet, err := r.unmetDependency(context.Background(), envDef("default", "gke-1", types.ClusterSpec{DependsOn: []string{"unraid-k3s"}}), &module.LockFile{Version: 1}, nil)
	require.NoError(t, err)
	assert.Empty(t, unmet, "dependsOn by short name resolves to the environment's host cluster")
}

// TestMgmtCluster_HostFallback: a cluster in an organization on the home
// cluster can name the control plane's host as its mgmtCluster only when
// the install shares it; its own clusters still win.
func TestMgmtCluster_HostFallback(t *testing.T) {
	host := envDef("default", "unraid-k3s", types.ClusterSpec{AccessMethod: types.AccessMethodPrimary})
	from := envDef("staging", "gke-1", types.ClusterSpec{})
	newR := func(allowed bool, own ...types.ClusterDefinition) (*Reconciler, *fakeHostKubeconfigIssuer) {
		r := NewReconciler(&fakeStateProvider{localPath: t.TempDir(), defs: own})
		issuer := &fakeHostKubeconfigIssuer{kc: []byte("minted")}
		r.HostKubeconfigIssuer = issuer
		r.HostClusters = func(context.Context) ([]types.ClusterDefinition, bool, error) {
			return []types.ClusterDefinition{host}, allowed, nil
		}
		return r, issuer
	}

	t.Run("shared: resolves by short name from any environment and mints", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		r, issuer := newR(true)
		assert.NoError(t, r.validateMgmtClusterRequirement(context.Background(), from, "unraid-k3s"))
		path, err := r.mgmtKubeconfigLocatorFor(from)(context.Background(), "unraid-k3s")
		require.NoError(t, err)
		assert.True(t, issuer.called)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "minted", string(data))
	})

	t.Run("not shared: says how to allow it, never mints", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		r, issuer := newR(false)
		err := r.validateMgmtClusterRequirement(context.Background(), from, "unraid-k3s")
		require.Error(t, err)
		assert.ErrorIs(t, err, errHostClusterNotShared)
		_, err = r.mgmtKubeconfigLocatorFor(from)(context.Background(), "unraid-k3s")
		assert.ErrorIs(t, err, errHostClusterNotShared)
		assert.False(t, issuer.called)
	})

	t.Run("the organization's own cluster of that name wins", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		r, issuer := newR(false, envDef("staging", "unraid-k3s", types.ClusterSpec{}))
		assert.NoError(t, r.validateMgmtClusterRequirement(context.Background(), from, "unraid-k3s"))
		assert.False(t, issuer.called)
	})

	t.Run("an unknown name is still not found", func(t *testing.T) {
		r, _ := newR(true)
		err := r.validateMgmtClusterRequirement(context.Background(), from, "nope")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "doesn't exist")
	})
}
