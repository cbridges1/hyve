package resourceref

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/workflowref"
)

func writeFixtureFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	return full
}

func TestResolveLocal_RelativePath(t *testing.T) {
	repoRoot := t.TempDir()
	writeFixtureFile(t, repoRoot, "resource-files/nginx.yaml", "kind: Deployment\n")

	res, err := Resolve("./resource-files/nginx.yaml", repoRoot)
	require.NoError(t, err)
	assert.Equal(t, "./resource-files/nginx.yaml", res.CanonicalSource)
	assert.Equal(t, []byte("kind: Deployment\n"), res.Data)
	assert.NotEmpty(t, res.SHA256)
	assert.Equal(t, filepath.Join(repoRoot, "resource-files/nginx.yaml"), res.Resolved)
}

func TestResolveLocal_AbsolutePath(t *testing.T) {
	repoRoot := t.TempDir()
	otherDir := t.TempDir()
	full := writeFixtureFile(t, otherDir, "nginx.yaml", "kind: Deployment\n")

	res, err := Resolve(full, repoRoot)
	require.NoError(t, err)
	assert.Equal(t, full, res.Resolved)
}

func TestResolveLocal_NotFound(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := Resolve("./missing.yaml", repoRoot)
	require.Error(t, err)
}

func TestExtractFileFromArchiveDir(t *testing.T) {
	archiveDir := t.TempDir()
	writeFixtureFile(t, archiveDir, "manifests/nginx.yaml", "kind: Deployment\n")

	ps := workflowref.ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "manifests/nginx.yaml"}
	res, err := extractFileFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "github.com/org/repo//manifests/nginx.yaml", res.CanonicalSource)
	assert.Equal(t, "https://raw.githubusercontent.com/org/repo/v1.0.0/manifests/nginx.yaml", res.Resolved)
	assert.NotEmpty(t, res.SHA256)
	assert.Equal(t, []byte("kind: Deployment\n"), res.Data)
}

func TestExtractFileFromArchiveDir_NotFound(t *testing.T) {
	archiveDir := t.TempDir()
	ps := workflowref.ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "missing.yaml"}
	_, err := extractFileFromArchiveDir(archiveDir, ps, "v1.0.0")
	require.Error(t, err)
}

func TestResolveRemote_RejectsDirectoryKind(t *testing.T) {
	// A directory-kind source is rejected by ParseSource+ClassifyPath before
	// any network call (module.ResolveRef/workflowref.FetchRepoArchive are
	// never reached), so this is safe to run without network access.
	_, err := resolveRemote("github.com/org/repo//manifests/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must name a single file")
}

func TestResolveRemote_RejectsRepoRootSource(t *testing.T) {
	// An omitted path classifies as PathKindDir too (repo root, shallow
	// listing) — also rejected before any network call.
	_, err := resolveRemote("github.com/org/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must name a single file")
}

func TestRawFileURL(t *testing.T) {
	assert.Equal(t,
		"https://raw.githubusercontent.com/org/repo/v1.0.0/foo/bar.yaml",
		rawFileURL("github.com", "org", "repo", "v1.0.0", "foo/bar.yaml"))
	assert.Contains(t, rawFileURL("gitlab.example.com", "org", "repo", "v1.0.0", "foo/bar.yaml"), "archive/v1.0.0.tar.gz#foo/bar.yaml")
}
