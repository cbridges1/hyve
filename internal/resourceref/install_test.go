package resourceref

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// preLockAndCache writes lf.Resources[LockKey(canonicalSource, "")] and its
// matching resource-cache entry directly — simulating a ref that's already
// been installed in a prior run, so Resolve's cache-hint short-circuits
// with zero network access (see resolve.go's resolveRemote doc comment).
// This is what lets Install/Resolve be tested here without a live git
// fetch, mirroring how workflowref's own tests avoid network access.
func preLockAndCache(t *testing.T, repoPath, canonicalSource, name string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	digest := fmt.Sprintf("%x", sum[:])
	require.NoError(t, StoreInCache(digest, data))

	lf, err := module.LoadLockFile(repoPath)
	require.NoError(t, err)
	lf.SetLockedResource(canonicalSource, "", &module.LockedResource{
		Name: name, Source: canonicalSource, Resolved: "https://example.invalid/" + canonicalSource, SHA256: digest,
	})
	require.NoError(t, module.SaveLockFile(repoPath, lf))
	return digest
}

func TestInstall_SkipsAlreadyLockedUnchangedRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := t.TempDir()

	canonicalSource := "github.com/org/repo//manifests/nginx.yaml"
	preLockAndCache(t, repoPath, canonicalSource, "nginx", []byte("kind: Deployment\n"))

	locked, collisions, resolveErrors, results, changed, err := Install(repoPath, []types.ResourceRef{
		{Name: "nginx", Source: canonicalSource},
	}, "")

	require.NoError(t, err)
	assert.False(t, changed, "an already-locked, cache-hit ref must be a no-op — no hyve.lock write")
	assert.Empty(t, locked)
	assert.Empty(t, collisions)
	assert.Empty(t, resolveErrors)

	// results must still be populated on a pure cache-hit, unchanged pass —
	// this is what lets a caller (e.g. resolveResourceIfNeeded) mirror
	// current status on every reconcile, not just when something changed.
	require.Len(t, results, 1)
	assert.Equal(t, "nginx", results[0].Name)
	assert.Equal(t, canonicalSource, results[0].CanonicalSource)
	assert.NotEmpty(t, results[0].SHA256)
	assert.NoError(t, results[0].Err)
}

func TestInstall_DetectsNameCollision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := t.TempDir()

	sourceA := "github.com/org/repo-a//nginx.yaml"
	sourceB := "github.com/org/repo-b//nginx.yaml"
	preLockAndCache(t, repoPath, sourceA, "nginx", []byte("kind: Deployment\nfrom: a\n"))
	preLockAndCache(t, repoPath, sourceB, "nginx", []byte("kind: Deployment\nfrom: b\n"))

	_, collisions, resolveErrors, _, _, err := Install(repoPath, []types.ResourceRef{
		{Name: "nginx", Source: sourceA},
		{Name: "nginx", Source: sourceB},
	}, "")

	require.NoError(t, err)
	assert.Empty(t, resolveErrors)
	require.Len(t, collisions, 1)
	assert.Equal(t, "nginx", collisions[0].Name)
	assert.Equal(t, sourceA, collisions[0].FirstSource)
	assert.Equal(t, sourceB, collisions[0].CollidedSource)
}

func TestInstall_IgnoresLocalRefs(t *testing.T) {
	repoPath := t.TempDir()

	locked, collisions, resolveErrors, results, changed, err := Install(repoPath, []types.ResourceRef{
		{Name: "local", Source: "./x.yaml"},
		{Name: "by-name"}, // Source unset — not remote either
	}, "")

	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, locked)
	assert.Empty(t, collisions)
	assert.Empty(t, resolveErrors)
	assert.Empty(t, results)
}

// TestInstall_PopulatesRefResultOnError confirms a failed resolve still
// produces a RefResult (Err set) rather than being silently dropped — a
// directory-kind source is rejected by ParseSource+ClassifyPath before any
// network call (see resolve_test.go's TestResolveRemote_RejectsDirectoryKind),
// so this is safe to run without network access.
func TestInstall_PopulatesRefResultOnError(t *testing.T) {
	repoPath := t.TempDir()

	_, _, resolveErrors, results, changed, err := Install(repoPath, []types.ResourceRef{
		{Name: "bad", Source: "github.com/org/repo//manifests/"},
	}, "")

	require.NoError(t, err)
	assert.False(t, changed)
	require.Len(t, resolveErrors, 1)
	require.Len(t, results, 1)
	assert.Equal(t, "bad", results[0].Name)
	assert.Error(t, results[0].Err)
}
