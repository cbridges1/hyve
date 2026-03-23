package reconcile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterpkg "github.com/cbridges1/hyve/internal/cluster"
	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/types"
)

// ── mockProvider ─────────────────────────────────────────────────────────────

type mockProvider struct {
	clusters    map[string]*provider.Cluster
	shouldError bool
	infos       map[string]*provider.ClusterInfo
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		clusters: make(map[string]*provider.Cluster),
		infos:    make(map[string]*provider.ClusterInfo),
	}
}

func (m *mockProvider) ListClusters(_ context.Context) ([]*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New("mock error")
	}
	out := make([]*provider.Cluster, 0, len(m.clusters))
	for _, c := range m.clusters {
		out = append(out, c)
	}
	return out, nil
}

func (m *mockProvider) GetCluster(_ context.Context, id string) (*provider.Cluster, error) {
	for _, c := range m.clusters {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockProvider) FindClusterByName(_ context.Context, name string) (*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New("mock error")
	}
	c, ok := m.clusters[name]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (m *mockProvider) CreateCluster(_ context.Context, cfg *provider.ClusterConfig) (*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New("mock error")
	}
	c := &provider.Cluster{ID: "id-" + cfg.Name, Name: cfg.Name, Status: "BUILDING"}
	m.clusters[cfg.Name] = c
	return c, nil
}

func (m *mockProvider) UpdateCluster(_ context.Context, id string, _ *provider.ClusterUpdateConfig) (*provider.Cluster, error) {
	for _, c := range m.clusters {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockProvider) DeleteCluster(_ context.Context, id string) error {
	if m.shouldError {
		return errors.New("mock error")
	}
	for name, c := range m.clusters {
		if c.ID == id {
			delete(m.clusters, name)
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockProvider) WaitForClusterReady(_ context.Context, id string) error {
	for _, c := range m.clusters {
		if c.ID == id {
			c.Status = "ACTIVE"
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockProvider) GetClusterInfo(_ context.Context, name string) (*provider.ClusterInfo, error) {
	if m.shouldError {
		return nil, errors.New("mock error")
	}
	info, ok := m.infos[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return info, nil
}

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
func (m *mockProvider) Region() string { return "mock-region" }

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "clusters"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "provider-configs"), 0755))
	return dir
}

// ── dedupRegions ──────────────────────────────────────────────────────────────

func TestDedupRegions_Empty(t *testing.T) {
	assert.Empty(t, dedupRegions(nil))
}

func TestDedupRegions_NoDuplicates(t *testing.T) {
	in := []string{"us-east-1", "eu-west-1", "ap-southeast-1"}
	assert.Equal(t, in, dedupRegions(in))
}

func TestDedupRegions_WithDuplicates(t *testing.T) {
	in := []string{"us-east-1", "eu-west-1", "us-east-1", "eu-west-1", "ap-southeast-1"}
	got := dedupRegions(in)
	assert.Equal(t, []string{"us-east-1", "eu-west-1", "ap-southeast-1"}, got)
}

func TestDedupRegions_PreservesOrder(t *testing.T) {
	in := []string{"c", "b", "a", "b", "c"}
	assert.Equal(t, []string{"c", "b", "a"}, dedupRegions(in))
}

func TestDedupRegions_SingleElement(t *testing.T) {
	assert.Equal(t, []string{"us-east-1"}, dedupRegions([]string{"us-east-1"}))
}

// ── exportClusterInfoToEnv ────────────────────────────────────────────────────

func TestExportClusterInfoToEnv_InactiveCluster(t *testing.T) {
	mp := newMockProvider()
	mp.infos["staging"] = &provider.ClusterInfo{
		Name:   "staging",
		Status: "BUILDING",
		ID:     "c1",
	}
	mgr := clusterpkg.NewManager(mp)

	// Ensure the env var is not already set.
	t.Setenv("HYVE_CLUSTER_NAME", "")

	err := exportClusterInfoToEnv(context.Background(), mgr, "staging")
	require.NoError(t, err)
	// Inactive cluster must not export anything.
	assert.Empty(t, os.Getenv("HYVE_CLUSTER_NAME"))
}

func TestExportClusterInfoToEnv_ActiveCluster(t *testing.T) {
	mp := newMockProvider()
	mp.infos["prod"] = &provider.ClusterInfo{
		Name:       "prod",
		Status:     "ACTIVE",
		ID:         "c1",
		IPAddress:  "1.2.3.4",
		AccessPort: "6443",
		Kubeconfig: "kubeconfig-data",
	}
	mgr := clusterpkg.NewManager(mp)

	err := exportClusterInfoToEnv(context.Background(), mgr, "prod")
	require.NoError(t, err)

	assert.Equal(t, "prod", os.Getenv("HYVE_CLUSTER_NAME"))
	assert.Equal(t, "1.2.3.4", os.Getenv("HYVE_CLUSTER_IP_ADDRESS"))
	assert.Equal(t, "6443", os.Getenv("HYVE_CLUSTER_ACCESS_PORT"))
	assert.Equal(t, "c1", os.Getenv("HYVE_CLUSTER_ID"))
	assert.Equal(t, "ACTIVE", os.Getenv("HYVE_CLUSTER_STATUS"))
}

func TestExportClusterInfoToEnv_ProviderError(t *testing.T) {
	mp := newMockProvider()
	mp.shouldError = true
	mgr := clusterpkg.NewManager(mp)

	err := exportClusterInfoToEnv(context.Background(), mgr, "nonexistent")
	require.Error(t, err)
}

// ── reconcileCluster action dispatch (via cluster.Manager.DetermineAction) ───
// The Reconciler.reconcileCluster switch is thin — the real logic is in
// cluster.Manager.DetermineAction which is already tested in
// internal/cluster/manager_test.go. Here we verify the three paths that
// reconcileCluster dispatches to.

func TestReconcileCluster_ActionNone(t *testing.T) {
	mp := newMockProvider()
	mp.clusters["active"] = &provider.Cluster{ID: "id-active", Name: "active", Status: "ACTIVE"}
	mgr := clusterpkg.NewManager(mp)

	desired := types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "active"}}
	action := mgr.DetermineAction(context.Background(), desired)
	assert.Equal(t, types.ActionNone, action)
}

func TestReconcileCluster_ActionCreate(t *testing.T) {
	mgr := clusterpkg.NewManager(newMockProvider())
	desired := types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "brand-new"}}
	action := mgr.DetermineAction(context.Background(), desired)
	assert.Equal(t, types.ActionCreate, action)
}

func TestReconcileCluster_ActionCreateForFailedCluster(t *testing.T) {
	mp := newMockProvider()
	mp.clusters["broken"] = &provider.Cluster{ID: "id-broken", Name: "broken", Status: "FAILED"}
	mgr := clusterpkg.NewManager(mp)

	desired := types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "broken"}}
	action := mgr.DetermineAction(context.Background(), desired)
	assert.Equal(t, types.ActionCreate, action)
}

// ── newTestStateDir usage ─────────────────────────────────────────────────────

func TestNewTestStateDir_CreatesExpectedDirs(t *testing.T) {
	root := newTestStateDir(t)

	_, err := os.Stat(filepath.Join(root, "clusters"))
	require.NoError(t, err, "clusters dir should exist")

	_, err = os.Stat(filepath.Join(root, "provider-configs"))
	require.NoError(t, err, "provider-configs dir should exist")
}
