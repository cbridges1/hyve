package config

import (
	"os"

	"github.com/cbridges1/hyve/internal/providerconfig"
)

// Manager handles configuration
type Manager struct{}

// NewManager creates a new config manager
func NewManager() *Manager {
	return &Manager{}
}

// GetCivoToken returns the active Civo API token. For local use it reads
// ~/.civo.json (the Civo CLI credential file); the CIVO_TOKEN environment
// variable is used as a fallback (for CI/CD pipelines).
func (m *Manager) GetCivoToken(_ string) string {
	if token := providerconfig.ReadCivoCLIToken(); token != "" {
		return token
	}
	return os.Getenv("CIVO_TOKEN")
}
