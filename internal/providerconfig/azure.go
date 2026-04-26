package providerconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AzureSubscription represents a named Azure subscription
type AzureSubscription struct {
	Name           string `yaml:"name"`
	SubscriptionID string `yaml:"subscription_id"`
	TenantID       string `yaml:"tenant_id,omitempty"`
	ClientID       string `yaml:"client_id,omitempty"`
	ClientSecret   string `yaml:"client_secret,omitempty"`
}

// AzureConfig represents Azure-specific configuration
type AzureConfig struct {
	Subscriptions []AzureSubscription `yaml:"subscriptions,omitempty"`
}

// LoadAzureConfig reads provider-configs/azure.yaml and returns the full config.
func (m *Manager) LoadAzureConfig() (*AzureConfig, error) {
	data, err := os.ReadFile(m.getConfigPath("azure"))
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

// SaveAzureConfig writes the full Azure config to provider-configs/azure.yaml.
func (m *Manager) SaveAzureConfig(config *AzureConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Azure config: %w", err)
	}
	if err := os.WriteFile(m.getConfigPath("azure"), data, 0644); err != nil {
		return fmt.Errorf("failed to write Azure config: %w", err)
	}
	return nil
}

// LoadAzureSubscription reads the config for a single named Azure subscription.
// Returns (nil, nil) when no subscription with that name exists.
func (m *Manager) LoadAzureSubscription(name string) (*AzureSubscription, error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return nil, err
	}
	for i := range config.Subscriptions {
		if config.Subscriptions[i].Name == name {
			return &config.Subscriptions[i], nil
		}
	}
	return nil, nil
}

// SaveAzureSubscription upserts a subscription entry in provider-configs/azure.yaml.
func (m *Manager) SaveAzureSubscription(sub *AzureSubscription) error {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return err
	}
	for i := range config.Subscriptions {
		if config.Subscriptions[i].Name == sub.Name {
			config.Subscriptions[i] = *sub
			return m.SaveAzureConfig(config)
		}
	}
	config.Subscriptions = append(config.Subscriptions, *sub)
	return m.SaveAzureConfig(config)
}

// AddAzureSubscription adds or updates a named Azure subscription.
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

// RemoveAzureSubscription removes a subscription by name.
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

// GetAzureSubscriptionID returns the subscription ID for a given name.
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

// ListAzureSubscriptions returns all configured Azure subscriptions.
func (m *Manager) ListAzureSubscriptions() ([]AzureSubscription, error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return nil, err
	}
	return config.Subscriptions, nil
}

// HasAzureSubscription checks if a subscription with the given name exists.
func (m *Manager) HasAzureSubscription(name string) (bool, error) {
	sub, err := m.LoadAzureSubscription(name)
	if err != nil {
		return false, err
	}
	return sub != nil, nil
}
