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

	locked, collisions, resolveErrors, changed, err := Install(repoPath, []types.ResourceRef{
		{Name: "nginx", Source: canonicalSource},
	}, "")

	require.NoError(t, err)
	assert.False(t, changed, "an already-locked, cache-hit ref must be a no-op — no hyve.lock write")
	assert.Empty(t, locked)
	assert.Empty(t, collisions)
	assert.Empty(t, resolveErrors)
}

func TestInstall_DetectsNameCollision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := t.TempDir()

	sourceA := "github.com/org/repo-a//nginx.yaml"
	sourceB := "github.com/org/repo-b//nginx.yaml"
	preLockAndCache(t, repoPath, sourceA, "nginx", []byte("kind: Deployment\nfrom: a\n"))
	preLockAndCache(t, repoPath, sourceB, "nginx", []byte("kind: Deployment\nfrom: b\n"))

	_, collisions, resolveErrors, _, err := Install(repoPath, []types.ResourceRef{
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

	locked, collisions, resolveErrors, changed, err := Install(repoPath, []types.ResourceRef{
		{Name: "local", Source: "./x.yaml"},
		{Name: "by-name"}, // Source unset — not remote either
	}, "")

	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, locked)
	assert.Empty(t, collisions)
	assert.Empty(t, resolveErrors)
}
