package providerconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// CivoNetwork represents a named Civo network
type CivoNetwork struct {
	Name      string `yaml:"name"`
	NetworkID string `yaml:"network_id"`
}

// CivoOrganization represents a named Civo organization/account
type CivoOrganization struct {
	Name     string        `yaml:"name"`
	OrgID    string        `yaml:"org_id"`
	Token    string        `yaml:"token,omitempty"`
	Regions  []string      `yaml:"regions,omitempty"`
	Networks []CivoNetwork `yaml:"networks,omitempty"`
}

// CivoConfig represents Civo-specific configuration (aggregated view across all org files)
type CivoConfig struct {
	Organizations []CivoOrganization `yaml:"organizations,omitempty"`
}

// LoadCivoOrganization reads the config file for a single named Civo organization.
// Returns (nil, nil) when no file exists for that organization.
func (m *Manager) LoadCivoOrganization(name string) (*CivoOrganization, error) {
	path := m.getAccountConfigPath("civo", name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read Civo organization config: %w", err)
	}
	var org CivoOrganization
	if err := yaml.Unmarshal(data, &org); err != nil {
		return nil, fmt.Errorf("failed to parse Civo organization config: %w", err)
	}
	return &org, nil
}

// SaveCivoOrganization writes the config for a single Civo organization to its own file.
func (m *Manager) SaveCivoOrganization(org *CivoOrganization) error {
	if err := m.ensureProviderDir("civo"); err != nil {
		return err
	}
	data, err := yaml.Marshal(org)
	if err != nil {
		return fmt.Errorf("failed to marshal Civo organization config: %w", err)
	}
	path := m.getAccountConfigPath("civo", org.Name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write Civo organization config: %w", err)
	}
	return nil
}

// LoadCivoConfig reads all per-org files under provider-configs/civo/ and
// returns them as a single aggregated CivoConfig.
func (m *Manager) LoadCivoConfig() (*CivoConfig, error) {
	dir := m.getProviderDir("civo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &CivoConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read Civo config directory: %w", err)
	}

	var config CivoConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", e.Name(), err)
		}
		var org CivoOrganization
		if err := yaml.Unmarshal(data, &org); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", e.Name(), err)
		}
		config.Organizations = append(config.Organizations, org)
	}
	return &config, nil
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

// RemoveCivoOrganization removes an organization by deleting its config file.
func (m *Manager) RemoveCivoOrganization(name string) error {
	path := m.getAccountConfigPath("civo", name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("organization '%s' not found", name)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove Civo organization config: %w", err)
	}
	return nil
}

// GetCivoOrgID returns the org ID for a given name
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

// GetCivoOrganization returns the full organization config for a given name
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

// ListCivoOrganizations returns all configured organizations
func (m *Manager) ListCivoOrganizations() ([]CivoOrganization, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return nil, err
	}
	return config.Organizations, nil
}

// HasCivoOrganization checks if an organization with the given name exists
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

// AddCivoNetwork adds a named network to an organization
func (m *Manager) AddCivoNetwork(orgName, networkName, networkID string) error {
	org, err := m.LoadCivoOrganization(orgName)
	if err != nil {
		return err
	}
	if org == nil {
		return fmt.Errorf("Civo organization '%s' not found", orgName)
	}
	for i, n := range org.Networks {
		if n.Name == networkName {
			org.Networks[i].NetworkID = networkID
			return m.SaveCivoOrganization(org)
		}
	}
	org.Networks = append(org.Networks, CivoNetwork{Name: networkName, NetworkID: networkID})
	return m.SaveCivoOrganization(org)
}

// RemoveCivoNetwork removes a network by name from an organization
func (m *Manager) RemoveCivoNetwork(orgName, networkName string) error {
	org, err := m.LoadCivoOrganization(orgName)
	if err != nil {
		return err
	}
	if org == nil {
		return fmt.Errorf("Civo organization '%s' not found", orgName)
	}
	filtered := []CivoNetwork{}
	found := false
	for _, n := range org.Networks {
		if n.Name != networkName {
			filtered = append(filtered, n)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("network '%s' not found in organization '%s'", networkName, orgName)
	}
	org.Networks = filtered
	return m.SaveCivoOrganization(org)
}

// GetCivoNetworkID returns the network ID for a given network name in an organization
func (m *Manager) GetCivoNetworkID(orgName, networkName string) (string, error) {
	org, err := m.LoadCivoOrganization(orgName)
	if err != nil {
		return "", err
	}
	if org == nil {
		return "", fmt.Errorf("Civo organization '%s' not found", orgName)
	}
	for _, n := range org.Networks {
		if n.Name == networkName {
			return n.NetworkID, nil
		}
	}
	return "", fmt.Errorf("network '%s' not found in organization '%s'", networkName, orgName)
}

// ListCivoNetworks returns all configured networks for an organization
func (m *Manager) ListCivoNetworks(orgName string) ([]CivoNetwork, error) {
	org, err := m.LoadCivoOrganization(orgName)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, fmt.Errorf("Civo organization '%s' not found", orgName)
	}
	return org.Networks, nil
}
