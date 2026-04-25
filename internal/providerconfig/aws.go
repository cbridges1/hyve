package providerconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AWSEKSRole represents a named EKS IAM role
type AWSEKSRole struct {
	Name    string `yaml:"name"`
	RoleARN string `yaml:"role_arn"`
}

// AWSNodeRole represents a named EKS node IAM role
type AWSNodeRole struct {
	Name    string `yaml:"name"`
	RoleARN string `yaml:"role_arn"`
}

// AWSVPC represents a named VPC
type AWSVPC struct {
	Name  string `yaml:"name"`
	VPCID string `yaml:"vpc_id"`
}

// AWSAccount represents a named AWS account with all its resources
type AWSAccount struct {
	Name            string        `yaml:"name"`
	AccountID       string        `yaml:"account_id"`
	AccessKeyID     string        `yaml:"access_key_id,omitempty"`
	SecretAccessKey string        `yaml:"secret_access_key,omitempty"`
	SessionToken    string        `yaml:"session_token,omitempty"`
	Regions         []string      `yaml:"regions,omitempty"`
	VPCs            []AWSVPC      `yaml:"vpcs,omitempty"`
	EKSRoles        []AWSEKSRole  `yaml:"eks_roles,omitempty"`
	NodeRoles       []AWSNodeRole `yaml:"node_roles,omitempty"`
}

// AWSConfig represents AWS-specific configuration (aggregated view across all account files)
type AWSConfig struct {
	Accounts []AWSAccount `yaml:"accounts,omitempty"`
}

// LoadAWSAccount reads the config file for a single named AWS account.
// Returns (nil, nil) when no file exists for that account.
func (m *Manager) LoadAWSAccount(name string) (*AWSAccount, error) {
	path := m.getAccountConfigPath("aws", name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read AWS account config: %w", err)
	}
	var acct AWSAccount
	if err := yaml.Unmarshal(data, &acct); err != nil {
		return nil, fmt.Errorf("failed to parse AWS account config: %w", err)
	}
	return &acct, nil
}

// SaveAWSAccount writes the config for a single AWS account to its own file.
func (m *Manager) SaveAWSAccount(account *AWSAccount) error {
	if err := m.ensureProviderDir("aws"); err != nil {
		return err
	}
	data, err := yaml.Marshal(account)
	if err != nil {
		return fmt.Errorf("failed to marshal AWS account config: %w", err)
	}
	path := m.getAccountConfigPath("aws", account.Name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write AWS account config: %w", err)
	}
	return nil
}

// LoadAWSConfig reads all per-account files under provider-configs/aws/ and
// returns them as a single aggregated AWSConfig.
func (m *Manager) LoadAWSConfig() (*AWSConfig, error) {
	dir := m.getProviderDir("aws")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &AWSConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read AWS config directory: %w", err)
	}

	var config AWSConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", e.Name(), err)
		}
		var acct AWSAccount
		if err := yaml.Unmarshal(data, &acct); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", e.Name(), err)
		}
		config.Accounts = append(config.Accounts, acct)
	}
	return &config, nil
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

// RemoveAWSAccount removes an account by deleting its config file.
func (m *Manager) RemoveAWSAccount(name string) error {
	path := m.getAccountConfigPath("aws", name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("account '%s' not found", name)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove AWS account config: %w", err)
	}
	return nil
}

// GetAWSAccountID returns the account ID for a given name/alias
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

// GetAWSAccount returns the full account config for a given name
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

// ListAWSAccounts returns all configured AWS accounts
func (m *Manager) ListAWSAccounts() ([]AWSAccount, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}
	return config.Accounts, nil
}

// HasAWSAccount checks if an account with the given name exists
func (m *Manager) HasAWSAccount(name string) (bool, error) {
	acct, err := m.LoadAWSAccount(name)
	if err != nil {
		return false, err
	}
	return acct != nil, nil
}

// AddAWSEKSRole adds a named EKS role to an account
func (m *Manager) AddAWSEKSRole(accountName, roleName, roleARN string) error {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for i, r := range acct.EKSRoles {
		if r.Name == roleName {
			acct.EKSRoles[i].RoleARN = roleARN
			return m.SaveAWSAccount(acct)
		}
	}
	acct.EKSRoles = append(acct.EKSRoles, AWSEKSRole{Name: roleName, RoleARN: roleARN})
	return m.SaveAWSAccount(acct)
}

// RemoveAWSEKSRole removes an EKS role by name from an account
func (m *Manager) RemoveAWSEKSRole(accountName, roleName string) error {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}
	filtered := []AWSEKSRole{}
	found := false
	for _, r := range acct.EKSRoles {
		if r.Name != roleName {
			filtered = append(filtered, r)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("EKS role '%s' not found in account '%s'", roleName, accountName)
	}
	acct.EKSRoles = filtered
	return m.SaveAWSAccount(acct)
}

// GetAWSEKSRoleARN returns the role ARN for a given role name in an account
func (m *Manager) GetAWSEKSRoleARN(accountName, roleName string) (string, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return "", err
	}
	if acct == nil {
		return "", fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for _, r := range acct.EKSRoles {
		if r.Name == roleName {
			return r.RoleARN, nil
		}
	}
	return "", fmt.Errorf("EKS role '%s' not found in account '%s'", roleName, accountName)
}

// ListAWSEKSRoles returns all configured EKS roles for an account
func (m *Manager) ListAWSEKSRoles(accountName string) ([]AWSEKSRole, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, fmt.Errorf("AWS account '%s' not found", accountName)
	}
	return acct.EKSRoles, nil
}

// HasAWSEKSRole checks if an EKS role with the given name exists in an account
func (m *Manager) HasAWSEKSRole(accountName, roleName string) (bool, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return false, err
	}
	if acct == nil {
		return false, fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for _, r := range acct.EKSRoles {
		if r.Name == roleName {
			return true, nil
		}
	}
	return false, nil
}

// AddAWSNodeRole adds a named node role to an account
func (m *Manager) AddAWSNodeRole(accountName, roleName, roleARN string) error {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for i, r := range acct.NodeRoles {
		if r.Name == roleName {
			acct.NodeRoles[i].RoleARN = roleARN
			return m.SaveAWSAccount(acct)
		}
	}
	acct.NodeRoles = append(acct.NodeRoles, AWSNodeRole{Name: roleName, RoleARN: roleARN})
	return m.SaveAWSAccount(acct)
}

// RemoveAWSNodeRole removes a node role by name from an account
func (m *Manager) RemoveAWSNodeRole(accountName, roleName string) error {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}
	filtered := []AWSNodeRole{}
	found := false
	for _, r := range acct.NodeRoles {
		if r.Name != roleName {
			filtered = append(filtered, r)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("node role '%s' not found in account '%s'", roleName, accountName)
	}
	acct.NodeRoles = filtered
	return m.SaveAWSAccount(acct)
}

// GetAWSNodeRoleARN returns the role ARN for a given node role name in an account
func (m *Manager) GetAWSNodeRoleARN(accountName, roleName string) (string, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return "", err
	}
	if acct == nil {
		return "", fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for _, r := range acct.NodeRoles {
		if r.Name == roleName {
			return r.RoleARN, nil
		}
	}
	return "", fmt.Errorf("node role '%s' not found in account '%s'", roleName, accountName)
}

// ListAWSNodeRoles returns all configured node roles for an account
func (m *Manager) ListAWSNodeRoles(accountName string) ([]AWSNodeRole, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, fmt.Errorf("AWS account '%s' not found", accountName)
	}
	return acct.NodeRoles, nil
}

// HasAWSNodeRole checks if a node role with the given name exists in an account
func (m *Manager) HasAWSNodeRole(accountName, roleName string) (bool, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return false, err
	}
	if acct == nil {
		return false, fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for _, r := range acct.NodeRoles {
		if r.Name == roleName {
			return true, nil
		}
	}
	return false, nil
}

// AddAWSVPC adds a named VPC to an account
func (m *Manager) AddAWSVPC(accountName, vpcName, vpcID string) error {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for i, v := range acct.VPCs {
		if v.Name == vpcName {
			acct.VPCs[i].VPCID = vpcID
			return m.SaveAWSAccount(acct)
		}
	}
	acct.VPCs = append(acct.VPCs, AWSVPC{Name: vpcName, VPCID: vpcID})
	return m.SaveAWSAccount(acct)
}

// RemoveAWSVPC removes a VPC by name from an account
func (m *Manager) RemoveAWSVPC(accountName, vpcName string) error {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}
	filtered := []AWSVPC{}
	found := false
	for _, v := range acct.VPCs {
		if v.Name != vpcName {
			filtered = append(filtered, v)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("VPC '%s' not found in account '%s'", vpcName, accountName)
	}
	acct.VPCs = filtered
	return m.SaveAWSAccount(acct)
}

// GetAWSVPCID returns the VPC ID for a given VPC name in an account
func (m *Manager) GetAWSVPCID(accountName, vpcName string) (string, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return "", err
	}
	if acct == nil {
		return "", fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for _, v := range acct.VPCs {
		if v.Name == vpcName {
			return v.VPCID, nil
		}
	}
	return "", fmt.Errorf("VPC '%s' not found in account '%s'", vpcName, accountName)
}

// ListAWSVPCs returns all configured VPCs for an account
func (m *Manager) ListAWSVPCs(accountName string) ([]AWSVPC, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, fmt.Errorf("AWS account '%s' not found", accountName)
	}
	return acct.VPCs, nil
}

// HasAWSVPC checks if a VPC with the given name exists in an account
func (m *Manager) HasAWSVPC(accountName, vpcName string) (bool, error) {
	acct, err := m.LoadAWSAccount(accountName)
	if err != nil {
		return false, err
	}
	if acct == nil {
		return false, fmt.Errorf("AWS account '%s' not found", accountName)
	}
	for _, v := range acct.VPCs {
		if v.Name == vpcName {
			return true, nil
		}
	}
	return false, nil
}
