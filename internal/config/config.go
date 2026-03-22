package config

import (
	"hyve/internal/credentials"
)

// Manager handles configuration
type Manager struct{}

// NewManager creates a new config manager
func NewManager() *Manager {
	return &Manager{}
}

// GetCivoToken loads the Civo API token from the local database for the given org.
// Returns an empty string when no token is stored.
func (m *Manager) GetCivoToken(orgName string) string {
	credsMgr, err := credentials.NewManager()
	if err != nil {
		return ""
	}
	defer credsMgr.Close()

	token, err := credsMgr.GetCivoToken(orgName)
	if err != nil {
		return ""
	}
	return token
}
