package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

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
