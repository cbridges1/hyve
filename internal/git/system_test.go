package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBackend initialises a real git repository in a temp directory and
// returns a SystemBackend pointing at it.
func newTestBackend(t *testing.T) *SystemBackend {
	t.Helper()
	dir := t.TempDir()

	b := NewSystemBackend("", dir, "", "")

	ctx := context.Background()
	require.NoError(t, b.runGitCommand(ctx, "init"))
	require.NoError(t, b.runGitCommand(ctx, "config", "user.name", "Test"))
	require.NoError(t, b.runGitCommand(ctx, "config", "user.email", "test@test.com"))

	return b
}

// ── NewBackend ────────────────────────────────────────────────────────────────

func TestNewBackend_SystemGitAvailable(t *testing.T) {
	// Skips on systems without git installed.
	if err := checkSystemGit(); err != nil {
		t.Skip("system git not available:", err)
	}
	dir := t.TempDir()
	b, err := NewBackend("", dir, "", "")
	require.NoError(t, err)
	assert.NotNil(t, b)
}

func TestNewSystemBackend_Fields(t *testing.T) {
	b := NewSystemBackend("https://example.com/repo.git", "/tmp/repo", "user", "token")
	assert.Equal(t, "https://example.com/repo.git", b.repoURL)
	assert.Equal(t, "/tmp/repo", b.localPath)
	assert.Equal(t, "user", b.username)
	assert.Equal(t, "token", b.token)
}

// ── GetStateDir ───────────────────────────────────────────────────────────────

func TestGetStateDir(t *testing.T) {
	b := NewSystemBackend("", "/some/path", "", "")
	assert.Equal(t, filepath.Join("/some/path", "clusters"), b.GetStateDir())
}

// ── EnsureStateDir ────────────────────────────────────────────────────────────

func TestEnsureStateDir_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	b := NewSystemBackend("", dir, "", "")

	require.NoError(t, b.EnsureStateDir())
	_, err := os.Stat(filepath.Join(dir, "clusters"))
	require.NoError(t, err, "clusters directory should be created")
}

func TestEnsureStateDir_Idempotent(t *testing.T) {
	dir := t.TempDir()
	b := NewSystemBackend("", dir, "", "")

	require.NoError(t, b.EnsureStateDir())
	require.NoError(t, b.EnsureStateDir()) // second call must not error
}

// ── Clone ─────────────────────────────────────────────────────────────────────

func TestClone_AlreadyExists(t *testing.T) {
	b := newTestBackend(t)
	// Repository is already initialised; Clone should be a no-op.
	require.NoError(t, b.Clone(context.Background()))
}

// ── Pull ──────────────────────────────────────────────────────────────────────

func TestPull_NotInitialized(t *testing.T) {
	dir := t.TempDir()
	b := NewSystemBackend("", dir, "", "")
	err := b.Pull(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

// ── Commit ────────────────────────────────────────────────────────────────────

func TestCommit_NothingToCommit(t *testing.T) {
	b := newTestBackend(t)
	// No files staged — "nothing to commit" should be silently ignored.
	err := b.Commit(context.Background(), "empty commit")
	require.NoError(t, err)
}

func TestCommit_WithChanges(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	// Write a file then commit it.
	testFile := filepath.Join(b.localPath, "hello.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello"), 0644))

	require.NoError(t, b.Commit(ctx, "add hello.txt"))
}

// ── GetCurrentBranch ─────────────────────────────────────────────────────────

func TestGetCurrentBranch(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	// Create a commit so the branch reference exists.
	testFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	branch, err := b.GetCurrentBranch(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, branch)
}

// ── CreateBranch / SwitchBranch / DeleteBranch ────────────────────────────────

func TestBranchLifecycle(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	// Need at least one commit before branches can be created.
	testFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	// Create a new branch.
	require.NoError(t, b.CreateBranch(ctx, "feature"))

	// Switch to it.
	require.NoError(t, b.SwitchBranch(ctx, "feature"))

	current, err := b.GetCurrentBranch(ctx)
	require.NoError(t, err)
	assert.Equal(t, "feature", current)

	// Switch back to the default branch so we can delete "feature".
	defaultBranch, _ := b.runGitCommandOutput(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if defaultBranch == "" {
		// Fallback: pick whatever branch we were on before.
		defaultBranch = "main"
		// Some git versions default to "master".
		if err := b.SwitchBranch(ctx, defaultBranch); err != nil {
			defaultBranch = "master"
			_ = b.SwitchBranch(ctx, defaultBranch)
		}
	} else {
		defaultBranch = strings.TrimPrefix(defaultBranch, "origin/")
		require.NoError(t, b.SwitchBranch(ctx, defaultBranch))
	}

	// Delete the feature branch.
	require.NoError(t, b.DeleteBranch(ctx, "feature", false))
}

func TestDeleteBranch_CurrentBranchFails(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	testFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	current, err := b.GetCurrentBranch(ctx)
	require.NoError(t, err)

	err = b.DeleteBranch(ctx, current, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete current branch")
}

// ── HasUncommittedChanges ─────────────────────────────────────────────────────

func TestHasUncommittedChanges_Clean(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	testFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	dirty, err := b.HasUncommittedChanges(ctx)
	require.NoError(t, err)
	assert.False(t, dirty)
}

func TestHasUncommittedChanges_Dirty(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	// Write a file without committing it.
	testFile := filepath.Join(b.localPath, "dirty.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("unstaged"), 0644))

	dirty, err := b.HasUncommittedChanges(ctx)
	require.NoError(t, err)
	assert.True(t, dirty)
}

// ── GetStatusSummary ──────────────────────────────────────────────────────────

func TestGetStatusSummary_Clean(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	testFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	summary, err := b.GetStatusSummary(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Working tree clean", summary)
}

func TestGetStatusSummary_WithUntrackedFile(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	// Create a commit so HEAD exists.
	initFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(initFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	// Add an untracked file.
	require.NoError(t, os.WriteFile(filepath.Join(b.localPath, "new.txt"), []byte("new"), 0644))

	summary, err := b.GetStatusSummary(ctx)
	require.NoError(t, err)
	assert.Contains(t, summary, "untracked")
}

// ── ListBranches ──────────────────────────────────────────────────────────────

func TestListBranches(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	testFile := filepath.Join(b.localPath, "init.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("init"), 0644))
	require.NoError(t, b.Commit(ctx, "initial commit"))

	branches, err := b.ListBranches(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, branches)

	// At least one branch should be marked current.
	hasCurrent := false
	for _, br := range branches {
		if br.IsCurrent {
			hasCurrent = true
			break
		}
	}
	assert.True(t, hasCurrent, "at least one branch should be marked as current")
}
