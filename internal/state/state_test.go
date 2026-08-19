package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/types"
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
	assert.False(t, cfg.Reconcile.StrictResourceDelete)
}

func TestLoadRepoConfig_StrictResourceDeleteTrue(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")

	content := "reconcile:\n  mode: local\n  strictResourceDelete: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "hyve.yaml"), []byte(content), 0644))

	mgr := newTestManager(stateDir)
	cfg, err := mgr.LoadRepoConfig()
	require.NoError(t, err)
	assert.True(t, cfg.Reconcile.StrictResourceDelete)
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

	yaml := `apiVersion: hyve.io/v1alpha1
kind: ClusterDefinition
metadata:
  name: my-cluster
spec:
  region: PHX1
  driver:
    source: github.com/example/civo-k3s
    version: 1.0.0
  params:
    nodes: g4s.kube.medium
`
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "my-cluster.yaml"), []byte(yaml), 0644))

	mgr := newTestManager(stateDir)
	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "my-cluster", clusters[0].Metadata.Name)
	assert.Equal(t, "PHX1", clusters[0].Metadata.Region)
	assert.Equal(t, "github.com/example/civo-k3s", clusters[0].Spec.Driver.Source)
}

func TestLoadClusterDefinitions_MultipleClusters(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	cluster1 := "apiVersion: hyve.io/v1alpha1\nkind: ClusterDefinition\nmetadata:\n  name: alpha\nspec:\n  region: civo\n"
	cluster2 := "apiVersion: hyve.io/v1alpha1\nkind: ClusterDefinition\nmetadata:\n  name: beta\nspec:\n  region: aws\n"
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

	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "cluster.yaml"), []byte("apiVersion: hyve.io/v1alpha1\nkind: ClusterDefinition\nmetadata:\n  name: real\n"), 0644))
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

// --- ClusterState sidecar split ---

func testClusterDef(name string) *types.ClusterDefinition {
	return &types.ClusterDefinition{
		APIVersion: "hyve.io/v1alpha1",
		Kind:       "ClusterDefinition",
		Metadata:   types.ClusterMetadata{Name: name, Region: "NYC1"},
		Spec: types.ClusterSpec{
			Driver: types.DriverRef{Source: "./custom-modules/civo", Version: "latest"},
			DriverOutputs: map[string]string{
				"HYVE_CLUSTER_ID": "abc-123",
			},
			AppliedResources: map[string]*types.AppliedResource{
				"toolbox-namespace": {
					SourceSHA256: "deadbeef",
					AppliedAt:    "2026-07-12T15:49:37Z",
					Objects: []types.AppliedObject{
						{APIVersion: "v1", Kind: "Namespace", Name: "toolbox"},
					},
				},
			},
		},
	}
}

func TestSaveClusterDefinition_SplitsSidecarFile(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := testClusterDef("sun-hyve")
	require.NoError(t, mgr.SaveClusterDefinition(def))

	primaryData, err := os.ReadFile(mgr.clusterPath("sun-hyve"))
	require.NoError(t, err)
	var primary map[string]interface{}
	require.NoError(t, yaml.Unmarshal(primaryData, &primary))
	spec, ok := primary["spec"].(map[string]interface{})
	require.True(t, ok)
	_, hasDriverOutputs := spec["driverOutputs"]
	_, hasAppliedResources := spec["appliedResources"]
	assert.False(t, hasDriverOutputs, "primary file must not contain driverOutputs")
	assert.False(t, hasAppliedResources, "primary file must not contain appliedResources")

	sidecarData, err := os.ReadFile(mgr.sidecarPath("sun-hyve"))
	require.NoError(t, err)
	var state types.ClusterState
	require.NoError(t, yaml.Unmarshal(sidecarData, &state))
	assert.Equal(t, "abc-123", state.DriverOutputs["HYVE_CLUSTER_ID"])
	require.Contains(t, state.AppliedResources, "toolbox-namespace")
	assert.Equal(t, "deadbeef", state.AppliedResources["toolbox-namespace"].SourceSHA256)
}

func TestSaveClusterDefinition_SidecarLivesOutsideStateDir(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := testClusterDef("sun-hyve")
	require.NoError(t, mgr.SaveClusterDefinition(def))

	assert.Equal(t, filepath.Join(filepath.Dir(stateDir), "cluster-state"), mgr.sidecarDir())

	entries, err := os.ReadDir(stateDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), stateSidecarSuffix),
			"stateDir must not contain any *.state.yaml file, found %q", e.Name())
	}

	_, err = os.Stat(mgr.sidecarPath("sun-hyve"))
	require.NoError(t, err, "sidecar file must exist in cluster-state/")
}

func TestSaveClusterDefinition_CreatesSidecarDirIfMissing(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	_, err := os.Stat(mgr.sidecarDir())
	require.True(t, os.IsNotExist(err), "cluster-state/ must not exist before the first save")

	require.NoError(t, mgr.SaveClusterDefinition(testClusterDef("sun-hyve")))

	info, err := os.Stat(mgr.sidecarDir())
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestSaveClusterDefinition_NoSidecarWhenStateEmpty(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := &types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "fresh"}}
	require.NoError(t, mgr.SaveClusterDefinition(def))

	_, err := os.Stat(mgr.sidecarPath("fresh"))
	assert.True(t, os.IsNotExist(err), "no sidecar file should be written for empty state")
}

func TestSaveClusterDefinition_RemovesStaleSidecarWhenStateBecomesEmpty(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := testClusterDef("sun-hyve")
	require.NoError(t, mgr.SaveClusterDefinition(def))
	_, err := os.Stat(mgr.sidecarPath("sun-hyve"))
	require.NoError(t, err, "sidecar should exist after first save")

	def.Spec.DriverOutputs = nil
	def.Spec.AppliedResources = nil
	require.NoError(t, mgr.SaveClusterDefinition(def))

	_, err = os.Stat(mgr.sidecarPath("sun-hyve"))
	assert.True(t, os.IsNotExist(err), "stale sidecar should be removed once state empties")
}

func TestSaveClusterDefinition_CreatesClustersDirIfMissing(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters") // never created
	mgr := newTestManager(stateDir)

	def := &types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "fresh"}}
	require.NoError(t, mgr.SaveClusterDefinition(def))

	_, err := os.Stat(mgr.clusterPath("fresh"))
	assert.NoError(t, err)
}

func TestSaveClusterDefinition_DoesNotMutateCallersDef(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := testClusterDef("sun-hyve")
	require.NoError(t, mgr.SaveClusterDefinition(def))

	// The caller's in-memory def must still have its maps populated —
	// SaveClusterDefinition must only clear a shallow copy, never def itself.
	assert.Equal(t, "abc-123", def.Spec.DriverOutputs["HYVE_CLUSTER_ID"])
	assert.Contains(t, def.Spec.AppliedResources, "toolbox-namespace")
}

func TestLoadClusterDefinition_MergesSidecar(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	mgr := newTestManager(stateDir)

	primary := "apiVersion: hyve.io/v1alpha1\nkind: ClusterDefinition\nmetadata:\n  name: sun-hyve\nspec:\n  driver:\n    source: ./custom-modules/civo\n"
	require.NoError(t, os.WriteFile(mgr.clusterPath("sun-hyve"), []byte(primary), 0644))
	sidecar := "driverOutputs:\n  HYVE_CLUSTER_ID: abc-123\nappliedResources:\n  toolbox-namespace:\n    sourceSHA256: deadbeef\n    appliedAt: \"2026-07-12T15:49:37Z\"\n"
	require.NoError(t, os.MkdirAll(mgr.sidecarDir(), 0755))
	require.NoError(t, os.WriteFile(mgr.sidecarPath("sun-hyve"), []byte(sidecar), 0644))

	def, rawData, err := mgr.LoadClusterDefinition("sun-hyve")
	require.NoError(t, err)
	assert.Equal(t, "abc-123", def.Spec.DriverOutputs["HYVE_CLUSTER_ID"])
	require.Contains(t, def.Spec.AppliedResources, "toolbox-namespace")
	assert.Equal(t, primary, string(rawData), "raw bytes must be exactly the primary file's contents")
}

func TestLoadClusterDefinition_RejectsLegacyFormat(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	mgr := newTestManager(stateDir)

	// The pre-unification file format (apiVersion: v1 / kind: Cluster) is no
	// longer accepted — local files must be real ClusterDefinition CR YAML
	// (hyve.io/v1alpha1) so `kubectl apply -f` works against them unmodified.
	legacy := `apiVersion: v1
kind: Cluster
metadata:
  name: sun-hyve
spec:
  driver:
    source: ./custom-modules/civo
`
	require.NoError(t, os.WriteFile(mgr.clusterPath("sun-hyve"), []byte(legacy), 0644))

	_, _, err := mgr.LoadClusterDefinition("sun-hyve")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hyve.io/v1alpha1")
}

func TestLoadClusterDefinition_SidecarWinsOverInlineData(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	mgr := newTestManager(stateDir)

	primary := `apiVersion: hyve.io/v1alpha1
kind: ClusterDefinition
metadata:
  name: sun-hyve
`
	require.NoError(t, os.WriteFile(mgr.clusterPath("sun-hyve"), []byte(primary), 0644))
	sidecar := "driverOutputs:\n  HYVE_CLUSTER_ID: fresh-sidecar-value\n"
	require.NoError(t, os.MkdirAll(mgr.sidecarDir(), 0755))
	require.NoError(t, os.WriteFile(mgr.sidecarPath("sun-hyve"), []byte(sidecar), 0644))

	def, _, err := mgr.LoadClusterDefinition("sun-hyve")
	require.NoError(t, err)
	assert.Equal(t, "fresh-sidecar-value", def.Spec.DriverOutputs["HYVE_CLUSTER_ID"])
}

func TestLoadClusterDefinition_NotFound(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	mgr := newTestManager(stateDir)

	_, _, err := mgr.LoadClusterDefinition("does-not-exist")
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err))
}

func TestLoadClusterDefinitions_SkipsStateSidecarFiles(t *testing.T) {
	// Sidecars normally live in cluster-state/, a sibling of stateDir — never
	// stateDir itself (see SaveClusterDefinition). This test covers the
	// defensive fallback: a stray *.state.yaml file sitting directly in
	// stateDir (e.g. left over from a pre-migration repo, or dropped there by
	// mistake) must still be skipped rather than parsed as a bogus second
	// cluster.
	stateDir := filepath.Join(t.TempDir(), "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	mgr := newTestManager(stateDir)

	def := testClusterDef("alpha")
	require.NoError(t, mgr.SaveClusterDefinition(def))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "alpha.state.yaml"), []byte("driverOutputs:\n  STRAY: value\n"), 0644))

	clusters, err := mgr.LoadClusterDefinitions()
	require.NoError(t, err)
	require.Len(t, clusters, 1, "a stray .state.yaml file in stateDir must not be parsed as a second cluster")
	assert.Equal(t, "alpha", clusters[0].Metadata.Name)
	assert.Contains(t, clusters[0].Spec.AppliedResources, "toolbox-namespace")
}

func TestRemoveClusterFile_RemovesSidecarToo(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := testClusterDef("sun-hyve")
	require.NoError(t, mgr.SaveClusterDefinition(def))
	require.NoError(t, mgr.RemoveClusterFile("sun-hyve"))

	_, err := os.Stat(mgr.clusterPath("sun-hyve"))
	assert.True(t, os.IsNotExist(err), "primary file should be removed")
	_, err = os.Stat(mgr.sidecarPath("sun-hyve"))
	assert.True(t, os.IsNotExist(err), "sidecar file should be removed")
}

func TestRemoveClusterFile_NoSidecarIsNotAnError(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := &types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "fresh"}}
	require.NoError(t, mgr.SaveClusterDefinition(def))
	assert.NoError(t, mgr.RemoveClusterFile("fresh"))
}

func TestHasStateSidecar_TrueAndFalse(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	assert.False(t, mgr.HasStateSidecar("sun-hyve"))
	require.NoError(t, mgr.SaveClusterDefinition(testClusterDef("sun-hyve")))
	assert.True(t, mgr.HasStateSidecar("sun-hyve"))
}

func TestSaveThenLoad_RoundTrip(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "clusters")
	mgr := newTestManager(stateDir)

	def := testClusterDef("sun-hyve")
	def.Spec.AppliedResources["newt"] = &types.AppliedResource{
		SourceSHA256: "abc123",
		Helm:         true,
		Namespace:    "default",
		AppliedAt:    "2026-07-12T15:49:48Z",
		Objects: []types.AppliedObject{
			{APIVersion: "v1", Kind: "ServiceAccount", Namespace: "default", Name: "newt-newt-sa"},
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "default", Name: "newt-newt-main-tunnel"},
		},
	}
	require.NoError(t, mgr.SaveClusterDefinition(def))

	loaded, _, err := mgr.LoadClusterDefinition("sun-hyve")
	require.NoError(t, err)
	assert.Equal(t, def.Spec.DriverOutputs, loaded.Spec.DriverOutputs)
	assert.Equal(t, def.Spec.AppliedResources, loaded.Spec.AppliedResources)
}
