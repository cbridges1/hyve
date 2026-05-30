package providerconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type civoCLIConfig struct {
	APIKeys map[string]string `json:"apikeys"`
	Meta    struct {
		CurrentAPIKey string `json:"current_apikey"`
	} `json:"meta"`
}

// ReadCivoCLIToken reads the active Civo API token from ~/.civo.json,
// the credential file written by the Civo CLI.
// Returns an empty string if the file is absent or malformed.
func ReadCivoCLIToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".civo.json"))
	if err != nil {
		return ""
	}
	var cfg civoCLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	key := cfg.Meta.CurrentAPIKey
	if key == "" {
		return ""
	}
	return cfg.APIKeys[key]
}

// CivoOrganization represents a named Civo organization/account
type CivoOrganization struct {
	Name    string   `yaml:"name"`
	OrgID   string   `yaml:"org_id"`
	Token   string   `yaml:"token,omitempty"`
	Regions []string `yaml:"regions,omitempty"`
}

// CivoConfig represents Civo-specific configuration
type CivoConfig struct {
	Organizations []CivoOrganization `yaml:"organizations,omitempty"`
}

// LoadCivoConfig reads provider-configs/civo.yaml and returns the full config.
func (m *Manager) LoadCivoConfig() (*CivoConfig, error) {
	data, err := os.ReadFile(m.getConfigPath("civo"))
	if err != nil {
		if os.IsNotExist(err) {
			return &CivoConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read Civo config: %w", err)
	}
	var config CivoConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse Civo config: %w", err)
	}
	return &config, nil
}

// SaveCivoConfig writes the full Civo config to provider-configs/civo.yaml.
func (m *Manager) SaveCivoConfig(config *CivoConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Civo config: %w", err)
	}
	if err := os.WriteFile(m.getConfigPath("civo"), data, 0644); err != nil {
		return fmt.Errorf("failed to write Civo config: %w", err)
	}
	return nil
}

// LoadCivoOrganization reads the config for a single named Civo organization.
// Returns (nil, nil) when no organization with that name exists.
func (m *Manager) LoadCivoOrganization(name string) (*CivoOrganization, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return nil, err
	}
	for i := range config.Organizations {
		if config.Organizations[i].Name == name {
			return &config.Organizations[i], nil
		}
	}
	return nil, nil
}

// SaveCivoOrganization upserts an organization entry in provider-configs/civo.yaml.
func (m *Manager) SaveCivoOrganization(org *CivoOrganization) error {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return err
	}
	for i := range config.Organizations {
		if config.Organizations[i].Name == org.Name {
			config.Organizations[i] = *org
			return m.SaveCivoConfig(config)
		}
	}
	config.Organizations = append(config.Organizations, *org)
	return m.SaveCivoConfig(config)
}

// AddCivoOrganization adds or updates a named Civo organization.
func (m *Manager) AddCivoOrganization(name, orgID string) error {
	org, err := m.LoadCivoOrganization(name)
	if err != nil {
		return err
	}
	if org == nil {
		org = &CivoOrganization{Name: name}
	}
	org.OrgID = orgID
	return m.SaveCivoOrganization(org)
}

// RemoveCivoOrganization removes an organization by name.
func (m *Manager) RemoveCivoOrganization(name string) error {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return err
	}
	filtered := []CivoOrganization{}
	found := false
	for _, o := range config.Organizations {
		if o.Name != name {
			filtered = append(filtered, o)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("organization '%s' not found", name)
	}
	config.Organizations = filtered
	return m.SaveCivoConfig(config)
}

// GetCivoOrgID returns the org ID for a given name.
func (m *Manager) GetCivoOrgID(name string) (string, error) {
	org, err := m.LoadCivoOrganization(name)
	if err != nil {
		return "", err
	}
	if org == nil {
		return "", fmt.Errorf("Civo organization '%s' not found", name)
	}
	return org.OrgID, nil
}

// GetCivoOrganization returns the full organization config for a given name.
func (m *Manager) GetCivoOrganization(name string) (*CivoOrganization, error) {
	org, err := m.LoadCivoOrganization(name)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, fmt.Errorf("Civo organization '%s' not found", name)
	}
	return org, nil
}

// ListCivoOrganizations returns all configured Civo organizations.
func (m *Manager) ListCivoOrganizations() ([]CivoOrganization, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return nil, err
	}
	return config.Organizations, nil
}

// HasCivoOrganization checks if an organization with the given name exists.
func (m *Manager) HasCivoOrganization(name string) (bool, error) {
	org, err := m.LoadCivoOrganization(name)
	if err != nil {
		return false, err
	}
	return org != nil, nil
}

// GetCivoToken returns the API token for a Civo organization.
// The token field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetCivoToken(orgName string) (string, error) {
	org, err := m.LoadCivoOrganization(orgName)
	if err != nil {
		return "", err
	}
	if org == nil {
		return "", fmt.Errorf("Civo organization '%s' not found", orgName)
	}
	return resolveCredential(org.Token), nil
}
