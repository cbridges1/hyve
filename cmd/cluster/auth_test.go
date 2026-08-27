package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/kubeconfig"
)

const fakePerClusterKubeconfig = `apiVersion: v1
kind: Config
clusters:
  - name: original-name
    cluster:
      server: https://del-clust.example.com
users:
  - name: original-name
    user:
      token: fake-token
contexts:
  - name: original-name
    context:
      cluster: original-name
      user: original-name
current-context: original-name
`

// TestMergeAuthResultIntoDefaultKubeconfig_EmptyPathIsNoOp guards the
// contract that a module with no KUBECONFIG export (Outputs["KUBECONFIG"]
// == "") is a no-op, not an error — see ClusterAuth's own Exports field.
func TestMergeAuthResultIntoDefaultKubeconfig_EmptyPathIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mergeAuthResultIntoDefaultKubeconfig("del-clust", "")

	_, err := os.Stat(filepath.Join(home, ".kube", "config"))
	assert.True(t, os.IsNotExist(err), "default kubeconfig should never be created for an empty per-cluster path")
}

// TestMergeAuthResultIntoDefaultKubeconfig_MergesIntoDefault is the actual
// regression test for the bug this function fixes: executeAuth always
// writes to a per-cluster path (module.KubeconfigPathForCluster), which
// cmd/cluster/auth.go used to just discard — leaving ~/.kube/config
// completely untouched even though auth "succeeded".
func TestMergeAuthResultIntoDefaultKubeconfig_MergesIntoDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	perClusterPath := filepath.Join(home, ".hyve", "kubeconfigs", "del-clust.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(perClusterPath), 0700))
	require.NoError(t, os.WriteFile(perClusterPath, []byte(fakePerClusterKubeconfig), 0600))

	mergeAuthResultIntoDefaultKubeconfig("del-clust", perClusterPath)

	defaultPath := filepath.Join(home, ".kube", "config")
	names, err := kubeconfig.ContextNames(readFile(t, defaultPath))
	require.NoError(t, err)
	assert.Equal(t, []string{"del-clust"}, names, "entry must be renamed to the cluster name, not the module's own arbitrary name")
}

// TestMergeAuthResultIntoDefaultKubeconfig_IdempotentOnRepeatedAuth confirms
// re-running `hyve cluster auth <name>` (e.g. to refresh an expiring
// credential) doesn't accumulate duplicate entries for the same cluster.
func TestMergeAuthResultIntoDefaultKubeconfig_IdempotentOnRepeatedAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	perClusterPath := filepath.Join(home, ".hyve", "kubeconfigs", "del-clust.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(perClusterPath), 0700))
	require.NoError(t, os.WriteFile(perClusterPath, []byte(fakePerClusterKubeconfig), 0600))

	mergeAuthResultIntoDefaultKubeconfig("del-clust", perClusterPath)
	mergeAuthResultIntoDefaultKubeconfig("del-clust", perClusterPath)

	defaultPath := filepath.Join(home, ".kube", "config")
	names, err := kubeconfig.ContextNames(readFile(t, defaultPath))
	require.NoError(t, err)
	assert.Equal(t, []string{"del-clust"}, names)
}

// TestMergeAuthResultIntoDefaultKubeconfig_PreservesOtherClusters confirms
// merging one cluster's auth result doesn't clobber another cluster's
// existing entry — the whole point of naming entries after the cluster.
func TestMergeAuthResultIntoDefaultKubeconfig_PreservesOtherClusters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, name := range []string{"cluster-a", "cluster-b"} {
		perClusterPath := filepath.Join(home, ".hyve", "kubeconfigs", name+".yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(perClusterPath), 0700))
		require.NoError(t, os.WriteFile(perClusterPath, []byte(fakePerClusterKubeconfig), 0600))
		mergeAuthResultIntoDefaultKubeconfig(name, perClusterPath)
	}

	defaultPath := filepath.Join(home, ".kube", "config")
	names, err := kubeconfig.ContextNames(readFile(t, defaultPath))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"cluster-a", "cluster-b"}, names)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
