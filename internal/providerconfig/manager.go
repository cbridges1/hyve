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

// getConfigPath returns the path to the single flat config file for a provider.
func (m *Manager) getConfigPath(provider string) string {
	return filepath.Join(m.repoPath, ProviderConfigDir, fmt.Sprintf("%s.yaml", provider))
}

// ensureConfigDir creates the provider-configs directory if it doesn't exist.
func (m *Manager) ensureConfigDir() error {
	if err := os.MkdirAll(m.getConfigDir(), 0755); err != nil {
		return fmt.Errorf("failed to create provider-configs directory: %w", err)
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

// ConfigExists checks whether the config file for the given provider exists.
func (m *Manager) ConfigExists(provider string) bool {
	_, err := os.Stat(m.getConfigPath(provider))
	return err == nil
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
