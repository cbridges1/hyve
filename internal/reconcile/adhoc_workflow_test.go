package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// TestRunAdHocWorkflow_SetVarsSatisfyDeclaredInputs is a regression test for
// a bug found live: `hyve workflow run <name> --cluster <cluster> --set
// KEY=VALUE` in cluster mode (RunAdHocWorkflow's own call path) only ever
// injected --set overrides as HYVE_PARAM_<KEY> (via cluster.Spec.Params ->
// buildModuleEnv, matching how a real template param flows into a script),
// never under their own bare name — but workflow.Executor.validateInputs
// checks a declared spec.inputs entry against its bare name. A workflow like
// register-with-rancher.yaml, whose inputs are deliberately named to match
// the template params its steps read via $HYVE_PARAM_*, could never satisfy
// validateInputs through --set at all: "requires ... RANCHER_SERVER_URL"
// even with `--set RANCHER_SERVER_URL=...` supplied. Local mode's own
// `hyve workflow run` never hit this — it calls executor.InjectVars(setVars)
// directly with bare names.
func TestRunAdHocWorkflow_SetVarsSatisfyDeclaredInputs(t *testing.T) {
	repoRoot := t.TempDir()
	moduleDir := filepath.Join(repoRoot, "modules", "test-driver")
	require.NoError(t, os.MkdirAll(moduleDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "module.yaml"), []byte(`apiVersion: v1
kind: Module
metadata:
  name: test-driver
  version: 0.1.0
  type: authOnly
`), 0644))

	// fakeStateProvider.WorkflowSource() returns FileSource{Dir:
	// f.LocalPath()} directly (no "workflows" subdir join, unlike the real
	// NewManager) — so the fixture lives at repoRoot's root.
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "echo-input.yaml"), []byte(`apiVersion: hyve.io/v1alpha1
kind: Workflow
metadata:
  name: echo-input
spec:
  inputs:
    - name: RANCHER_SERVER_URL
  jobs:
    - name: job1
      steps:
        - name: step1
          script: |
            echo "SEEN_VALUE=$HYVE_PARAM_RANCHER_SERVER_URL"
`), 0644))

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Driver: types.DriverRef{Source: "./modules/test-driver", Version: "local"}},
	}
	lf := &module.LockFile{Version: 1}

	r := NewReconciler(&fakeStateProvider{localPath: repoRoot})
	output, err := r.RunAdHocWorkflow(context.Background(), cluster, types.WorkflowRef{Name: "echo-input"},
		map[string]string{"RANCHER_SERVER_URL": "https://rancher.example.test"}, lf, nil)

	require.NoError(t, err, "output so far:\n%s", output)
	assert.Contains(t, output, "SEEN_VALUE=https://rancher.example.test")
}
