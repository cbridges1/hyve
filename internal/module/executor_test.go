package module

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractBetweenMarkers_WithTrailingNewlineInFile(t *testing.T) {
	// The wrapper's `cat "$KUBECONFIG"` output already ends in "\n" here
	// (a real kubeconfig file almost always does), plus the wrapper's own
	// unconditional blank echo before the end marker.
	stdout := "noise before\n___HYVE_KUBECONFIG_BEGIN___\nline1\nline2\n\n___HYVE_KUBECONFIG_END___\nnoise after\n"
	content, ok := extractBetweenMarkers(stdout, kubeconfigBeginMarker, kubeconfigEndMarker)
	require.True(t, ok)
	assert.Equal(t, "line1\nline2\n", content)
}

func TestExtractBetweenMarkers_NoTrailingNewlineInFile(t *testing.T) {
	// cat's output does NOT end in "\n" here — only the wrapper's own
	// unconditional blank echo (exactly one "\n") separates it from the
	// end marker.
	stdout := "___HYVE_KUBECONFIG_BEGIN___\nline1\n___HYVE_KUBECONFIG_END___\n"
	content, ok := extractBetweenMarkers(stdout, kubeconfigBeginMarker, kubeconfigEndMarker)
	require.True(t, ok)
	assert.Equal(t, "line1", content)
}

func TestExtractBetweenMarkers_MissingMarkersReturnsFalse(t *testing.T) {
	_, ok := extractBetweenMarkers("no markers here\n", kubeconfigBeginMarker, kubeconfigEndMarker)
	assert.False(t, ok)
}

func TestExtractBetweenMarkers_MissingEndMarkerReturnsFalse(t *testing.T) {
	stdout := "___HYVE_KUBECONFIG_BEGIN___\nline1\n"
	_, ok := extractBetweenMarkers(stdout, kubeconfigBeginMarker, kubeconfigEndMarker)
	assert.False(t, ok)
}

func TestKubeconfigPathForCluster_UniquePerCluster(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pathA, err := KubeconfigPathForCluster("cluster-a")
	require.NoError(t, err)
	pathB, err := KubeconfigPathForCluster("cluster-b")
	require.NoError(t, err)

	assert.NotEqual(t, pathA, pathB)
	assert.Equal(t, filepath.Join(home, ".hyve", "kubeconfigs"), filepath.Dir(pathA))
}

func TestKubeconfigPathForCluster_SanitizesUnsafeCharacters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := KubeconfigPathForCluster("../../etc/passwd")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".hyve", "kubeconfigs"), filepath.Dir(path))
}

// TestExecuteAuth_WritesPerClusterKubeconfig_NotProcessEnv is the direct
// regression test for the MaxConcurrentReconciles fix: two clusters'
// concurrent auth calls must never be able to clobber each other via
// process-wide KUBECONFIG. Confirms the auth script (inline mode — no
// Runner set) sees a per-cluster KUBECONFIG value (so tools like civo
// --save write there), the returned OperationResult carries that same
// path, and the process environment is never mutated as a side effect.
func TestExecuteAuth_WritesPerClusterKubeconfig_NotProcessEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", "")

	moduleDir := t.TempDir()
	authYAML := `apiVersion: v1
kind: ClusterAuth
metadata:
  name: test
spec:
  methods:
    - name: default
      auth:
        script: "echo fake-kubeconfig > \"$KUBECONFIG\""
      exports: KUBECONFIG
`
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "auth.yaml"), []byte(authYAML), 0644))

	exec := &Executor{ModuleDir: moduleDir, WorkDir: t.TempDir(), ClusterName: "my-cluster"}
	result, err := exec.Execute(context.Background(), OperationAuth)
	require.NoError(t, err)

	wantPath, err := KubeconfigPathForCluster("my-cluster")
	require.NoError(t, err)
	assert.Equal(t, wantPath, result.Outputs["KUBECONFIG"])
	assert.FileExists(t, wantPath)

	assert.Empty(t, os.Getenv("KUBECONFIG"), "auth must never mutate the process-wide KUBECONFIG env var")
}

// TestExecuteAuth_DifferentClustersGetIsolatedKubeconfigs proves two
// clusters' auth calls through the same process never collide on a single
// file — the property MaxConcurrentReconciles > 1 depends on.
func TestExecuteAuth_DifferentClustersGetIsolatedKubeconfigs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	moduleDir := t.TempDir()
	authYAML := `apiVersion: v1
kind: ClusterAuth
metadata:
  name: test
spec:
  methods:
    - name: default
      auth:
        script: "echo \"cluster=$HYVE_CLUSTER_NAME\" > \"$KUBECONFIG\""
      exports: KUBECONFIG
`
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "auth.yaml"), []byte(authYAML), 0644))

	execA := &Executor{ModuleDir: moduleDir, WorkDir: t.TempDir(), ClusterName: "cluster-a", Env: []string{"HYVE_CLUSTER_NAME=cluster-a"}}
	execB := &Executor{ModuleDir: moduleDir, WorkDir: t.TempDir(), ClusterName: "cluster-b", Env: []string{"HYVE_CLUSTER_NAME=cluster-b"}}

	resultA, err := execA.Execute(context.Background(), OperationAuth)
	require.NoError(t, err)
	resultB, err := execB.Execute(context.Background(), OperationAuth)
	require.NoError(t, err)

	assert.NotEqual(t, resultA.Outputs["KUBECONFIG"], resultB.Outputs["KUBECONFIG"])

	contentA, err := os.ReadFile(resultA.Outputs["KUBECONFIG"])
	require.NoError(t, err)
	contentB, err := os.ReadFile(resultB.Outputs["KUBECONFIG"])
	require.NoError(t, err)

	assert.Contains(t, string(contentA), "cluster=cluster-a")
	assert.Contains(t, string(contentB), "cluster=cluster-b")
}
