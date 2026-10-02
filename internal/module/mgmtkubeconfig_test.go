package module

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMgmtModule writes a module whose status op echoes the
// HYVE_MGMT_KUBECONFIG it received, requiring mgmtCluster mgmt ("" for
// none).
func writeMgmtModule(t *testing.T, mgmt string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "apiVersion: hyve.io/v1alpha1\nkind: Module\nmetadata:\n  name: capi\n  type: driver\nspec: {}\n"
	if mgmt != "" {
		manifest = "apiVersion: hyve.io/v1alpha1\nkind: Module\nmetadata:\n  name: capi\n  type: driver\nspec:\n  requirements:\n    mgmtCluster: " + mgmt + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.yaml"), []byte(manifest), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.sh"), []byte("#!/bin/sh\necho \"HYVE_GOT_MGMT=${HYVE_MGMT_KUBECONFIG:-unset}\"\n"), 0o755))
	return dir
}

func TestExecute_InjectsMgmtKubeconfig(t *testing.T) {
	var asked string
	exec := &Executor{
		ModuleDir:   writeMgmtModule(t, "capi-mgmt"),
		ClusterName: "workload",
		MgmtKubeconfigLocator: func(_ context.Context, cluster string) (string, error) {
			asked = cluster
			return "/tmp/mgmt.yaml", nil
		},
	}
	res, err := exec.Execute(context.Background(), OperationStatus)
	require.NoError(t, err)
	assert.Equal(t, "capi-mgmt", asked)
	assert.Equal(t, "/tmp/mgmt.yaml", res.Outputs["HYVE_GOT_MGMT"])
	assert.Empty(t, exec.Env, "the injected variable is scoped to the one operation")
}

func TestExecute_NoMgmtRequirement_NothingInjected(t *testing.T) {
	exec := &Executor{
		ModuleDir:   writeMgmtModule(t, ""),
		ClusterName: "workload",
		MgmtKubeconfigLocator: func(context.Context, string) (string, error) {
			t.Fatal("locator must not be called without requirements.mgmtCluster")
			return "", nil
		},
	}
	res, err := exec.Execute(context.Background(), OperationStatus)
	require.NoError(t, err)
	assert.Equal(t, "unset", res.Outputs["HYVE_GOT_MGMT"])
}

func TestExecute_ExplicitMgmtKubeconfigWins(t *testing.T) {
	exec := &Executor{
		ModuleDir:   writeMgmtModule(t, "capi-mgmt"),
		ClusterName: "workload",
		Env:         []string{MgmtKubeconfigEnv + "=/explicit.yaml"},
		MgmtKubeconfigLocator: func(context.Context, string) (string, error) {
			t.Fatal("an explicit HYVE_MGMT_KUBECONFIG must not be overridden")
			return "", nil
		},
	}
	res, err := exec.Execute(context.Background(), OperationStatus)
	require.NoError(t, err)
	assert.Equal(t, "/explicit.yaml", res.Outputs["HYVE_GOT_MGMT"])
}

func TestExecute_MgmtLocatorErrorFailsTheOperation(t *testing.T) {
	exec := &Executor{
		ModuleDir:   writeMgmtModule(t, "capi-mgmt"),
		ClusterName: "workload",
		MgmtKubeconfigLocator: func(context.Context, string) (string, error) {
			return "", errors.New("not reachable")
		},
	}
	_, err := exec.Execute(context.Background(), OperationStatus)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `mgmtCluster "capi-mgmt"`)
	assert.Contains(t, err.Error(), "not reachable")
}

func TestDefaultMgmtKubeconfigLocator(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := DefaultMgmtKubeconfigLocator(context.Background(), "capi-mgmt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hyve cluster auth capi-mgmt")

	path, err := KubeconfigPathForCluster("capi-mgmt")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("apiVersion: v1\n"), 0o600))
	got, err := DefaultMgmtKubeconfigLocator(context.Background(), "capi-mgmt")
	require.NoError(t, err)
	assert.Equal(t, path, got)
}
