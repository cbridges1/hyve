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

// getConfigPath returns the path to a provider's config file
func (m *Manager) getConfigPath(provider string) string {
	return filepath.Join(m.getConfigDir(), fmt.Sprintf("%s.yaml", provider))
}

// ensureConfigDir creates the provider-configs directory if it doesn't exist
func (m *Manager) ensureConfigDir() error {
	configDir := m.getConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create provider-configs directory: %w", err)
	}
	return nil
}

// ConfigExists checks if a provider config file exists
func (m *Manager) ConfigExists(provider string) bool {
	configPath := m.getConfigPath(provider)
	_, err := os.Stat(configPath)
	return err == nil
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

// GetCivoToken returns the API token for a Civo organization.
// The token field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetCivoToken(orgName string) (string, error) {
	config, err := m.LoadCivoConfig()
	if err != nil {
		return "", err
	}
	for _, o := range config.Organizations {
		if o.Name == orgName {
			return resolveCredential(o.Token), nil
		}
	}
	return "", fmt.Errorf("Civo organization '%s' not found", orgName)
}

// GetGCPCredentialsJSON returns the service account credentials JSON for a GCP project.
// The credentials_json field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetGCPCredentialsJSON(projectName string) (string, error) {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return "", err
	}
	for _, p := range config.Projects {
		if p.Name == projectName {
			return resolveCredential(p.CredentialsJSON), nil
		}
	}
	return "", fmt.Errorf("GCP project '%s' not found", projectName)
}

// GetAWSCredentials returns the credentials for an AWS account.
// Each credential field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetAWSCredentials(accountName string) (accessKeyID, secretAccessKey, sessionToken string, err error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return
	}
	for _, a := range config.Accounts {
		if a.Name == accountName {
			return resolveCredential(a.AccessKeyID), resolveCredential(a.SecretAccessKey), resolveCredential(a.SessionToken), nil
		}
	}
	err = fmt.Errorf("AWS account '%s' not found", accountName)
	return
}

// GetAzureCredentials returns the service principal credentials for an Azure subscription.
// Each credential field may be a literal value or an env var reference (${VAR_NAME}).
func (m *Manager) GetAzureCredentials(subscriptionName string) (tenantID, clientID, clientSecret string, err error) {
	config, err := m.LoadAzureConfig()
	if err != nil {
		return
	}
	for _, s := range config.Subscriptions {
		if s.Name == subscriptionName {
			return resolveCredential(s.TenantID), resolveCredential(s.ClientID), resolveCredential(s.ClientSecret), nil
		}
	}
	err = fmt.Errorf("Azure subscription '%s' not found", subscriptionName)
	return
}
