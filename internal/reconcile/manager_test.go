package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/resource"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// fakeStateProvider is a minimal in-memory StateProvider for tests that
// only need LoadClusterDefinitions to return a fixed set — everything else
// panics if called, so a test using it fails loudly if it exercises more
// of the interface than intended.
type fakeStateProvider struct {
	defs      []types.ClusterDefinition
	localPath string
}

func (f *fakeStateProvider) LocalPath() string { return f.localPath }
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
func (f *fakeStateProvider) ResourceSource() resource.Source {
	return resource.FileSource{Dir: f.LocalPath()}
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

// TestValidateResourceRefsLocked mirrors TestValidateWorkflowRefsLocked
// exactly, one tier below it — validateResourceRefsLocked has the same
// local/Name-is-fine, remote-must-be-locked, directory-kind-rejected shape.
func TestValidateResourceRefsLocked(t *testing.T) {
	t.Run("local and Name-only refs are always fine", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Resources: []types.ResourceRef{
					{Name: "local-resource", Source: "./x.yaml"},
					{Name: "by-name"},
				},
			},
		}
		assert.NoError(t, validateResourceRefsLocked(c, lf))
	})

	t.Run("remote ref not in lock errors", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Resources: []types.ResourceRef{{Name: "a", Source: "github.com/org/repo//a.yaml@v1.0.0"}},
			},
		}
		err := validateResourceRefsLocked(c, lf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in hyve.lock")
	})

	t.Run("remote ref present in lock passes", func(t *testing.T) {
		lf := &module.LockFile{
			Version: 1,
			Resources: map[string]*module.LockedResource{
				"github.com/org/repo//a.yaml@v1.0.0": {Name: "a", Source: "github.com/org/repo//a.yaml", SHA256: "abc"},
			},
		}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Resources: []types.ResourceRef{{Name: "a", Source: "github.com/org/repo//a.yaml@v1.0.0"}},
			},
		}
		assert.NoError(t, validateResourceRefsLocked(c, lf))
	})

	t.Run("directory-kind ref is rejected", func(t *testing.T) {
		lf := &module.LockFile{Version: 1}
		c := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: "test"},
			Spec: types.ClusterSpec{
				Resources: []types.ResourceRef{{Name: "a", Source: "github.com/org/repo//manifests/"}},
			},
		}
		err := validateResourceRefsLocked(c, lf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must name a single file")
	})
}

// TestReconcileCluster_ToolRequirements_OnlyEnforcedInlineNotViaJobDispatch
// is the regression test for the "civo not found in PATH" bug: modules
// declaring spec.requirements.tools are meant to be checked against
// whatever process actually runs their scripts. In local/CLI mode that's
// this process, so the check is real and correct. In cluster mode
// (r.ModuleRunner != nil) the module instead runs inside a separate Job on
// its own runner.image — this process's own PATH says nothing about what's
// in that image, so the pre-flight check must be skipped there (a missing
// tool still surfaces, just naturally, as the Job's script failing with
// "command not found").
func TestReconcileCluster_ToolRequirements_OnlyEnforcedInlineNotViaJobDispatch(t *testing.T) {
	repoRoot := t.TempDir()
	moduleDir := filepath.Join(repoRoot, "modules", "test-driver")
	require.NoError(t, os.MkdirAll(moduleDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "module.yaml"), []byte(`apiVersion: v1
kind: Module
metadata:
  name: test-driver
  version: 0.1.0
  type: authOnly
spec:
  requirements:
    tools:
      - name: definitely-not-a-real-tool-xyz-123
`), 0644))

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "./modules/test-driver", Version: "local"}},
	}
	lf := &module.LockFile{Version: 1}

	t.Run("local mode: missing tool is a hard error", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{localPath: repoRoot})
		err := r.reconcileCluster(context.Background(), cluster, lf, false, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "definitely-not-a-real-tool-xyz-123")
	})

	t.Run("cluster mode: tool check is skipped, module dispatches to its own runner.image instead", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{localPath: repoRoot})
		r.ModuleRunner = &module.JobRunner{} // never actually invoked — this authOnly module has no auth/create/status/delete files to dispatch
		err := r.reconcileCluster(context.Background(), cluster, lf, false, nil, nil)
		assert.NoError(t, err)
	})
}

// TestModuleImage covers the resolution chain: a ClusterDefinition's own
// spec.runner.image (set directly, or inherited from a Template at
// creation time) wins over HyveConfig's cluster-wide DefaultModuleImage —
// the module's own module.yaml is never consulted at all (see moduleImage's
// doc comment for why: a module recommends, but doesn't choose).
func TestModuleImage(t *testing.T) {
	t.Run("cluster's own runner.image wins", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		r.DefaultModuleImage = "cluster-wide-default:latest"
		cluster := types.ClusterDefinition{Spec: types.ClusterSpec{Runner: types.RunnerSpec{Image: "per-cluster:v2"}}}
		assert.Equal(t, "per-cluster:v2", r.moduleImage(cluster))
	})

	t.Run("falls back to DefaultModuleImage when unset", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		r.DefaultModuleImage = "cluster-wide-default:latest"
		assert.Equal(t, "cluster-wide-default:latest", r.moduleImage(types.ClusterDefinition{}))
	})

	t.Run("empty when neither is set", func(t *testing.T) {
		r := NewReconciler(&fakeStateProvider{})
		assert.Empty(t, r.moduleImage(types.ClusterDefinition{}))
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

func TestParamsHash_DeterministicRegardlessOfMapIterationOrder(t *testing.T) {
	a := map[string]string{"node_count": "3", "node_size": "g4s.kube.medium"}
	b := map[string]string{"node_size": "g4s.kube.medium", "node_count": "3"}
	assert.Equal(t, ParamsHash(a), ParamsHash(b))
	assert.NotEmpty(t, ParamsHash(a))
}

func TestParamsHash_DifferentParamsDifferentHash(t *testing.T) {
	a := map[string]string{"node_count": "3"}
	b := map[string]string{"node_count": "5"}
	assert.NotEqual(t, ParamsHash(a), ParamsHash(b))
}

func TestParamsHash_EmptyIsEmptyString(t *testing.T) {
	assert.Equal(t, "", ParamsHash(nil))
	assert.Equal(t, "", ParamsHash(map[string]string{}))
}

// TestParamsHash_MatchesDriftDetection guards against ParamsHash and
// paramsChanged's own comparison ever drifting apart now that adopt (via
// cmd/shared.ParamsHash) seeds HYVE_LAST_PARAMS_HASH from outside this
// package — a cluster adopted with this hash must read back as "no drift"
// on the very next paramsChanged check.
func TestParamsHash_MatchesDriftDetection(t *testing.T) {
	r := NewReconciler(&fakeStateProvider{})
	params := map[string]string{"node_count": "3", "node_size": "g4s.kube.medium"}
	cluster := types.ClusterDefinition{
		Spec: types.ClusterSpec{
			Params:        params,
			DriverOutputs: map[string]string{"HYVE_LAST_PARAMS_HASH": ParamsHash(params)},
		},
	}
	assert.False(t, r.paramsChanged(cluster))
}
