package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func configMap(name string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: default\ndata:\n  k: v\n"
}

func TestExpandResourceItems_DirectoryInNameOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app/10-second.yaml"), configMap("second"))
	writeFile(t, filepath.Join(root, "app/00-first.yaml"), configMap("first"))
	writeFile(t, filepath.Join(root, "app/values.yml"), "not: a manifest\n") // .yml is ignored, like workflow directories
	writeFile(t, filepath.Join(root, "app/README.md"), "# ignored\n")
	writeFile(t, filepath.Join(root, "single.yaml"), configMap("single"))

	items, held, err := expandResourceItems([]types.ResourceRef{
		{Name: "single", Source: "./single.yaml"},
		{Name: "app", Source: "./app/"},
	}, root, "")
	require.NoError(t, err)
	assert.Empty(t, held)

	var names []string
	for _, it := range items {
		names = append(names, it.ref.Name)
	}
	assert.Equal(t, []string{"single", "app/00-first", "app/10-second"}, names)
	assert.Nil(t, items[0].pre, "single files resolve in the apply loop, as before")
	require.NotNil(t, items[1].pre)
	assert.Contains(t, string(items[1].pre.Data), "name: first")
	assert.Equal(t, 1, items[2].origIdx, "children point back at their directory's spec.resources index")
}

// A directory that fails to resolve stops expansion (like the apply loop's
// first error) and holds its own and every later directory's children, so
// the orphan pass can't prune them over a transient failure.
func TestExpandResourceItems_FailureHoldsDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok/a.yaml"), configMap("a"))

	items, held, err := expandResourceItems([]types.ResourceRef{
		{Name: "ok", Source: "./ok/"},
		{Name: "missing", Source: "./missing/"},
		{Name: "later", Source: "./later/"},
	}, root, "")
	require.Error(t, err)
	assert.Equal(t, []string{"missing", "later"}, held)
	require.Len(t, items, 1)
	assert.Equal(t, "ok/a", items[0].ref.Name)

	applied := map[string]*types.AppliedResource{"ok/a": {}, "missing/x": {}, "later/y": {}, "gone": {}}
	assert.Equal(t, []string{"gone"}, findOrphanedResources(specResourceNames(nil, items), held, applied))
}

func TestAppliedKeysFor(t *testing.T) {
	applied := map[string]*types.AppliedResource{"app": {}, "app/b": {}, "app/a": {}, "apple": {}, "other": {}}
	assert.Equal(t, []string{"app", "app/a", "app/b"}, appliedKeysFor(applied, "app"), "a sibling named apple is not a child")
}

// fakeKubectl puts a kubectl on PATH that logs each invocation (plus the
// first metadata.name of any manifest on stdin) and reports no drift.
func fakeKubectl(t *testing.T) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(bin, "calls.log")
	script := `#!/bin/sh
case "$1" in
  apply) name=$(grep -m1 '  name:' | awk '{print $2}'); echo "apply $name" >> "` + logPath + `" ;;
  diff)  cat >/dev/null; exit 0 ;;
  delete) echo "delete $2 $3" >> "` + logPath + `" ;;
esac
exit 0
`
	writeFile(t, filepath.Join(bin, "kubectl"), script)
	require.NoError(t, os.Chmod(filepath.Join(bin, "kubectl"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	_ = os.Remove(logPath)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// End to end: a directory source applies each file in order and tracks it
// separately; removing a file prunes just that file (strictResourceDelete);
// an unresolvable directory never prunes; delete:true removes every child.
func TestReconcileResources_DirectorySource(t *testing.T) {
	logPath := fakeKubectl(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "resource-files/app/00-first.yaml"), configMap("first"))
	writeFile(t, filepath.Join(root, "resource-files/app/10-second.yaml"), configMap("second"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "clusters"), 0o755))

	r := NewReconciler(state.NewManagerFromPath(filepath.Join(root, "clusters")))
	cluster := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "c1"},
		Spec: types.ClusterSpec{Resources: []types.ResourceRef{
			{Name: "app", Source: "./resource-files/app/"},
		}},
	}
	ctx := context.Background()

	require.NoError(t, r.reconcileResources(ctx, cluster, nil, nil, true, false))
	assert.Equal(t, []string{"apply first", "apply second"}, readCalls(t, logPath), "files apply in name order")
	assert.Contains(t, cluster.Spec.AppliedResources, "app/00-first")
	assert.Contains(t, cluster.Spec.AppliedResources, "app/10-second")

	// Unchanged files are left alone (config hash matches, no live diff).
	require.NoError(t, r.reconcileResources(ctx, cluster, nil, nil, true, false))
	assert.Empty(t, readCalls(t, logPath))

	// Removing a file prunes only that file's objects.
	require.NoError(t, os.Remove(filepath.Join(root, "resource-files/app/10-second.yaml")))
	require.NoError(t, r.reconcileResources(ctx, cluster, nil, nil, true, false))
	assert.Equal(t, []string{"delete ConfigMap second"}, readCalls(t, logPath))
	assert.NotContains(t, cluster.Spec.AppliedResources, "app/10-second")

	// If the directory can't be resolved, nothing is pruned.
	require.NoError(t, os.Rename(filepath.Join(root, "resource-files"), filepath.Join(root, "moved")))
	require.Error(t, r.reconcileResources(ctx, cluster, nil, nil, true, false))
	assert.Empty(t, readCalls(t, logPath))
	assert.Contains(t, cluster.Spec.AppliedResources, "app/00-first")

	// delete:true removes every child, without the directory resolving.
	cluster.Spec.Resources[0].Delete = true
	require.NoError(t, r.reconcileResources(ctx, cluster, nil, nil, true, false))
	assert.Equal(t, []string{"delete ConfigMap first"}, readCalls(t, logPath))
	assert.Empty(t, cluster.Spec.AppliedResources)
	assert.Empty(t, cluster.Spec.Resources, "the delete:true entry is removed once done")
}

// A remote directory source has no hyve.lock entry by design, so the local
// mode "is every remote resource locked?" pre-flight must let it through —
// while still requiring a lock entry for a remote single file.
func TestValidateResourceRefsLocked_DirectoryExempt(t *testing.T) {
	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "c1"},
		Spec: types.ClusterSpec{Resources: []types.ResourceRef{
			{Name: "dir", Source: "github.com/org/repo//manifests/@main"},
		}},
	}
	assert.NoError(t, validateResourceRefsLocked(cluster, &module.LockFile{}))

	cluster.Spec.Resources = append(cluster.Spec.Resources, types.ResourceRef{Name: "file", Source: "github.com/org/repo//manifests/a.yaml@main"})
	assert.ErrorContains(t, validateResourceRefsLocked(cluster, &module.LockFile{}), "not in hyve.lock")
}
