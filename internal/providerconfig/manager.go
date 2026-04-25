package providerconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProviderConfigDir is the directory name for provider configurations
const ProviderConfigDir = "provider-configs"

// Manager handles provider configuration operations
type Manager struct {
	repoPath string
}

// NewManager creates a new provider config manager for a repository
func NewManager(repoPath string) *Manager {
	return &Manager{
		repoPath: repoPath,
	}
}

// getConfigDir returns the provider-configs directory path
func (m *Manager) getConfigDir() string {
	return filepath.Join(m.repoPath, ProviderConfigDir)
}

// getProviderDir returns the per-provider subdirectory (e.g. provider-configs/aws/)
func (m *Manager) getProviderDir(provider string) string {
	return filepath.Join(m.repoPath, ProviderConfigDir, provider)
}

// getAccountConfigPath returns the path to a single account/project/subscription config file.
func (m *Manager) getAccountConfigPath(provider, name string) string {
	return filepath.Join(m.getProviderDir(provider), fmt.Sprintf("%s.yaml", name))
}

// ensureProviderDir creates the provider subdirectory if it doesn't exist.
func (m *Manager) ensureProviderDir(provider string) error {
	dir := m.getProviderDir(provider)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create %s provider config directory: %w", provider, err)
	}
	return nil
}

// resolveCredential resolves a credential field value.
// If the value is wrapped in ${...} it is treated as an environment variable reference
// and the named variable's value is returned. Otherwise the literal value is returned as-is.
func resolveCredential(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return os.Getenv(strings.TrimSpace(v[2 : len(v)-1]))
	}
	return v
}

// GetGCPCredentialsJSON returns the service account credentials JSON for a GCP project.
// The credentials_json field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetGCPCredentialsJSON(projectName string) (string, error) {
	proj, err := m.LoadGCPProject(projectName)
	if err != nil {
		return "", err
	}
	if proj == nil {
		return "", fmt.Errorf("GCP project '%s' not found", projectName)
	}
	return resolveCredential(proj.CredentialsJSON), nil
}

// GetAWSCredentials returns the credentials for an AWS account.
// Each credential field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetAWSCredentials(accountName string) (accessKeyID, secretAccessKey, sessionToken string, err error) {
	acct, loadErr := m.LoadAWSAccount(accountName)
	if loadErr != nil {
		err = loadErr
		return
	}
	if acct == nil {
		err = fmt.Errorf("AWS account '%s' not found", accountName)
		return
	}
	return resolveCredential(acct.AccessKeyID), resolveCredential(acct.SecretAccessKey), resolveCredential(acct.SessionToken), nil
}

// GetAzureCredentials returns the service principal credentials for an Azure subscription.
// Each credential field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetAzureCredentials(subscriptionName string) (tenantID, clientID, clientSecret string, err error) {
	sub, loadErr := m.LoadAzureSubscription(subscriptionName)
	if loadErr != nil {
		err = loadErr
		return
	}
	if sub == nil {
		err = fmt.Errorf("Azure subscription '%s' not found", subscriptionName)
		return
	}
	return resolveCredential(sub.TenantID), resolveCredential(sub.ClientID), resolveCredential(sub.ClientSecret), nil
}

// ConfigExists checks whether at least one config file exists for the given provider.
func (m *Manager) ConfigExists(provider string) bool {
	entries, err := os.ReadDir(m.getProviderDir(provider))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".yaml" {
			return true
		}
	}
	return false
}

// SaveAWSConfig writes each account in config to its own per-account file.
func (m *Manager) SaveAWSConfig(config *AWSConfig) error {
	for i := range config.Accounts {
		if err := m.SaveAWSAccount(&config.Accounts[i]); err != nil {
			return err
		}
	}
	return nil
}

// SaveAzureConfig writes each subscription in config to its own per-subscription file.
func (m *Manager) SaveAzureConfig(config *AzureConfig) error {
	for i := range config.Subscriptions {
		if err := m.SaveAzureSubscription(&config.Subscriptions[i]); err != nil {
			return err
		}
	}
	return nil
}

// SaveGCPConfig writes each project in config to its own per-project file.
func (m *Manager) SaveGCPConfig(config *GCPConfig) error {
	for i := range config.Projects {
		if err := m.SaveGCPProject(&config.Projects[i]); err != nil {
			return err
		}
	}
	return nil
}

// SaveCivoConfig writes each organization in config to its own per-org file.
func (m *Manager) SaveCivoConfig(config *CivoConfig) error {
	for i := range config.Organizations {
		if err := m.SaveCivoOrganization(&config.Organizations[i]); err != nil {
			return err
		}
	}
	return nil
}
