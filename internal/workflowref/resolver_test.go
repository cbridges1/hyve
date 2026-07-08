package workflowref

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFixtureFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
}

func TestExtractDirFromArchiveDir_ShallowYamlOnly(t *testing.T) {
	archiveDir := t.TempDir()
	writeFixtureFile(t, archiveDir, "workflows/a.yaml", "metadata:\n  name: a\n")
	writeFixtureFile(t, archiveDir, "workflows/b.yaml", "metadata:\n  name: b\n")
	writeFixtureFile(t, archiveDir, "workflows/c.yml", "metadata:\n  name: c\n")         // excluded: .yml not .yaml
	writeFixtureFile(t, archiveDir, "workflows/nested/d.yaml", "metadata:\n  name: d\n") // excluded: non-recursive
	writeFixtureFile(t, archiveDir, "workflows/readme.md", "not a workflow")             // excluded: wrong ext

	ps := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "workflows/"}
	files, err := extractDirFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.NoError(t, err)
	require.Len(t, files, 2)

	assert.Equal(t, "a", files[0].Name)
	assert.Equal(t, "github.com/org/repo//workflows/a.yaml", files[0].CanonicalSource)
	assert.Equal(t, "b", files[1].Name)
	assert.Equal(t, "github.com/org/repo//workflows/b.yaml", files[1].CanonicalSource)
	for _, f := range files {
		assert.NotEmpty(t, f.SHA256)
		assert.Equal(t, "v1.0.0", f.ResolvedVersion)
	}
}

func TestExtractDirFromArchiveDir_EmptyDirIsError(t *testing.T) {
	archiveDir := t.TempDir()
	writeFixtureFile(t, archiveDir, "workflows/readme.md", "not a workflow")

	ps := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "workflows/"}
	_, err := extractDirFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.Error(t, err)
}

func TestExtractDirFromArchiveDir_RepoRoot(t *testing.T) {
	archiveDir := t.TempDir()
	writeFixtureFile(t, archiveDir, "root.yaml", "metadata:\n  name: root\n")

	ps := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: ""}
	files, err := extractDirFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "github.com/org/repo//root.yaml", files[0].CanonicalSource)
}

func TestExtractFileFromArchiveDir(t *testing.T) {
	archiveDir := t.TempDir()
	writeFixtureFile(t, archiveDir, "workflows/hello.yaml", "metadata:\n  name: hello\n")

	ps := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "workflows/hello.yaml"}
	f, err := extractFileFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "hello", f.Name)
	assert.Equal(t, "github.com/org/repo//workflows/hello.yaml", f.CanonicalSource)
	assert.NotEmpty(t, f.SHA256)
	assert.Equal(t, "https://raw.githubusercontent.com/org/repo/v1.0.0/workflows/hello.yaml", f.Resolved)
}

func TestExtractFileFromArchiveDir_NotFound(t *testing.T) {
	archiveDir := t.TempDir()
	ps := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "missing.yaml"}
	_, err := extractFileFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.Error(t, err)
}

func TestExtractWorkflowName(t *testing.T) {
	t.Run("uses metadata.name", func(t *testing.T) {
		got := extractWorkflowName([]byte("metadata:\n  name: my-workflow\n"), "file.yaml")
		assert.Equal(t, "my-workflow", got)
	})

	t.Run("falls back to filename stem when name absent", func(t *testing.T) {
		got := extractWorkflowName([]byte("spec:\n  jobs: []\n"), "deploy-base.yaml")
		assert.Equal(t, "deploy-base", got)
	})

	t.Run("falls back to filename stem on invalid yaml", func(t *testing.T) {
		got := extractWorkflowName([]byte("not: valid: yaml: :::"), "setup-monitoring.yml")
		assert.Equal(t, "setup-monitoring", got)
	})
}

func TestRawFileURL(t *testing.T) {
	assert.Equal(t,
		"https://raw.githubusercontent.com/org/repo/v1.0.0/foo/bar.yaml",
		rawFileURL("github.com", "org", "repo", "v1.0.0", "foo/bar.yaml"))

	// Non-github hosts fall back to an archive-URL-based display string.
	assert.Contains(t, rawFileURL("gitlab.example.com", "org", "repo", "v1.0.0", "foo/bar.yaml"), "archive/v1.0.0.tar.gz#foo/bar.yaml")
}
