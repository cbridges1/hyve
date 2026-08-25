package workflowref

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// preLockAndCache mirrors resourceref's own test helper of the same name —
// simulates a ref already installed in a prior run, so Resolve's cache-hint
// short-circuits with zero network access.
func preLockAndCache(t *testing.T, repoPath, canonicalSource, name string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	digest := fmt.Sprintf("%x", sum[:])
	require.NoError(t, StoreInCache(digest, data))

	lf, err := module.LoadLockFile(repoPath)
	require.NoError(t, err)
	lf.SetLockedWorkflow(canonicalSource, "", &module.LockedWorkflow{
		Name: name, Source: canonicalSource, Resolved: "https://example.invalid/" + canonicalSource, SHA256: digest,
	})
	require.NoError(t, module.SaveLockFile(repoPath, lf))
	return digest
}

func TestInstall_SkipsAlreadyLockedUnchangedRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := t.TempDir()

	canonicalSource := "github.com/org/repo//workflows/nginx.yaml"
	preLockAndCache(t, repoPath, canonicalSource, "nginx", []byte("spec:\n  jobs: []\n"))

	locked, collisions, resolveErrors, results, changed, err := Install(repoPath, []types.WorkflowRef{
		{Source: canonicalSource},
	}, "")

	require.NoError(t, err)
	assert.False(t, changed, "an already-locked, cache-hit ref must be a no-op — no hyve.lock write")
	assert.Empty(t, locked)
	assert.Empty(t, collisions)
	assert.Empty(t, resolveErrors)

	// results must still be populated on a pure cache-hit, unchanged pass —
	// this is what lets resolveWorkflowIfNeeded mirror current status on
	// every reconcile, not just when something changed.
	require.Len(t, results, 1)
	assert.Equal(t, "nginx", results[0].Name)
	assert.Equal(t, canonicalSource, results[0].CanonicalSource)
	assert.NotEmpty(t, results[0].SHA256)
	assert.NoError(t, results[0].Err)
}

func TestInstall_DetectsNameCollision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := t.TempDir()

	sourceA := "github.com/org/repo-a//workflows/nginx.yaml"
	sourceB := "github.com/org/repo-b//workflows/nginx.yaml"
	preLockAndCache(t, repoPath, sourceA, "nginx", []byte("spec:\n  jobs: []\nfrom: a\n"))
	preLockAndCache(t, repoPath, sourceB, "nginx", []byte("spec:\n  jobs: []\nfrom: b\n"))

	_, collisions, resolveErrors, _, _, err := Install(repoPath, []types.WorkflowRef{
		{Source: sourceA},
		{Source: sourceB},
	}, "")

	require.NoError(t, err)
	assert.Empty(t, resolveErrors)
	require.Len(t, collisions, 1)
	assert.Equal(t, "nginx", collisions[0].Name)
	assert.Equal(t, sourceA, collisions[0].FirstSource)
	assert.Equal(t, sourceB, collisions[0].CollidedSource)
}

// TestInstall_PopulatesRefResultOnError confirms a failed resolve still
// produces a RefResult (Err set) rather than being silently dropped — a
// directory-kind source with no matching hyve.lock entry is rejected before
// any network call (there's no way to distinguish "no ref override, expand
// the dir" from "install requires a single file" here except by fetching —
// so this uses an invalid source string instead, rejected by ParseSource
// itself, equally network-free).
func TestInstall_PopulatesRefResultOnError(t *testing.T) {
	repoPath := t.TempDir()

	_, _, resolveErrors, results, changed, err := Install(repoPath, []types.WorkflowRef{
		{Source: "not-a-valid-source"},
	}, "")

	require.NoError(t, err)
	assert.False(t, changed)
	require.Len(t, resolveErrors, 1)
	require.Len(t, results, 1)
	assert.Error(t, results[0].Err)
}
