package module

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCRName(t *testing.T) {
	tests := []struct {
		source, version, want string
	}{
		{"github.com/cbridges1/hyve-civo-module", "main", "github-com-cbridges1-hyve-civo-module-main"},
		{"./modules/civo", "local", "modules-civo-local"},
		{"a", "b", "a-b"},
	}
	for _, tt := range tests {
		got := CRName(tt.source, tt.version)
		assert.Equal(t, tt.want, got)
	}
}

func TestCRName_Deterministic(t *testing.T) {
	a := CRName("github.com/org/repo", "v1.0.0")
	b := CRName("github.com/org/repo", "v1.0.0")
	assert.Equal(t, a, b)
}

func TestCRName_NeverEmpty(t *testing.T) {
	assert.NotEmpty(t, CRName("", ""))
}

func TestEnsureResolved_LocalSource(t *testing.T) {
	repoRoot := t.TempDir()
	source := writeLocalModule(t, repoRoot, "fake-driver", map[string]string{
		"module.yaml": "apiVersion: v1\nkind: Module\nmetadata:\n  name: fake-driver\n  version: 0.1.0\n",
		"create.sh":   "#!/bin/sh\necho ok\n",
	})

	locked, err := EnsureResolved(repoRoot, source, "local")
	require.NoError(t, err)
	require.NotNil(t, locked)

	lf, err := LoadLockFile(repoRoot)
	require.NoError(t, err)
	assert.NotNil(t, lf.GetLocked(source, "local"), "EnsureResolved must persist the lock")
}

func TestEnsureResolved_AlreadyLockedIsNoOp(t *testing.T) {
	repoRoot := t.TempDir()
	source := writeLocalModule(t, repoRoot, "fake-driver", map[string]string{
		"module.yaml": "apiVersion: v1\nkind: Module\nmetadata:\n  name: fake-driver\n  version: 0.1.0\n",
		"create.sh":   "#!/bin/sh\necho ok\n",
	})

	first, err := EnsureResolved(repoRoot, source, "local")
	require.NoError(t, err)

	second, err := EnsureResolved(repoRoot, source, "local")
	require.NoError(t, err)
	assert.Equal(t, first, second)
}
