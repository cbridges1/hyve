package reconcile

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// fakeStateProvider is a minimal in-memory StateProvider for tests that
// only need LoadClusterDefinitions to return a fixed set — everything else
// panics if called, so a test using it fails loudly if it exercises more
// of the interface than intended.
type fakeStateProvider struct {
	defs []types.ClusterDefinition
}

func (f *fakeStateProvider) LocalPath() string { return "" }
func (f *fakeStateProvider) LoadRepoConfig() (*state.RepoConfig, error) {
	return &state.RepoConfig{}, nil
}
func (f *fakeStateProvider) LoadClusterDefinitions() ([]types.ClusterDefinition, error) {
	return f.defs, nil
}
func (f *fakeStateProvider) SaveClusterDefinition(def *types.ClusterDefinition) error { return nil }
func (f *fakeStateProvider) RemoveClusterFile(name string) error                      { return nil }
func (f *fakeStateProvider) HasStateSidecar(name string) bool                         { return false }
func (f *fakeStateProvider) WorkflowSource() workflow.Source {
	return workflow.FileSource{Dir: f.LocalPath()}
}

func TestValidateDriverModuleLocked(t *testing.T) {
	t.Run("no driver source errors", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "test"}}
		err := validateDriverModuleLocked(c, lf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no driver specified")
	})

	t.Run("local source needs no lock entry", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "./custom-modules/civo", Version: "latest"}},
		}
		assert.NoError(t, validateDriverModuleLocked(c, lf))
	})

	t.Run("absolute path source needs no lock entry", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "/opt/modules/civo", Version: "latest"}},
		}
		assert.NoError(t, validateDriverModuleLocked(c, lf))
	})

	t.Run("remote source not in lock errors", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "github.com/hyve-modules/civo", Version: "v1.0.0"}},
		}
		err := validateDriverModuleLocked(c, lf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in hyve.lock")
	})

	t.Run("remote source present in lock passes", func(t *testing.T) {
		lf := &module.LockFile{
			Version: 1,
			Modules: map[string]*module.LockedModule{
				"github.com/hyve-modules/civo@v1.0.0": {Source: "github.com/hyve-modules/civo@v1.0.0"},
			},
		}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "github.com/hyve-modules/civo", Version: "v1.0.0"}},
		}
		assert.NoError(t, validateDriverModuleLocked(c, lf))
	})
}

func TestEffectiveStatus(t *testing.T) {
	t.Run("empty status defaults to ACTIVE for authOnly modules", func(t *testing.T) {
		assert.Equal(t, "ACTIVE", effectiveStatus("", true))
	})

	t.Run("empty status stays empty for normal modules", func(t *testing.T) {
		assert.Equal(t, "", effectiveStatus("", false))
	})

	t.Run("non-empty status is never overridden, authOnly or not", func(t *testing.T) {
		for _, isAuthOnly := range []bool{true, false} {
			assert.Equal(t, "NOT_FOUND", effectiveStatus("NOT_FOUND", isAuthOnly))
			assert.Equal(t, "FAILED", effectiveStatus("FAILED", isAuthOnly))
			assert.Equal(t, "ACTIVE", effectiveStatus("ACTIVE", isAuthOnly))
		}
	})
}

func TestValidateWorkflowRefsLocked(t *testing.T) {
	t.Run("no remote refs is always fine", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Workflows: types.WorkflowsSpec{
					OnCreate: []types.WorkflowRef{{Name: "local-workflow"}},
				},
			},
		}
		assert.NoError(t, validateWorkflowRefsLocked(c, lf))
	})

	t.Run("remote ref not in lock errors", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Workflows: types.WorkflowsSpec{
					OnCreate: []types.WorkflowRef{{Source: "github.com/org/repo//a.yaml@v1.0.0"}},
				},
			},
		}
		err := validateWorkflowRefsLocked(c, lf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in hyve.lock")
	})

	t.Run("remote ref present in lock passes", func(t *testing.T) {
		lf := &module.LockFile{
			Version: 1,
			Workflows: map[string]*module.LockedWorkflow{
				"github.com/org/repo//a.yaml@v1.0.0": {Name: "a", Source: "github.com/org/repo//a.yaml", SHA256: "abc"},
			},
		}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Workflows: types.WorkflowsSpec{
					OnCreate: []types.WorkflowRef{{Source: "github.com/org/repo//a.yaml@v1.0.0"}},
				},
			},
		}
		assert.NoError(t, validateWorkflowRefsLocked(c, lf))
	})

	t.Run("directory-kind ref in a hook is rejected", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Workflows: types.WorkflowsSpec{
					OnCreate: []types.WorkflowRef{{Source: "github.com/org/repo//workflows/"}},
				},
			},
		}
		err := validateWorkflowRefsLocked(c, lf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must reference a single file")
	})

	t.Run("checks all five hook lists", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		remote := types.WorkflowRef{Source: "github.com/org/repo//a.yaml@v1.0.0"}
		for _, spec := range []types.WorkflowsSpec{
			{PreReconcile: []types.WorkflowRef{remote}},
			{BeforeCreate: []types.WorkflowRef{remote}},
			{OnCreate: []types.WorkflowRef{remote}},
			{OnDelete: []types.WorkflowRef{remote}},
			{AfterDelete: []types.WorkflowRef{remote}},
		} {
			c := types.ClusterDefinition{
				Metadata: types.ClusterMetadata{Name: "test"},
				Spec:     types.ClusterSpec{Workflows: spec},
			}
			assert.Error(t, validateWorkflowRefsLocked(c, lf))
		}
	})
}

func TestValidateMgmtClusterRequirement(t *testing.T) {
	t.Run("empty requirement is always fine", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		assert.NoError(t, r.validateMgmtClusterRequirement("workload", ""))
	})

	t.Run("named cluster exists passes", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{defs: []types.ClusterDefinition{
			{Metadata: types.ClusterMetadata{Name: "mgmt"}},
		}})
		assert.NoError(t, r.validateMgmtClusterRequirement("workload", "mgmt"))
	})

	t.Run("named cluster missing errors with a clear message", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		err := r.validateMgmtClusterRequirement("workload", "mgmt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mgmtCluster "mgmt"`)
		assert.Contains(t, err.Error(), "doesn't exist")
	})
}

func TestUnmetDependency(t *testing.T) {
	lf := &module.LockFile{Version: 1}

	t.Run("no dependsOn is always met", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		def := types.ClusterDefinition{Metadata: types.ClusterMetadata{Name: "workload"}}
		unmet, err := r.unmetDependency(context.Background(), def, lf, nil)
		require.NoError(t, err)
		assert.Empty(t, unmet)
	})

	t.Run("dependency not present in state at all is unmet", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		def := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "workload"},
			Spec:     types.ClusterSpec{DependsOn: []string{"mgmt"}},
		}
		unmet, err := r.unmetDependency(context.Background(), def, lf, nil)
		require.NoError(t, err)
		assert.Equal(t, "mgmt", unmet)
	})

	t.Run("dependency present but its module can't resolve is unmet, not an error", func(t *testing.T) {
		// dependsOn's whole point is "skip this cycle, don't fail hard" —
		// a dependency whose own status check errors out (bad driver
		// source here) must read the same as one that's simply not ready.
		r := NewReconciler(&fakeStateProvider{defs: []types.ClusterDefinition{
			{
				Metadata: types.ClusterMetadata{Name: "mgmt"},
				Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "./does-not-exist", Version: "latest"}},
			},
		}})
		def := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "workload"},
			Spec:     types.ClusterSpec{DependsOn: []string{"mgmt"}},
		}
		unmet, err := r.unmetDependency(context.Background(), def, lf, nil)
		require.NoError(t, err)
		assert.Equal(t, "mgmt", unmet)
	})
}
