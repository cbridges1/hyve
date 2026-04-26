package providerconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AWSAccount represents a named AWS account
type AWSAccount struct {
	Name            string   `yaml:"name"`
	AccountID       string   `yaml:"account_id"`
	AccessKeyID     string   `yaml:"access_key_id,omitempty"`
	SecretAccessKey string   `yaml:"secret_access_key,omitempty"`
	SessionToken    string   `yaml:"session_token,omitempty"`
	Regions         []string `yaml:"regions,omitempty"`
}

// AWSConfig represents AWS-specific configuration
type AWSConfig struct {
	Accounts []AWSAccount `yaml:"accounts,omitempty"`
}

// LoadAWSConfig reads provider-configs/aws.yaml and returns the full config.
func (m *Manager) LoadAWSConfig() (*AWSConfig, error) {
	data, err := os.ReadFile(m.getConfigPath("aws"))
	if err != nil {
		if os.IsNotExist(err) {
			return &AWSConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read AWS config: %w", err)
	}
	var config AWSConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse AWS config: %w", err)
	}
	return &config, nil
}

// SaveAWSConfig writes the full AWS config to provider-configs/aws.yaml.
func (m *Manager) SaveAWSConfig(config *AWSConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal AWS config: %w", err)
	}
	if err := os.WriteFile(m.getConfigPath("aws"), data, 0644); err != nil {
		return fmt.Errorf("failed to write AWS config: %w", err)
	}
	return nil
}

// LoadAWSAccount reads the config for a single named AWS account.
// Returns (nil, nil) when no account with that name exists.
func (m *Manager) LoadAWSAccount(name string) (*AWSAccount, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}
	for i := range config.Accounts {
		if config.Accounts[i].Name == name {
			return &config.Accounts[i], nil
		}
	}
	return nil, nil
}

// SaveAWSAccount upserts an account entry in provider-configs/aws.yaml.
func (m *Manager) SaveAWSAccount(account *AWSAccount) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}
	for i := range config.Accounts {
		if config.Accounts[i].Name == account.Name {
			config.Accounts[i] = *account
			return m.SaveAWSConfig(config)
		}
	}
	config.Accounts = append(config.Accounts, *account)
	return m.SaveAWSConfig(config)
}

// AddAWSAccount adds or updates a named AWS account.
func (m *Manager) AddAWSAccount(name, accountID string) error {
	acct, err := m.LoadAWSAccount(name)
	if err != nil {
		return err
	}
	if acct == nil {
		acct = &AWSAccount{Name: name}
	}
	acct.AccountID = accountID
	return m.SaveAWSAccount(acct)
}

// RemoveAWSAccount removes an account by name.
func (m *Manager) RemoveAWSAccount(name string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}
	filtered := []AWSAccount{}
	found := false
	for _, a := range config.Accounts {
		if a.Name != name {
			filtered = append(filtered, a)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("account '%s' not found", name)
	}
	config.Accounts = filtered
	return m.SaveAWSConfig(config)
}

// GetAWSAccountID returns the account ID for a given name.
func (m *Manager) GetAWSAccountID(name string) (string, error) {
	acct, err := m.LoadAWSAccount(name)
	if err != nil {
		return "", err
	}
	if acct == nil {
		return "", fmt.Errorf("AWS account '%s' not found in repository configuration", name)
	}
	return acct.AccountID, nil
}

// GetAWSAccount returns the full account config for a given name.
func (m *Manager) GetAWSAccount(name string) (*AWSAccount, error) {
	acct, err := m.LoadAWSAccount(name)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, fmt.Errorf("AWS account '%s' not found in repository configuration", name)
	}
	return acct, nil
}

// ListAWSAccounts returns all configured AWS accounts.
func (m *Manager) ListAWSAccounts() ([]AWSAccount, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}
	return config.Accounts, nil
}

// HasAWSAccount checks if an account with the given name exists.
func (m *Manager) HasAWSAccount(name string) (bool, error) {
	acct, err := m.LoadAWSAccount(name)
	if err != nil {
		return false, err
	}
	return acct != nil, nil
}
