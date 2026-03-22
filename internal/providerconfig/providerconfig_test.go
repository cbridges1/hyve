package providerconfig

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(t.TempDir())
}

// ========== GCP ==========

func TestGCP_AddAndGet(t *testing.T) {
	mgr := newTestManager(t)

	err := mgr.AddGCPProject("dev", "my-dev-project-123")
	require.NoError(t, err)

	id, err := mgr.GetGCPProjectID("dev")
	require.NoError(t, err)
	assert.Equal(t, "my-dev-project-123", id)
}

func TestGCP_UpdateProject(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddGCPProject("dev", "old-project-id"))
	require.NoError(t, mgr.AddGCPProject("dev", "new-project-id"))

	id, err := mgr.GetGCPProjectID("dev")
	require.NoError(t, err)
	assert.Equal(t, "new-project-id", id)
}

func TestGCP_ListProjects(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddGCPProject("dev", "dev-id"))
	require.NoError(t, mgr.AddGCPProject("prod", "prod-id"))

	projects, err := mgr.ListGCPProjects()
	require.NoError(t, err)
	assert.Len(t, projects, 2)
}

func TestGCP_ListProjects_Empty(t *testing.T) {
	mgr := newTestManager(t)

	projects, err := mgr.ListGCPProjects()
	require.NoError(t, err)
	assert.Empty(t, projects)
}

func TestGCP_HasProject(t *testing.T) {
	mgr := newTestManager(t)

	has, err := mgr.HasGCPProject("dev")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddGCPProject("dev", "dev-id"))

	has, err = mgr.HasGCPProject("dev")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestGCP_RemoveProject(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddGCPProject("dev", "dev-id"))
	require.NoError(t, mgr.AddGCPProject("prod", "prod-id"))

	err := mgr.RemoveGCPProject("dev")
	require.NoError(t, err)

	has, err := mgr.HasGCPProject("dev")
	require.NoError(t, err)
	assert.False(t, has)

	projects, err := mgr.ListGCPProjects()
	require.NoError(t, err)
	assert.Len(t, projects, 1)
}

func TestGCP_RemoveProject_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	err := mgr.RemoveGCPProject("nonexistent")
	assert.Error(t, err)
}

func TestGCP_GetProject_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetGCPProjectID("nonexistent")
	assert.Error(t, err)
}

func TestGCP_ConfigExists(t *testing.T) {
	mgr := newTestManager(t)

	assert.False(t, mgr.ConfigExists("gcp"))

	require.NoError(t, mgr.AddGCPProject("dev", "dev-id"))

	assert.True(t, mgr.ConfigExists("gcp"))
}

// ========== AWS Accounts ==========

func TestAWS_AddAndGetAccount(t *testing.T) {
	mgr := newTestManager(t)

	err := mgr.AddAWSAccount("prod", "123456789012")
	require.NoError(t, err)

	id, err := mgr.GetAWSAccountID("prod")
	require.NoError(t, err)
	assert.Equal(t, "123456789012", id)
}

func TestAWS_GetFullAccount(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))

	account, err := mgr.GetAWSAccount("prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", account.Name)
	assert.Equal(t, "123456789012", account.AccountID)
}

func TestAWS_UpdateAccount(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "111111111111"))
	require.NoError(t, mgr.AddAWSAccount("prod", "222222222222"))

	id, err := mgr.GetAWSAccountID("prod")
	require.NoError(t, err)
	assert.Equal(t, "222222222222", id)
}

func TestAWS_ListAccounts(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "111111111111"))
	require.NoError(t, mgr.AddAWSAccount("dev", "222222222222"))

	accounts, err := mgr.ListAWSAccounts()
	require.NoError(t, err)
	assert.Len(t, accounts, 2)
}

func TestAWS_HasAccount(t *testing.T) {
	mgr := newTestManager(t)

	has, err := mgr.HasAWSAccount("prod")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))

	has, err = mgr.HasAWSAccount("prod")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAWS_RemoveAccount(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.RemoveAWSAccount("prod"))

	has, err := mgr.HasAWSAccount("prod")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestAWS_RemoveAccount_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.RemoveAWSAccount("nonexistent"))
}

func TestAWS_GetAccount_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetAWSAccountID("nonexistent")
	assert.Error(t, err)
}

// ========== AWS EKS Roles ==========

func TestAWS_AddAndGetEKSRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSEKSRole("prod", "eks-role", "arn:aws:iam::123456789012:role/eks-role"))

	arn, err := mgr.GetAWSEKSRoleARN("prod", "eks-role")
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:iam::123456789012:role/eks-role", arn)
}

func TestAWS_UpdateEKSRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSEKSRole("prod", "eks-role", "arn:old"))
	require.NoError(t, mgr.AddAWSEKSRole("prod", "eks-role", "arn:new"))

	arn, err := mgr.GetAWSEKSRoleARN("prod", "eks-role")
	require.NoError(t, err)
	assert.Equal(t, "arn:new", arn)
}

func TestAWS_ListEKSRoles(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSEKSRole("prod", "role-1", "arn:1"))
	require.NoError(t, mgr.AddAWSEKSRole("prod", "role-2", "arn:2"))

	roles, err := mgr.ListAWSEKSRoles("prod")
	require.NoError(t, err)
	assert.Len(t, roles, 2)
}

func TestAWS_HasEKSRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))

	has, err := mgr.HasAWSEKSRole("prod", "eks-role")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddAWSEKSRole("prod", "eks-role", "arn:aws:iam::123456789012:role/eks-role"))

	has, err = mgr.HasAWSEKSRole("prod", "eks-role")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAWS_RemoveEKSRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSEKSRole("prod", "eks-role", "arn:aws:iam::123456789012:role/eks-role"))
	require.NoError(t, mgr.RemoveAWSEKSRole("prod", "eks-role"))

	has, err := mgr.HasAWSEKSRole("prod", "eks-role")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestAWS_EKSRole_AccountNotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.AddAWSEKSRole("nonexistent", "role", "arn"))
	_, err := mgr.GetAWSEKSRoleARN("nonexistent", "role")
	assert.Error(t, err)
}

// ========== AWS Node Roles ==========

func TestAWS_AddAndGetNodeRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSNodeRole("prod", "node-role", "arn:aws:iam::123456789012:role/node-role"))

	arn, err := mgr.GetAWSNodeRoleARN("prod", "node-role")
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:iam::123456789012:role/node-role", arn)
}

func TestAWS_ListNodeRoles(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSNodeRole("prod", "role-1", "arn:1"))
	require.NoError(t, mgr.AddAWSNodeRole("prod", "role-2", "arn:2"))

	roles, err := mgr.ListAWSNodeRoles("prod")
	require.NoError(t, err)
	assert.Len(t, roles, 2)
}

func TestAWS_HasNodeRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))

	has, err := mgr.HasAWSNodeRole("prod", "node-role")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddAWSNodeRole("prod", "node-role", "arn:aws:iam::123456789012:role/node-role"))

	has, err = mgr.HasAWSNodeRole("prod", "node-role")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAWS_RemoveNodeRole(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSNodeRole("prod", "node-role", "arn:aws:iam::123456789012:role/node-role"))
	require.NoError(t, mgr.RemoveAWSNodeRole("prod", "node-role"))

	has, err := mgr.HasAWSNodeRole("prod", "node-role")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestAWS_NodeRole_AccountNotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.AddAWSNodeRole("nonexistent", "role", "arn"))
	_, err := mgr.GetAWSNodeRoleARN("nonexistent", "role")
	assert.Error(t, err)
}

// ========== AWS VPCs ==========

func TestAWS_AddAndGetVPC(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSVPC("prod", "my-vpc", "vpc-0123456789abcdef0"))

	id, err := mgr.GetAWSVPCID("prod", "my-vpc")
	require.NoError(t, err)
	assert.Equal(t, "vpc-0123456789abcdef0", id)
}

func TestAWS_UpdateVPC(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSVPC("prod", "my-vpc", "vpc-old"))
	require.NoError(t, mgr.AddAWSVPC("prod", "my-vpc", "vpc-new"))

	id, err := mgr.GetAWSVPCID("prod", "my-vpc")
	require.NoError(t, err)
	assert.Equal(t, "vpc-new", id)
}

func TestAWS_ListVPCs(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSVPC("prod", "vpc-1", "vpc-aaa"))
	require.NoError(t, mgr.AddAWSVPC("prod", "vpc-2", "vpc-bbb"))

	vpcs, err := mgr.ListAWSVPCs("prod")
	require.NoError(t, err)
	assert.Len(t, vpcs, 2)
}

func TestAWS_HasVPC(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))

	has, err := mgr.HasAWSVPC("prod", "my-vpc")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddAWSVPC("prod", "my-vpc", "vpc-0123456789abcdef0"))

	has, err = mgr.HasAWSVPC("prod", "my-vpc")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAWS_RemoveVPC(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	require.NoError(t, mgr.AddAWSVPC("prod", "my-vpc", "vpc-0123456789abcdef0"))
	require.NoError(t, mgr.RemoveAWSVPC("prod", "my-vpc"))

	has, err := mgr.HasAWSVPC("prod", "my-vpc")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestAWS_VPC_AccountNotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.AddAWSVPC("nonexistent", "vpc", "vpc-id"))
	_, err := mgr.GetAWSVPCID("nonexistent", "vpc")
	assert.Error(t, err)
}

func TestAWS_RemoveVPC_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	assert.Error(t, mgr.RemoveAWSVPC("prod", "nonexistent"))
}

// ========== Azure ==========

func TestAzure_AddAndGet(t *testing.T) {
	mgr := newTestManager(t)

	err := mgr.AddAzureSubscription("prod", "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
	require.NoError(t, err)

	id, err := mgr.GetAzureSubscriptionID("prod")
	require.NoError(t, err)
	assert.Equal(t, "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", id)
}

func TestAzure_UpdateSubscription(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAzureSubscription("prod", "old-id"))
	require.NoError(t, mgr.AddAzureSubscription("prod", "new-id"))

	id, err := mgr.GetAzureSubscriptionID("prod")
	require.NoError(t, err)
	assert.Equal(t, "new-id", id)
}

func TestAzure_ListSubscriptions(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAzureSubscription("prod", "prod-id"))
	require.NoError(t, mgr.AddAzureSubscription("dev", "dev-id"))

	subs, err := mgr.ListAzureSubscriptions()
	require.NoError(t, err)
	assert.Len(t, subs, 2)
}

func TestAzure_ListSubscriptions_Empty(t *testing.T) {
	mgr := newTestManager(t)

	subs, err := mgr.ListAzureSubscriptions()
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestAzure_HasSubscription(t *testing.T) {
	mgr := newTestManager(t)

	has, err := mgr.HasAzureSubscription("prod")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddAzureSubscription("prod", "prod-id"))

	has, err = mgr.HasAzureSubscription("prod")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAzure_RemoveSubscription(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAzureSubscription("prod", "prod-id"))
	require.NoError(t, mgr.RemoveAzureSubscription("prod"))

	has, err := mgr.HasAzureSubscription("prod")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestAzure_RemoveSubscription_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.RemoveAzureSubscription("nonexistent"))
}

func TestAzure_GetSubscription_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetAzureSubscriptionID("nonexistent")
	assert.Error(t, err)
}

// ========== Civo ==========

func TestCivo_AddAndGet(t *testing.T) {
	mgr := newTestManager(t)

	err := mgr.AddCivoOrganization("my-org", "org-123456")
	require.NoError(t, err)

	id, err := mgr.GetCivoOrgID("my-org")
	require.NoError(t, err)
	assert.Equal(t, "org-123456", id)
}

func TestCivo_GetFullOrganization(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123456"))

	org, err := mgr.GetCivoOrganization("my-org")
	require.NoError(t, err)
	assert.Equal(t, "my-org", org.Name)
	assert.Equal(t, "org-123456", org.OrgID)
}

func TestCivo_UpdateOrganization(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "old-org-id"))
	require.NoError(t, mgr.AddCivoOrganization("my-org", "new-org-id"))

	id, err := mgr.GetCivoOrgID("my-org")
	require.NoError(t, err)
	assert.Equal(t, "new-org-id", id)
}

func TestCivo_ListOrganizations(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("org-1", "id-1"))
	require.NoError(t, mgr.AddCivoOrganization("org-2", "id-2"))

	orgs, err := mgr.ListCivoOrganizations()
	require.NoError(t, err)
	assert.Len(t, orgs, 2)
}

func TestCivo_ListOrganizations_Empty(t *testing.T) {
	mgr := newTestManager(t)

	orgs, err := mgr.ListCivoOrganizations()
	require.NoError(t, err)
	assert.Empty(t, orgs)
}

func TestCivo_HasOrganization(t *testing.T) {
	mgr := newTestManager(t)

	has, err := mgr.HasCivoOrganization("my-org")
	require.NoError(t, err)
	assert.False(t, has)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))

	has, err = mgr.HasCivoOrganization("my-org")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestCivo_RemoveOrganization(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	require.NoError(t, mgr.RemoveCivoOrganization("my-org"))

	has, err := mgr.HasCivoOrganization("my-org")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestCivo_RemoveOrganization_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.RemoveCivoOrganization("nonexistent"))
}

func TestCivo_GetOrganization_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetCivoOrganization("nonexistent")
	assert.Error(t, err)
}

// ========== Civo Networks ==========

func TestCivo_AddAndGetNetwork(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	require.NoError(t, mgr.AddCivoNetwork("my-org", "default", "net-abc123"))

	id, err := mgr.GetCivoNetworkID("my-org", "default")
	require.NoError(t, err)
	assert.Equal(t, "net-abc123", id)
}

func TestCivo_UpdateNetwork(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	require.NoError(t, mgr.AddCivoNetwork("my-org", "default", "net-old"))
	require.NoError(t, mgr.AddCivoNetwork("my-org", "default", "net-new"))

	id, err := mgr.GetCivoNetworkID("my-org", "default")
	require.NoError(t, err)
	assert.Equal(t, "net-new", id)
}

func TestCivo_ListNetworks(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	require.NoError(t, mgr.AddCivoNetwork("my-org", "net-1", "id-1"))
	require.NoError(t, mgr.AddCivoNetwork("my-org", "net-2", "id-2"))

	networks, err := mgr.ListCivoNetworks("my-org")
	require.NoError(t, err)
	assert.Len(t, networks, 2)
}

func TestCivo_RemoveNetwork(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	require.NoError(t, mgr.AddCivoNetwork("my-org", "default", "net-abc123"))
	require.NoError(t, mgr.RemoveCivoNetwork("my-org", "default"))

	_, err := mgr.GetCivoNetworkID("my-org", "default")
	assert.Error(t, err)
}

func TestCivo_Network_OrgNotFound(t *testing.T) {
	mgr := newTestManager(t)
	assert.Error(t, mgr.AddCivoNetwork("nonexistent", "net", "id"))
	_, err := mgr.GetCivoNetworkID("nonexistent", "net")
	assert.Error(t, err)
}

// ========== Config persistence ==========

func TestConfigPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	require.NoError(t, mgr.AddGCPProject("dev", "my-dev-project"))

	// New manager with same path reads persisted config
	mgr2 := NewManager(tmpDir)
	id, err := mgr2.GetGCPProjectID("dev")
	require.NoError(t, err)
	assert.Equal(t, "my-dev-project", id)
}

func TestConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	require.NoError(t, mgr.AddGCPProject("dev", "my-dev-project"))

	assert.DirExists(t, filepath.Join(tmpDir, ProviderConfigDir))
}
