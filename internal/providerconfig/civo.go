package providerconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

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

// CivoConfig represents Civo-specific configuration
type CivoConfig struct {
	Organizations []CivoOrganization `yaml:"organizations,omitempty"`
}

// LoadCivoConfig loads the Civo configuration from the repository
func (m *Manager) LoadCivoConfig() (*CivoConfig, error) {
	configPath := m.getConfigPath("civo")

	data, err := os.ReadFile(configPath)
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

// SaveCivoConfig saves the Civo configuration to the repository
func (m *Manager) SaveCivoConfig(config *CivoConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Civo config: %w", err)
	}

	configPath := m.getConfigPath("civo")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write Civo config: %w", err)
	}

	return nil
}

// AddCivoOrganization adds a named organization to the Civo configuration
func (m *Manager) AddCivoOrganization(name, orgID string) error {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return err
	}

	for i, o := range config.Organizations {
		if o.Name == name {
			config.Organizations[i].OrgID = orgID
			return m.SaveCivoConfig(config)
		}
	}

	config.Organizations = append(config.Organizations, CivoOrganization{
		Name:  name,
		OrgID: orgID,
	})

	return m.SaveCivoConfig(config)
}

// RemoveCivoOrganization removes an organization by name
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

// GetCivoOrgID returns the org ID for a given name
func (m *Manager) GetCivoOrgID(name string) (string, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return "", err
	}

	for _, o := range config.Organizations {
		if o.Name == name {
			return o.OrgID, nil
		}
	}

	return "", fmt.Errorf("Civo organization '%s' not found", name)
}

// GetCivoOrganization returns the full organization config for a given name
func (m *Manager) GetCivoOrganization(name string) (*CivoOrganization, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return nil, err
	}

	for _, o := range config.Organizations {
		if o.Name == name {
			return &o, nil
		}
	}

	return nil, fmt.Errorf("Civo organization '%s' not found", name)
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
	config, err := m.LoadCivoConfig()
	if err != nil {
		return false, err
	}

	for _, o := range config.Organizations {
		if o.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// AddCivoNetwork adds a named network to an organization
func (m *Manager) AddCivoNetwork(orgName, networkName, networkID string) error {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return err
	}

	for i, o := range config.Organizations {
		if o.Name == orgName {
			for j, n := range o.Networks {
				if n.Name == networkName {
					config.Organizations[i].Networks[j].NetworkID = networkID
					return m.SaveCivoConfig(config)
				}
			}
			config.Organizations[i].Networks = append(config.Organizations[i].Networks, CivoNetwork{
				Name:      networkName,
				NetworkID: networkID,
			})
			return m.SaveCivoConfig(config)
		}
	}

	return fmt.Errorf("Civo organization '%s' not found", orgName)
}

// RemoveCivoNetwork removes a network by name from an organization
func (m *Manager) RemoveCivoNetwork(orgName, networkName string) error {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return err
	}

	for i, o := range config.Organizations {
		if o.Name == orgName {
			filtered := []CivoNetwork{}
			found := false
			for _, n := range o.Networks {
				if n.Name != networkName {
					filtered = append(filtered, n)
				} else {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("network '%s' not found in organization '%s'", networkName, orgName)
			}
			config.Organizations[i].Networks = filtered
			return m.SaveCivoConfig(config)
		}
	}

	return fmt.Errorf("Civo organization '%s' not found", orgName)
}

// GetCivoNetworkID returns the network ID for a given network name in an organization
func (m *Manager) GetCivoNetworkID(orgName, networkName string) (string, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return "", err
	}

	for _, o := range config.Organizations {
		if o.Name == orgName {
			for _, n := range o.Networks {
				if n.Name == networkName {
					return n.NetworkID, nil
				}
			}
			return "", fmt.Errorf("network '%s' not found in organization '%s'", networkName, orgName)
		}
	}

	return "", fmt.Errorf("Civo organization '%s' not found", orgName)
}

// ListCivoNetworks returns all configured networks for an organization
func (m *Manager) ListCivoNetworks(orgName string) ([]CivoNetwork, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return nil, err
	}

	for _, o := range config.Organizations {
		if o.Name == orgName {
			return o.Networks, nil
		}
	}

	return nil, fmt.Errorf("Civo organization '%s' not found", orgName)
}
