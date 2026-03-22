package kubeconfig

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hyve/internal/provider"
	"hyve/internal/types"
)

// mockProvider implements provider.Provider for testing without real cloud calls.
type mockProvider struct {
	clusters     map[string]*provider.Cluster
	clusterInfos map[string]*provider.ClusterInfo
	findErr      map[string]error
	infoErr      map[string]error
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		clusters:     make(map[string]*provider.Cluster),
		clusterInfos: make(map[string]*provider.ClusterInfo),
		findErr:      make(map[string]error),
		infoErr:      make(map[string]error),
	}
}

func (m *mockProvider) FindClusterByName(_ context.Context, name string) (*provider.Cluster, error) {
	if err, ok := m.findErr[name]; ok {
		return nil, err
	}
	c, ok := m.clusters[name]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (m *mockProvider) GetClusterInfo(_ context.Context, name string) (*provider.ClusterInfo, error) {
	if err, ok := m.infoErr[name]; ok {
		return nil, err
	}
	info, ok := m.clusterInfos[name]
	if !ok {
		return &provider.ClusterInfo{}, nil
	}
	return info, nil
}

// Unused interface methods — return zero values.
func (m *mockProvider) ListClusters(_ context.Context) ([]*provider.Cluster, error) { return nil, nil }
func (m *mockProvider) GetCluster(_ context.Context, _ string) (*provider.Cluster, error) {
	return nil, nil
}
func (m *mockProvider) CreateCluster(_ context.Context, _ *provider.ClusterConfig) (*provider.Cluster, error) {
	return nil, nil
}
func (m *mockProvider) UpdateCluster(_ context.Context, _ string, _ *provider.ClusterUpdateConfig) (*provider.Cluster, error) {
	return nil, nil
}
func (m *mockProvider) DeleteCluster(_ context.Context, _ string) error       { return nil }
func (m *mockProvider) WaitForClusterReady(_ context.Context, _ string) error { return nil }
func (m *mockProvider) ListFirewalls(_ context.Context) ([]*provider.Firewall, error) {
	return nil, nil
}
func (m *mockProvider) CreateFirewall(_ context.Context, _ *provider.FirewallConfig) (*provider.Firewall, error) {
	return nil, nil
}
func (m *mockProvider) DeleteFirewall(_ context.Context, _ string) error { return nil }
func (m *mockProvider) FindFirewallByName(_ context.Context, _ string) (*provider.Firewall, error) {
	return nil, nil
}
func (m *mockProvider) Name() string   { return "mock" }
func (m *mockProvider) Region() string { return "test-region" }

// clusterDef is a helper to build a minimal ClusterDefinition for tests.
func clusterDef(name string) types.ClusterDefinition {
	var def types.ClusterDefinition
	def.Metadata.Name = name
	return def
}

// activeCluster returns a mock cluster entry in ACTIVE status.
func activeCluster(name string) *provider.Cluster {
	return &provider.Cluster{ID: name + "-id", Name: name, Status: "ACTIVE"}
}

// --- NewSyncer ---

func TestNewSyncer(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	syncer := NewSyncer(mgr, newMockProvider())
	assert.NotNil(t, syncer)
}

// --- SyncKubeconfigs ---

func TestSyncKubeconfigs_Success(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.clusters["my-cluster"] = activeCluster("my-cluster")
	mock.clusterInfos["my-cluster"] = &provider.ClusterInfo{Kubeconfig: sampleKubeconfig}

	syncer := NewSyncer(mgr, mock)
	require.NoError(t, syncer.SyncKubeconfigs(context.Background(), []types.ClusterDefinition{clusterDef("my-cluster")}))

	kc, err := mgr.GetKubeconfig("my-cluster")
	require.NoError(t, err)
	require.NotNil(t, kc)

	config, err := kc.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, sampleKubeconfig, config)
}

func TestSyncKubeconfigs_MultipleCluster(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()

	for _, name := range []string{"cluster-a", "cluster-b", "cluster-c"} {
		mock.clusters[name] = activeCluster(name)
		mock.clusterInfos[name] = &provider.ClusterInfo{Kubeconfig: sampleKubeconfig}
	}

	defs := []types.ClusterDefinition{clusterDef("cluster-a"), clusterDef("cluster-b"), clusterDef("cluster-c")}
	require.NoError(t, NewSyncer(mgr, mock).SyncKubeconfigs(context.Background(), defs))

	list, err := mgr.ListKubeconfigs()
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestSyncKubeconfigs_ClusterNotActive(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.clusters["my-cluster"] = &provider.Cluster{ID: "c1", Name: "my-cluster", Status: "CREATING"}

	require.NoError(t, NewSyncer(mgr, mock).SyncKubeconfigs(context.Background(), []types.ClusterDefinition{clusterDef("my-cluster")}))

	kc, err := mgr.GetKubeconfig("my-cluster")
	assert.NoError(t, err)
	assert.Nil(t, kc, "non-active cluster should not have its kubeconfig stored")
}

func TestSyncKubeconfigs_ClusterNotFound(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	// cluster not registered in mock

	require.NoError(t, NewSyncer(mgr, newMockProvider()).SyncKubeconfigs(context.Background(), []types.ClusterDefinition{clusterDef("missing")}))

	kc, err := mgr.GetKubeconfig("missing")
	assert.NoError(t, err)
	assert.Nil(t, kc)
}

func TestSyncKubeconfigs_FindError(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.findErr["my-cluster"] = fmt.Errorf("api timeout")

	// Errors per-cluster are logged and skipped; the overall sync does not fail
	require.NoError(t, NewSyncer(mgr, mock).SyncKubeconfigs(context.Background(), []types.ClusterDefinition{clusterDef("my-cluster")}))

	kc, err := mgr.GetKubeconfig("my-cluster")
	assert.NoError(t, err)
	assert.Nil(t, kc)
}

func TestSyncKubeconfigs_NoKubeconfigAvailable(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.clusters["my-cluster"] = activeCluster("my-cluster")
	mock.clusterInfos["my-cluster"] = &provider.ClusterInfo{Kubeconfig: ""}

	require.NoError(t, NewSyncer(mgr, mock).SyncKubeconfigs(context.Background(), []types.ClusterDefinition{clusterDef("my-cluster")}))

	kc, err := mgr.GetKubeconfig("my-cluster")
	assert.NoError(t, err)
	assert.Nil(t, kc, "empty kubeconfig should not be stored")
}

func TestSyncKubeconfigs_CleansOrphans(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	// Pre-seed an orphaned entry
	_, err := mgr.StoreKubeconfig("orphan", sampleKubeconfig)
	require.NoError(t, err)

	mock := newMockProvider()
	mock.clusters["active-cluster"] = activeCluster("active-cluster")
	mock.clusterInfos["active-cluster"] = &provider.ClusterInfo{Kubeconfig: sampleKubeconfig}

	require.NoError(t, NewSyncer(mgr, mock).SyncKubeconfigs(context.Background(), []types.ClusterDefinition{clusterDef("active-cluster")}))

	orphan, err := mgr.GetKubeconfig("orphan")
	assert.NoError(t, err)
	assert.Nil(t, orphan, "orphaned kubeconfig should be removed")

	active, err := mgr.GetKubeconfig("active-cluster")
	assert.NoError(t, err)
	assert.NotNil(t, active, "active kubeconfig should be present")
}

func TestSyncKubeconfigs_Empty(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	err := NewSyncer(mgr, newMockProvider()).SyncKubeconfigs(context.Background(), []types.ClusterDefinition{})
	assert.NoError(t, err)
}

// --- SyncSingleKubeconfig ---

func TestSyncSingleKubeconfig_Success(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.clusters["my-cluster"] = activeCluster("my-cluster")
	mock.clusterInfos["my-cluster"] = &provider.ClusterInfo{Kubeconfig: sampleKubeconfig}

	syncer := NewSyncer(mgr, mock)
	require.NoError(t, syncer.SyncSingleKubeconfig(context.Background(), "my-cluster"))

	config, err := syncer.GetKubeconfigContent("my-cluster")
	require.NoError(t, err)
	assert.Equal(t, sampleKubeconfig, config)
}

func TestSyncSingleKubeconfig_NotFound(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	err := NewSyncer(mgr, newMockProvider()).SyncSingleKubeconfig(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSyncSingleKubeconfig_NotActive(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.clusters["my-cluster"] = &provider.Cluster{ID: "c1", Name: "my-cluster", Status: "DEGRADED"}

	err := NewSyncer(mgr, mock).SyncSingleKubeconfig(context.Background(), "my-cluster")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not active")
}

func TestSyncSingleKubeconfig_NoKubeconfig(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	mock := newMockProvider()
	mock.clusters["my-cluster"] = activeCluster("my-cluster")
	mock.clusterInfos["my-cluster"] = &provider.ClusterInfo{Kubeconfig: ""}

	err := NewSyncer(mgr, mock).SyncSingleKubeconfig(context.Background(), "my-cluster")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no kubeconfig available")
}

// --- GetKubeconfigContent ---

func TestGetKubeconfigContent_Success(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("my-cluster", sampleKubeconfig)
	require.NoError(t, err)

	content, err := NewSyncer(mgr, newMockProvider()).GetKubeconfigContent("my-cluster")
	require.NoError(t, err)
	assert.Equal(t, sampleKubeconfig, content)
}

func TestGetKubeconfigContent_NotStored(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := NewSyncer(mgr, newMockProvider()).GetKubeconfigContent("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- ListStoredKubeconfigs ---

func TestListStoredKubeconfigs(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	for _, name := range []string{"cluster-a", "cluster-b"} {
		_, err := mgr.StoreKubeconfig(name, sampleKubeconfig)
		require.NoError(t, err)
	}

	list, err := NewSyncer(mgr, newMockProvider()).ListStoredKubeconfigs()
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestListStoredKubeconfigs_Empty(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	list, err := NewSyncer(mgr, newMockProvider()).ListStoredKubeconfigs()
	require.NoError(t, err)
	assert.Empty(t, list)
}
