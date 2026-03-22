package providerconfig

import (
	"fmt"
	"os"

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

// AWSConfig represents AWS-specific configuration
type AWSConfig struct {
	Accounts []AWSAccount `yaml:"accounts,omitempty"`
}

// LoadAWSConfig loads the AWS configuration from the repository
func (m *Manager) LoadAWSConfig() (*AWSConfig, error) {
	configPath := m.getConfigPath("aws")

	data, err := os.ReadFile(configPath)
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

// SaveAWSConfig saves the AWS configuration to the repository
func (m *Manager) SaveAWSConfig(config *AWSConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal AWS config: %w", err)
	}

	configPath := m.getConfigPath("aws")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write AWS config: %w", err)
	}

	return nil
}

// findAWSAccount finds an account by name and returns its index
func (m *Manager) findAWSAccount(config *AWSConfig, name string) int {
	for i, a := range config.Accounts {
		if a.Name == name {
			return i
		}
	}
	return -1
}

// AddAWSAccount adds a named account to the AWS configuration
func (m *Manager) AddAWSAccount(name, accountID string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, name)
	if idx >= 0 {
		config.Accounts[idx].AccountID = accountID
		return m.SaveAWSConfig(config)
	}

	config.Accounts = append(config.Accounts, AWSAccount{
		Name:      name,
		AccountID: accountID,
	})

	return m.SaveAWSConfig(config)
}

// RemoveAWSAccount removes an account by name from the AWS configuration
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

// GetAWSAccountID returns the account ID for a given name/alias
func (m *Manager) GetAWSAccountID(name string) (string, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return "", err
	}

	for _, a := range config.Accounts {
		if a.Name == name {
			return a.AccountID, nil
		}
	}

	return "", fmt.Errorf("AWS account '%s' not found in repository configuration", name)
}

// GetAWSAccount returns the full account config for a given name
func (m *Manager) GetAWSAccount(name string) (*AWSAccount, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}

	for _, a := range config.Accounts {
		if a.Name == name {
			return &a, nil
		}
	}

	return nil, fmt.Errorf("AWS account '%s' not found in repository configuration", name)
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
	config, err := m.LoadAWSConfig()
	if err != nil {
		return false, err
	}

	for _, a := range config.Accounts {
		if a.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// AddAWSEKSRole adds a named EKS role to an account
func (m *Manager) AddAWSEKSRole(accountName, roleName, roleARN string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}

	// Check if role already exists
	for i, r := range config.Accounts[idx].EKSRoles {
		if r.Name == roleName {
			config.Accounts[idx].EKSRoles[i].RoleARN = roleARN
			return m.SaveAWSConfig(config)
		}
	}

	config.Accounts[idx].EKSRoles = append(config.Accounts[idx].EKSRoles, AWSEKSRole{
		Name:    roleName,
		RoleARN: roleARN,
	})

	return m.SaveAWSConfig(config)
}

// RemoveAWSEKSRole removes an EKS role by name from an account
func (m *Manager) RemoveAWSEKSRole(accountName, roleName string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}

	filtered := []AWSEKSRole{}
	found := false
	for _, r := range config.Accounts[idx].EKSRoles {
		if r.Name != roleName {
			filtered = append(filtered, r)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("EKS role '%s' not found in account '%s'", roleName, accountName)
	}

	config.Accounts[idx].EKSRoles = filtered
	return m.SaveAWSConfig(config)
}

// GetAWSEKSRoleARN returns the role ARN for a given role name in an account
func (m *Manager) GetAWSEKSRoleARN(accountName, roleName string) (string, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return "", err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return "", fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for _, r := range config.Accounts[idx].EKSRoles {
		if r.Name == roleName {
			return r.RoleARN, nil
		}
	}

	return "", fmt.Errorf("EKS role '%s' not found in account '%s'", roleName, accountName)
}

// ListAWSEKSRoles returns all configured EKS roles for an account
func (m *Manager) ListAWSEKSRoles(accountName string) ([]AWSEKSRole, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return nil, fmt.Errorf("AWS account '%s' not found", accountName)
	}

	return config.Accounts[idx].EKSRoles, nil
}

// HasAWSEKSRole checks if an EKS role with the given name exists in an account
func (m *Manager) HasAWSEKSRole(accountName, roleName string) (bool, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return false, err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return false, fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for _, r := range config.Accounts[idx].EKSRoles {
		if r.Name == roleName {
			return true, nil
		}
	}

	return false, nil
}

// AddAWSNodeRole adds a named node role to an account
func (m *Manager) AddAWSNodeRole(accountName, roleName, roleARN string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for i, r := range config.Accounts[idx].NodeRoles {
		if r.Name == roleName {
			config.Accounts[idx].NodeRoles[i].RoleARN = roleARN
			return m.SaveAWSConfig(config)
		}
	}

	config.Accounts[idx].NodeRoles = append(config.Accounts[idx].NodeRoles, AWSNodeRole{
		Name:    roleName,
		RoleARN: roleARN,
	})

	return m.SaveAWSConfig(config)
}

// RemoveAWSNodeRole removes a node role by name from an account
func (m *Manager) RemoveAWSNodeRole(accountName, roleName string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}

	filtered := []AWSNodeRole{}
	found := false
	for _, r := range config.Accounts[idx].NodeRoles {
		if r.Name != roleName {
			filtered = append(filtered, r)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("node role '%s' not found in account '%s'", roleName, accountName)
	}

	config.Accounts[idx].NodeRoles = filtered
	return m.SaveAWSConfig(config)
}

// GetAWSNodeRoleARN returns the role ARN for a given node role name in an account
func (m *Manager) GetAWSNodeRoleARN(accountName, roleName string) (string, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return "", err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return "", fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for _, r := range config.Accounts[idx].NodeRoles {
		if r.Name == roleName {
			return r.RoleARN, nil
		}
	}

	return "", fmt.Errorf("node role '%s' not found in account '%s'", roleName, accountName)
}

// ListAWSNodeRoles returns all configured node roles for an account
func (m *Manager) ListAWSNodeRoles(accountName string) ([]AWSNodeRole, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return nil, fmt.Errorf("AWS account '%s' not found", accountName)
	}

	return config.Accounts[idx].NodeRoles, nil
}

// HasAWSNodeRole checks if a node role with the given name exists in an account
func (m *Manager) HasAWSNodeRole(accountName, roleName string) (bool, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return false, err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return false, fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for _, r := range config.Accounts[idx].NodeRoles {
		if r.Name == roleName {
			return true, nil
		}
	}

	return false, nil
}

// AddAWSVPC adds a named VPC to an account
func (m *Manager) AddAWSVPC(accountName, vpcName, vpcID string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for i, v := range config.Accounts[idx].VPCs {
		if v.Name == vpcName {
			config.Accounts[idx].VPCs[i].VPCID = vpcID
			return m.SaveAWSConfig(config)
		}
	}

	config.Accounts[idx].VPCs = append(config.Accounts[idx].VPCs, AWSVPC{
		Name:  vpcName,
		VPCID: vpcID,
	})

	return m.SaveAWSConfig(config)
}

// RemoveAWSVPC removes a VPC by name from an account
func (m *Manager) RemoveAWSVPC(accountName, vpcName string) error {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return fmt.Errorf("AWS account '%s' not found", accountName)
	}

	filtered := []AWSVPC{}
	found := false
	for _, v := range config.Accounts[idx].VPCs {
		if v.Name != vpcName {
			filtered = append(filtered, v)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("VPC '%s' not found in account '%s'", vpcName, accountName)
	}

	config.Accounts[idx].VPCs = filtered
	return m.SaveAWSConfig(config)
}

// GetAWSVPCID returns the VPC ID for a given VPC name in an account
func (m *Manager) GetAWSVPCID(accountName, vpcName string) (string, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return "", err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return "", fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for _, v := range config.Accounts[idx].VPCs {
		if v.Name == vpcName {
			return v.VPCID, nil
		}
	}

	return "", fmt.Errorf("VPC '%s' not found in account '%s'", vpcName, accountName)
}

// ListAWSVPCs returns all configured VPCs for an account
func (m *Manager) ListAWSVPCs(accountName string) ([]AWSVPC, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return nil, err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return nil, fmt.Errorf("AWS account '%s' not found", accountName)
	}

	return config.Accounts[idx].VPCs, nil
}

// HasAWSVPC checks if a VPC with the given name exists in an account
func (m *Manager) HasAWSVPC(accountName, vpcName string) (bool, error) {
	config, err := m.LoadAWSConfig()
	if err != nil {
		return false, err
	}

	idx := m.findAWSAccount(config, accountName)
	if idx < 0 {
		return false, fmt.Errorf("AWS account '%s' not found", accountName)
	}

	for _, v := range config.Accounts[idx].VPCs {
		if v.Name == vpcName {
			return true, nil
		}
	}

	return false, nil
}
