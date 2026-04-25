package providerconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// AzureConfig represents Azure-specific configuration (aggregated view across all subscription files)
type AzureConfig struct {
	Subscriptions []AzureSubscription `yaml:"subscriptions,omitempty"`
}

// LoadAzureSubscription reads the config file for a single named Azure subscription.
// Returns (nil, nil) when no file exists for that subscription.
func (m *Manager) LoadAzureSubscription(name string) (*AzureSubscription, error) {
	path := m.getAccountConfigPath("azure", name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read Azure subscription config: %w", err)
	}
	var sub AzureSubscription
	if err := yaml.Unmarshal(data, &sub); err != nil {
		return nil, fmt.Errorf("failed to parse Azure subscription config: %w", err)
	}
	return &sub, nil
}

// SaveAzureSubscription writes the config for a single Azure subscription to its own file.
func (m *Manager) SaveAzureSubscription(sub *AzureSubscription) error {
	if err := m.ensureProviderDir("azure"); err != nil {
		return err
	}
	data, err := yaml.Marshal(sub)
	if err != nil {
		return fmt.Errorf("failed to marshal Azure subscription config: %w", err)
	}
	path := m.getAccountConfigPath("azure", sub.Name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write Azure subscription config: %w", err)
	}
	return nil
}

// LoadAzureConfig reads all per-subscription files under provider-configs/azure/ and
// returns them as a single aggregated AzureConfig.
func (m *Manager) LoadAzureConfig() (*AzureConfig, error) {
	dir := m.getProviderDir("azure")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &AzureConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read Azure config directory: %w", err)
	}

	var config AzureConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", e.Name(), err)
		}
		var sub AzureSubscription
		if err := yaml.Unmarshal(data, &sub); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", e.Name(), err)
		}
		config.Subscriptions = append(config.Subscriptions, sub)
	}
	return &config, nil
}

// AddAzureSubscription adds or updates a named subscription.
func (m *Manager) AddAzureSubscription(name, subscriptionID string) error {
	sub, err := m.LoadAzureSubscription(name)
	if err != nil {
		return err
	}
	if sub == nil {
		sub = &AzureSubscription{Name: name}
	}
	sub.SubscriptionID = subscriptionID
	return m.SaveAzureSubscription(sub)
}

// RemoveAzureSubscription removes a subscription by deleting its config file.
func (m *Manager) RemoveAzureSubscription(name string) error {
	path := m.getAccountConfigPath("azure", name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("subscription '%s' not found", name)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove Azure subscription config: %w", err)
	}
	return nil
}

// GetAzureSubscriptionID returns the subscription ID for a given name
func (m *Manager) GetAzureSubscriptionID(name string) (string, error) {
	sub, err := m.LoadAzureSubscription(name)
	if err != nil {
		return "", err
	}
	if sub == nil {
		return "", fmt.Errorf("Azure subscription '%s' not found", name)
	}
	return sub.SubscriptionID, nil
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
	sub, err := m.LoadAzureSubscription(name)
	if err != nil {
		return false, err
	}
	return sub != nil, nil
}

// AddAzureResourceGroup adds a resource group to a named subscription
func (m *Manager) AddAzureResourceGroup(subscriptionName, rgName, location string) error {
	sub, err := m.LoadAzureSubscription(subscriptionName)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("subscription '%s' not found", subscriptionName)
	}
	for i, rg := range sub.ResourceGroups {
		if rg.Name == rgName {
			sub.ResourceGroups[i].Location = location
			return m.SaveAzureSubscription(sub)
		}
	}
	sub.ResourceGroups = append(sub.ResourceGroups, AzureResourceGroup{Name: rgName, Location: location})
	return m.SaveAzureSubscription(sub)
}

// ListAzureResourceGroups returns all resource groups for a named subscription
func (m *Manager) ListAzureResourceGroups(subscriptionName string) ([]AzureResourceGroup, error) {
	sub, err := m.LoadAzureSubscription(subscriptionName)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, fmt.Errorf("subscription '%s' not found", subscriptionName)
	}
	return sub.ResourceGroups, nil
}

// RemoveAzureResourceGroup removes a resource group from a named subscription
func (m *Manager) RemoveAzureResourceGroup(subscriptionName, rgName string) error {
	sub, err := m.LoadAzureSubscription(subscriptionName)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("subscription '%s' not found", subscriptionName)
	}
	filtered := []AzureResourceGroup{}
	found := false
	for _, rg := range sub.ResourceGroups {
		if rg.Name != rgName {
			filtered = append(filtered, rg)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("resource group '%s' not found in subscription '%s'", rgName, subscriptionName)
	}
	sub.ResourceGroups = filtered
	return m.SaveAzureSubscription(sub)
}
