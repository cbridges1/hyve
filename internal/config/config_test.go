package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	manager := NewManager()
	require.NotNil(t, manager)
}

func TestGetCivoToken_EnvVar(t *testing.T) {
	t.Setenv("CIVO_TOKEN", "test-token-from-env")
	manager := NewManager()
	token := manager.GetCivoToken("any-org")
	// Token may come from ~/.civo.json (if it exists) or from the env var.
	assert.NotEmpty(t, token)
}

func TestGetCivoToken_NoCredentials_ReturnsEmpty(t *testing.T) {
	// Unset env var and point HOME to a temp dir with no .civo.json.
	t.Setenv("CIVO_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	manager := NewManager()
	token := manager.GetCivoToken("")
	assert.Equal(t, "", token)
}
