package providerconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AzureResourceGroup represents a named resource group
type AzureResourceGroup struct {
	Name     string `yaml:"name"`
	Location string `yaml:"location,omitempty"`
}

// AzureSubscription represents a named Azure subscription
type AzureSubscription struct {
	Name           string               `yaml:"name"`
	SubscriptionID string               `yaml:"subscription_id"`
	TenantID       string               `yaml:"tenant_id,omitempty"`
	ClientID       string               `yaml:"client_id,omitempty"`
	ClientSecret   string               `yaml:"client_secret,omitempty"`
	ResourceGroups []AzureResourceGroup `yaml:"resource_groups,omitempty"`
}

// AzureConfig represents Azure-specific configuration
type AzureConfig struct {
	Subscriptions []AzureSubscription `yaml:"subscriptions,omitempty"`
}

// LoadAzureConfig loads the Azure configuration from the repository
func (m *Manager) LoadAzureConfig() (*AzureConfig, error) {
	configPath := m.getConfigPath("azure")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &AzureConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read Azure config: %w", err)
	}

	var config AzureConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse Azure config: %w", err)
	}

	return &config, nil
}

// SaveAzureConfig saves the Azure configuration to the repository
func (m *Manager) SaveAzureConfig(config *AzureConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Azure config: %w", err)
	}

	configPath := m.getConfigPath("azure")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write Azure config: %w", err)
	}

	return nil
}

// AddAzureSubscription adds a named subscription to the Azure configuration
func (m *Manager) AddAzureSubscription(name, subscriptionID string) error {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return err
	}

	for i, s := range config.Subscriptions {
		if s.Name == name {
			config.Subscriptions[i].SubscriptionID = subscriptionID
			return m.SaveAzureConfig(config)
		}
	}

	config.Subscriptions = append(config.Subscriptions, AzureSubscription{
		Name:           name,
		SubscriptionID: subscriptionID,
	})

	return m.SaveAzureConfig(config)
}

// RemoveAzureSubscription removes a subscription by name
func (m *Manager) RemoveAzureSubscription(name string) error {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return err
	}

	filtered := []AzureSubscription{}
	found := false
	for _, s := range config.Subscriptions {
		if s.Name != name {
			filtered = append(filtered, s)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("subscription '%s' not found", name)
	}

	config.Subscriptions = filtered
	return m.SaveAzureConfig(config)
}

// GetAzureSubscriptionID returns the subscription ID for a given name
func (m *Manager) GetAzureSubscriptionID(name string) (string, error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return "", err
	}

	for _, s := range config.Subscriptions {
		if s.Name == name {
			return s.SubscriptionID, nil
		}
	}

	return "", fmt.Errorf("Azure subscription '%s' not found", name)
}

// ListAzureSubscriptions returns all configured subscriptions
func (m *Manager) ListAzureSubscriptions() ([]AzureSubscription, error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return nil, err
	}

	return config.Subscriptions, nil
}

// HasAzureSubscription checks if a subscription with the given name exists
func (m *Manager) HasAzureSubscription(name string) (bool, error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return false, err
	}

	for _, s := range config.Subscriptions {
		if s.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// AddAzureResourceGroup adds a resource group to a named subscription
func (m *Manager) AddAzureResourceGroup(subscriptionName, rgName, location string) error {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return err
	}

	for i, s := range config.Subscriptions {
		if s.Name != subscriptionName {
			continue
		}
		for j, rg := range s.ResourceGroups {
			if rg.Name == rgName {
				config.Subscriptions[i].ResourceGroups[j].Location = location
				return m.SaveAzureConfig(config)
			}
		}
		config.Subscriptions[i].ResourceGroups = append(config.Subscriptions[i].ResourceGroups, AzureResourceGroup{
			Name:     rgName,
			Location: location,
		})
		return m.SaveAzureConfig(config)
	}

	return fmt.Errorf("subscription '%s' not found", subscriptionName)
}

// ListAzureResourceGroups returns all resource groups for a named subscription
func (m *Manager) ListAzureResourceGroups(subscriptionName string) ([]AzureResourceGroup, error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return nil, err
	}

	for _, s := range config.Subscriptions {
		if s.Name == subscriptionName {
			return s.ResourceGroups, nil
		}
	}

	return nil, fmt.Errorf("subscription '%s' not found", subscriptionName)
}

// RemoveAzureResourceGroup removes a resource group from a named subscription
func (m *Manager) RemoveAzureResourceGroup(subscriptionName, rgName string) error {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return err
	}

	for i, s := range config.Subscriptions {
		if s.Name != subscriptionName {
			continue
		}
		filtered := []AzureResourceGroup{}
		found := false
		for _, rg := range s.ResourceGroups {
			if rg.Name != rgName {
				filtered = append(filtered, rg)
			} else {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("resource group '%s' not found in subscription '%s'", rgName, subscriptionName)
		}
		config.Subscriptions[i].ResourceGroups = filtered
		return m.SaveAzureConfig(config)
	}

	return fmt.Errorf("subscription '%s' not found", subscriptionName)
}
