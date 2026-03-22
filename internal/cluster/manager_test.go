package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hyve/internal/provider"
	"hyve/internal/types"
)

// mockProvider implements the provider.Provider interface for testing
type mockProvider struct {
	clusters     map[string]*provider.Cluster
	shouldError  bool
	errorMessage string
	clusterInfos map[string]*provider.ClusterInfo
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		clusters:     make(map[string]*provider.Cluster),
		clusterInfos: make(map[string]*provider.ClusterInfo),
	}
}

// ClusterProvider methods
func (m *mockProvider) ListClusters(ctx context.Context) ([]*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New(m.errorMessage)
	}

	clusters := make([]*provider.Cluster, 0, len(m.clusters))
	for _, cluster := range m.clusters {
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

func (m *mockProvider) GetCluster(ctx context.Context, clusterID string) (*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New(m.errorMessage)
	}

	for _, cluster := range m.clusters {
		if cluster.ID == clusterID {
			return cluster, nil
		}
	}
	return nil, errors.New("cluster not found")
}

func (m *mockProvider) FindClusterByName(ctx context.Context, name string) (*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New(m.errorMessage)
	}

	cluster, exists := m.clusters[name]
	if !exists {
		return nil, nil
	}
	return cluster, nil
}

func (m *mockProvider) CreateCluster(ctx context.Context, config *provider.ClusterConfig) (*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New(m.errorMessage)
	}

	cluster := &provider.Cluster{
		ID:        "cluster-" + config.Name,
		Name:      config.Name,
		Status:    "BUILDING",
		CreatedAt: time.Now(),
	}
	m.clusters[config.Name] = cluster
	return cluster, nil
}

func (m *mockProvider) UpdateCluster(ctx context.Context, clusterID string, config *provider.ClusterUpdateConfig) (*provider.Cluster, error) {
	if m.shouldError {
		return nil, errors.New(m.errorMessage)
	}

	for _, cluster := range m.clusters {
		if cluster.ID == clusterID {
			cluster.Status = "UPDATING"
			return cluster, nil
		}
	}
	return nil, errors.New("cluster not found")
}

func (m *mockProvider) DeleteCluster(ctx context.Context, clusterID string) error {
	if m.shouldError {
		return errors.New(m.errorMessage)
	}

	for name, cluster := range m.clusters {
		if cluster.ID == clusterID {
			delete(m.clusters, name)
			return nil
		}
	}
	return errors.New("cluster not found")
}

func (m *mockProvider) WaitForClusterReady(ctx context.Context, clusterID string) error {
	if m.shouldError {
		return errors.New(m.errorMessage)
	}

	for _, cluster := range m.clusters {
		if cluster.ID == clusterID {
			cluster.Status = "ACTIVE"
			return nil
		}
	}
	return errors.New("cluster not found")
}

func (m *mockProvider) GetClusterInfo(ctx context.Context, name string) (*provider.ClusterInfo, error) {
	if m.shouldError {
		return nil, errors.New(m.errorMessage)
	}

	info, exists := m.clusterInfos[name]
	if !exists {
		return nil, errors.New("cluster info not found")
	}
	return info, nil
}

// FirewallProvider methods (not used in cluster manager but required by interface)
func (m *mockProvider) ListFirewalls(ctx context.Context) ([]*provider.Firewall, error) {
	return nil, nil
}

func (m *mockProvider) CreateFirewall(ctx context.Context, config *provider.FirewallConfig) (*provider.Firewall, error) {
	return nil, nil
}

func (m *mockProvider) DeleteFirewall(ctx context.Context, firewallID string) error {
	return nil
}

func (m *mockProvider) FindFirewallByName(ctx context.Context, name string) (*provider.Firewall, error) {
	return nil, nil
}

// Provider metadata
func (m *mockProvider) Name() string {
	return "mock"
}

func (m *mockProvider) Region() string {
	return "mock-region"
}

// TestDetermineAction_Create tests that DetermineAction returns ActionCreate for new cluster
func TestDetermineAction_Create(t *testing.T) {
	mgr := NewManager(newMockProvider())

	desired := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "new-cluster",
			Region: "mock-region",
		},
	}

	action := mgr.DetermineAction(context.Background(), desired)
	assert.Equal(t, types.ActionCreate, action)
}

// TestDetermineAction_None tests that DetermineAction returns ActionNone for active cluster
func TestDetermineAction_None(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["existing-cluster"] = &provider.Cluster{
		ID:     "cluster-1",
		Name:   "existing-cluster",
		Status: "ACTIVE",
	}

	mgr := NewManager(mockProv)

	desired := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "existing-cluster",
			Region: "mock-region",
		},
	}

	action := mgr.DetermineAction(context.Background(), desired)
	assert.Equal(t, types.ActionNone, action)
}

// TestDetermineAction_CreateForFailedCluster tests recreation for failed cluster
func TestDetermineAction_CreateForFailedCluster(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["failed-cluster"] = &provider.Cluster{
		ID:     "cluster-1",
		Name:   "failed-cluster",
		Status: "FAILED",
	}

	mgr := NewManager(mockProv)

	desired := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "failed-cluster",
			Region: "mock-region",
		},
	}

	action := mgr.DetermineAction(context.Background(), desired)
	assert.Equal(t, types.ActionCreate, action)
}

// TestFindByName tests finding a cluster by name
func TestFindByName(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["test-cluster"] = &provider.Cluster{
		ID:     "cluster-1",
		Name:   "test-cluster",
		Status: "ACTIVE",
	}

	mgr := NewManager(mockProv)

	cluster, err := mgr.FindByName(context.Background(), "test-cluster")
	require.NoError(t, err)
	assert.Equal(t, "test-cluster", cluster.Name)
}

// TestFindByName_NotFound tests finding a non-existent cluster
func TestFindByName_NotFound(t *testing.T) {
	mgr := NewManager(newMockProvider())

	cluster, err := mgr.FindByName(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, cluster)
}

// TestCreate tests creating a new cluster
func TestCreate(t *testing.T) {
	mgr := NewManager(newMockProvider())

	clusterDef := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "new-cluster",
			Region: "mock-region",
		},
		Spec: types.ClusterSpec{
			Nodes:       []string{"g4s.kube.small:2"},
			ClusterType: "k3s",
		},
	}

	cluster, err := mgr.Create(context.Background(), clusterDef)
	require.NoError(t, err)
	assert.Equal(t, "new-cluster", cluster.Name)
	assert.Equal(t, "BUILDING", cluster.Status)
}

// TestUpdate tests updating an existing cluster
func TestUpdate(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["existing-cluster"] = &provider.Cluster{
		ID:     "cluster-1",
		Name:   "existing-cluster",
		Status: "ACTIVE",
	}

	mgr := NewManager(mockProv)

	clusterDef := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "existing-cluster",
			Region: "mock-region",
		},
		Spec: types.ClusterSpec{
			Nodes: []string{"g4s.kube.small:3"},
		},
	}

	err := mgr.Update(context.Background(), clusterDef)
	require.NoError(t, err)
}

// TestUpdate_NotFound tests updating a non-existent cluster
func TestUpdate_NotFound(t *testing.T) {
	mgr := NewManager(newMockProvider())

	clusterDef := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "nonexistent",
			Region: "mock-region",
		},
	}

	err := mgr.Update(context.Background(), clusterDef)
	assert.Error(t, err)
}

// TestDelete tests deleting a cluster
func TestDelete(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["test-cluster"] = &provider.Cluster{
		ID:     "cluster-1",
		Name:   "test-cluster",
		Status: "ACTIVE",
	}

	mgr := NewManager(mockProv)

	err := mgr.Delete(context.Background(), "cluster-1")
	require.NoError(t, err)
	assert.NotContains(t, mockProv.clusters, "test-cluster")
}

// TestWaitForReady tests waiting for cluster to be ready
func TestWaitForReady(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["test-cluster"] = &provider.Cluster{
		ID:     "cluster-1",
		Name:   "test-cluster",
		Status: "BUILDING",
	}

	mgr := NewManager(mockProv)

	err := mgr.WaitForReady(context.Background(), "cluster-1")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", mockProv.clusters["test-cluster"].Status)
}

// TestFindOrphaned tests finding orphaned clusters
func TestFindOrphaned(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["hyve-managed"] = &provider.Cluster{ID: "cluster-1", Name: "hyve-managed", Status: "ACTIVE"}
	mockProv.clusters["hyve-orphaned"] = &provider.Cluster{ID: "cluster-2", Name: "hyve-orphaned", Status: "ACTIVE"}
	mockProv.clusters["unmanaged-cluster"] = &provider.Cluster{ID: "cluster-3", Name: "unmanaged-cluster", Status: "ACTIVE"}

	mgr := NewManager(mockProv)

	desiredClusters := []types.ClusterDefinition{
		{Metadata: types.ClusterMetadata{Name: "hyve-managed"}},
	}

	orphaned, err := mgr.FindOrphaned(context.Background(), desiredClusters)
	require.NoError(t, err)
	// Should find hyve-orphaned but not unmanaged-cluster
	require.Len(t, orphaned, 1)
	assert.Equal(t, "hyve-orphaned", orphaned[0].Name)
}

// TestShouldManage tests the ShouldManage logic
func TestShouldManage(t *testing.T) {
	mgr := NewManager(newMockProvider())

	testCases := []struct {
		name     string
		cluster  provider.Cluster
		expected bool
	}{
		{"hyve prefix", provider.Cluster{Name: "hyve-test-cluster"}, true},
		{"civo-deploy prefix", provider.Cluster{Name: "civo-deploy-test"}, true},
		{"unmanaged cluster", provider.Cluster{Name: "my-custom-cluster"}, false},
		{"empty name", provider.Cluster{Name: ""}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, mgr.ShouldManage(tc.cluster))
		})
	}
}

// TestGetClusterInfo tests getting cluster information
func TestGetClusterInfo(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusterInfos["test-cluster"] = &provider.ClusterInfo{
		Name:       "test-cluster",
		IPAddress:  "1.2.3.4",
		AccessPort: "6443",
		Status:     "ACTIVE",
		ID:         "cluster-1",
	}

	mgr := NewManager(mockProv)

	info, err := mgr.GetClusterInfo(context.Background(), "test-cluster")
	require.NoError(t, err)
	assert.Equal(t, "test-cluster", info.Name)
	assert.Equal(t, "1.2.3.4", info.IPAddress)
}

// TestCleanupOrphaned tests cleaning up orphaned clusters
func TestCleanupOrphaned(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["orphan1"] = &provider.Cluster{ID: "cluster-1", Name: "orphan1", Status: "ACTIVE"}
	mockProv.clusters["orphan2"] = &provider.Cluster{ID: "cluster-2", Name: "orphan2", Status: "ACTIVE"}

	mgr := NewManager(mockProv)

	orphaned := []*provider.Cluster{
		mockProv.clusters["orphan1"],
		mockProv.clusters["orphan2"],
	}

	err := mgr.CleanupOrphaned(context.Background(), orphaned)
	require.NoError(t, err)
	assert.Empty(t, mockProv.clusters)
}

// TestErrorHandling tests error handling in various operations
func TestErrorHandling(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.shouldError = true
	mockProv.errorMessage = "mock error"

	mgr := NewManager(mockProv)

	clusterDef := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{
			Name:   "test-cluster",
			Region: "mock-region",
		},
	}

	_, err := mgr.Create(context.Background(), clusterDef)
	assert.Error(t, err)

	err = mgr.Delete(context.Background(), "cluster-1")
	assert.Error(t, err)

	err = mgr.WaitForReady(context.Background(), "cluster-1")
	assert.Error(t, err)

	_, err = mgr.GetClusterInfo(context.Background(), "test-cluster")
	assert.Error(t, err)
}

// TestStrictDeleteOrphans tests that StrictDeleteOrphans deletes all cloud clusters
// not present in the desired list, regardless of name prefix.
func TestStrictDeleteOrphans(t *testing.T) {
	mockProv := newMockProvider()
	mockProv.clusters["hyve-managed"] = &provider.Cluster{ID: "c1", Name: "hyve-managed", Status: "ACTIVE"}
	mockProv.clusters["hyve-orphaned"] = &provider.Cluster{ID: "c2", Name: "hyve-orphaned", Status: "ACTIVE"}
	mockProv.clusters["unmanaged-cluster"] = &provider.Cluster{ID: "c3", Name: "unmanaged-cluster", Status: "ACTIVE"}

	mgr := NewManager(mockProv)

	desiredClusters := []types.ClusterDefinition{
		{Metadata: types.ClusterMetadata{Name: "hyve-managed"}},
	}

	err := mgr.StrictDeleteOrphans(context.Background(), desiredClusters)
	require.NoError(t, err)

	// hyve-managed should survive; hyve-orphaned and unmanaged-cluster should be gone.
	assert.Contains(t, mockProv.clusters, "hyve-managed")
	assert.NotContains(t, mockProv.clusters, "hyve-orphaned")
	assert.NotContains(t, mockProv.clusters, "unmanaged-cluster")
}
