package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hyve/internal/types"
)

// newTestManager constructs a Manager directly, bypassing NewManager which requires a git backend.
func newTestManager(stateDir string) *Manager {
	return &Manager{stateDir: stateDir}
}

func TestGetStateRoot(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	mgr := newTestManager(stateDir)
	assert.Equal(t, tmpDir, mgr.GetStateRoot())
}

func TestLoadRepoConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	mgr := newTestManager(stateDir)
	cfg, err := mgr.LoadRepoConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, ReconcileModeLocal, cfg.Reconcile.Mode)
	assert.False(t, cfg.Reconcile.StrictDelete)
}

func TestLoadRepoConfig_WithLocalMode(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	content := "reconcile:\n  mode: local\n  strictDelete: false\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "hyve.yaml"), []byte(content), 0644))

	mgr := newTestManager(stateDir)
	cfg, err := mgr.LoadRepoConfig()
	require.NoError(t, err)
	assert.Equal(t, ReconcileModeLocal, cfg.Reconcile.Mode)
	assert.False(t, cfg.Reconcile.StrictDelete)
}

func TestLoadRepoConfig_WithCICDMode(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	content := "reconcile:\n  mode: cicd\n  strictDelete: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "hyve.yaml"), []byte(content), 0644))

	mgr := newTestManager(stateDir)
	cfg, err := mgr.LoadRepoConfig()
	require.NoError(t, err)
	assert.Equal(t, ReconcileModeCICD, cfg.Reconcile.Mode)
	assert.True(t, cfg.Reconcile.StrictDelete)
}

func TestLoadRepoConfig_EmptyModeDefaultsToLocal(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	// hyve.yaml with no mode field set
	content := "reconcile:\n  strictDelete: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "hyve.yaml"), []byte(content), 0644))

	mgr := newTestManager(stateDir)
	cfg, err := mgr.LoadRepoConfig()
	require.NoError(t, err)
	assert.Equal(t, ReconcileModeLocal, cfg.Reconcile.Mode)
	assert.True(t, cfg.Reconcile.StrictDelete)
}

func TestLoadRepoConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "hyve.yaml"), []byte(":\n  invalid: [yaml"), 0644))

	mgr := newTestManager(stateDir)
	_, err := mgr.LoadRepoConfig()
	assert.Error(t, err)
}

func TestLoadClusterDefinitions_MissingDir(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters") // directory never created

	mgr := newTestManager(stateDir)
	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	assert.Empty(t, clusters)
}

func TestLoadClusterDefinitions_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	mgr := newTestManager(stateDir)
	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	assert.Empty(t, clusters)
}

func TestLoadClusterDefinitions_SingleCluster(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	yaml := `apiVersion: hyve/v1
kind: Cluster
metadata:
  name: my-cluster
  region: PHX1
spec:
  provider: civo
  nodes:
    - g4s.kube.medium
`
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "my-cluster.yaml"), []byte(yaml), 0644))

	mgr := newTestManager(stateDir)
	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "my-cluster", clusters[0].Metadata.Name)
	assert.Equal(t, "PHX1", clusters[0].Metadata.Region)
	assert.Equal(t, "civo", clusters[0].Spec.Provider)
}

func TestLoadClusterDefinitions_MultipleClusters(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	cluster1 := "metadata:\n  name: alpha\nspec:\n  provider: civo\n"
	cluster2 := "metadata:\n  name: beta\nspec:\n  provider: aws\n"
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "alpha.yaml"), []byte(cluster1), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "beta.yml"), []byte(cluster2), 0644))

	mgr := newTestManager(stateDir)
	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	assert.Len(t, clusters, 2)
}

func TestLoadClusterDefinitions_IgnoresNonYAMLFiles(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "cluster.yaml"), []byte("metadata:\n  name: real\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "README.md"), []byte("# docs"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "notes.txt"), []byte("notes"), 0644))

	mgr := newTestManager(stateDir)
	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	assert.Len(t, clusters, 1)
}

func TestLoadClusterDefinitions_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "bad.yaml"), []byte(":\n  [invalid"), 0644))

	mgr := newTestManager(stateDir)
	_, err := mgr.LoadClusterDefinitions()
	assert.Error(t, err)
}

func TestValidateClusterDefinitions(t *testing.T) {
	mgr := newTestManager(t.TempDir())

	clusters := []types.ClusterDefinition{
		{Metadata: types.ClusterMetadata{Name: "cluster-1"}},
		{Metadata: types.ClusterMetadata{Name: "cluster-2"}},
	}

	err := mgr.ValidateClusterDefinitions(clusters)
	assert.NoError(t, err)
}

func TestValidateClusterDefinitions_Empty(t *testing.T) {
	mgr := newTestManager(t.TempDir())
	err := mgr.ValidateClusterDefinitions(nil)
	assert.NoError(t, err)
}

func TestOrderClusters(t *testing.T) {
	mgr := newTestManager(t.TempDir())

	clusters := []types.ClusterDefinition{
		{Metadata: types.ClusterMetadata{Name: "z-cluster"}},
		{Metadata: types.ClusterMetadata{Name: "a-cluster"}},
		{Metadata: types.ClusterMetadata{Name: "m-cluster"}},
	}

	result := mgr.OrderClusters(clusters)
	require.Len(t, result, 3)
	assert.Equal(t, "z-cluster", result[0].Metadata.Name)
	assert.Equal(t, "a-cluster", result[1].Metadata.Name)
	assert.Equal(t, "m-cluster", result[2].Metadata.Name)
}

func TestOrderClusters_Empty(t *testing.T) {
	mgr := newTestManager(t.TempDir())
	result := mgr.OrderClusters(nil)
	assert.Nil(t, result)
}

func TestReconcileModeConstants(t *testing.T) {
	assert.Equal(t, ReconcileMode("local"), ReconcileModeLocal)
	assert.Equal(t, ReconcileMode("cicd"), ReconcileModeCICD)
}
