package workflowref

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreAndReadCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	data := []byte("metadata:\n  name: hello\n")
	sum := sha256.Sum256(data)
	digest := fmt.Sprintf("%x", sum[:])

	assert.False(t, IsCached(digest))

	require.NoError(t, StoreInCache(digest, data))
	assert.True(t, IsCached(digest))

	got, err := ReadCached(digest)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestIsCached_MissingOrEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	assert.False(t, IsCached(""))
	assert.False(t, IsCached("deadbeef"))
}
