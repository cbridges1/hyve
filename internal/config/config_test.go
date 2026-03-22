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

func TestGetCivoToken_UnknownOrg_ReturnsEmpty(t *testing.T) {
	manager := NewManager()
	token := manager.GetCivoToken("nonexistent-org-xyz123-test")
	assert.Equal(t, "", token)
}

func TestGetCivoToken_EmptyOrg_ReturnsEmpty(t *testing.T) {
	manager := NewManager()
	token := manager.GetCivoToken("")
	assert.Equal(t, "", token)
}
