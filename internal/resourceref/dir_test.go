package resourceref

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDirSource(t *testing.T) {
	for source, want := range map[string]bool{
		"./resource-files/hyve/":                                  true,
		"../elsewhere/":                                           true,
		"/abs/dir/":                                               true,
		"./resource-files/hyve/crds.yaml":                         false,
		"./resource-files/hyve":                                   false, // no trailing slash: a file, same rule as workflows
		"github.com/org/repo//resource-files/hyve/@main":          true,
		"github.com/org/repo//resource-files/hyve/":               true,
		"github.com/org/repo//resource-files/hyve/crds.yaml@main": false,
		"github.com/org/repo@main":                                true, // repo root
	} {
		assert.Equal(t, want, IsDirSource(source), source)
	}
}

func TestResolveDir_Local(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "res")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o755))
	for name, body := range map[string]string{"b.yaml": "kind: B\n", "a.yaml": "kind: A\n", "skip.yml": "x\n", "nested/n.yaml": "kind: N\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	entries, err := ResolveDir("./res/", root, "")
	require.NoError(t, err)
	require.Len(t, entries, 2, "shallow, *.yaml only")
	assert.Equal(t, "a", entries[0].Stem)
	assert.Equal(t, "./res/a.yaml", entries[0].Resolved.CanonicalSource)
	assert.Equal(t, "b", entries[1].Stem)

	_, err = ResolveDir("./missing/", root, "")
	assert.Error(t, err)
}

func TestResolve_DirectoryWithoutTrailingSlashExplains(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "res"), 0o755))
	_, err := Resolve("./res", root, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `end it with "/"`)
}
