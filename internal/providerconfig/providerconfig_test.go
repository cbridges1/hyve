package providerconfig

import (
	"os"
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

// ========== Credential resolution (resolveCredential / env var expansion) ==========

func TestGetCivoToken_LiteralValue(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	// Manually set the token by loading, setting, and saving the config.
	cfg, err := mgr.LoadCivoConfig()
	require.NoError(t, err)
	cfg.Organizations[0].Token = "my-literal-token"
	require.NoError(t, mgr.SaveCivoConfig(cfg))

	token, err := mgr.GetCivoToken("my-org")
	require.NoError(t, err)
	assert.Equal(t, "my-literal-token", token)
}

func TestGetCivoToken_EnvVarReference(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	cfg, err := mgr.LoadCivoConfig()
	require.NoError(t, err)
	cfg.Organizations[0].Token = "${TEST_CIVO_TOKEN}"
	require.NoError(t, mgr.SaveCivoConfig(cfg))

	t.Setenv("TEST_CIVO_TOKEN", "token-from-env")

	token, err := mgr.GetCivoToken("my-org")
	require.NoError(t, err)
	assert.Equal(t, "token-from-env", token)
}

func TestGetCivoToken_EnvVarUnset_ReturnsEmpty(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddCivoOrganization("my-org", "org-123"))
	cfg, err := mgr.LoadCivoConfig()
	require.NoError(t, err)
	cfg.Organizations[0].Token = "${DEFINITELY_NOT_SET_CIVO_VAR}"
	require.NoError(t, mgr.SaveCivoConfig(cfg))

	os.Unsetenv("DEFINITELY_NOT_SET_CIVO_VAR")

	token, err := mgr.GetCivoToken("my-org")
	require.NoError(t, err)
	assert.Empty(t, token)
}

func TestGetCivoToken_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetCivoToken("nonexistent")
	assert.Error(t, err)
}

func TestGetGCPCredentialsJSON_LiteralValue(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddGCPProject("dev", "dev-project-id"))
	cfg, err := mgr.LoadGCPConfig()
	require.NoError(t, err)
	cfg.Projects[0].CredentialsJSON = `{"type":"service_account"}`
	require.NoError(t, mgr.SaveGCPConfig(cfg))

	creds, err := mgr.GetGCPCredentialsJSON("dev")
	require.NoError(t, err)
	assert.Equal(t, `{"type":"service_account"}`, creds)
}

func TestGetGCPCredentialsJSON_EnvVarReference(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddGCPProject("dev", "dev-project-id"))
	cfg, err := mgr.LoadGCPConfig()
	require.NoError(t, err)
	cfg.Projects[0].CredentialsJSON = "${TEST_GCP_CREDS_JSON}"
	require.NoError(t, mgr.SaveGCPConfig(cfg))

	t.Setenv("TEST_GCP_CREDS_JSON", `{"type":"service_account","project_id":"dev"}`)

	creds, err := mgr.GetGCPCredentialsJSON("dev")
	require.NoError(t, err)
	assert.Equal(t, `{"type":"service_account","project_id":"dev"}`, creds)
}

func TestGetGCPCredentialsJSON_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetGCPCredentialsJSON("nonexistent")
	assert.Error(t, err)
}

func TestGetAWSCredentials_LiteralValues(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	cfg, err := mgr.LoadAWSConfig()
	require.NoError(t, err)
	cfg.Accounts[0].AccessKeyID = "AKIAIOSFODNN7EXAMPLE"
	cfg.Accounts[0].SecretAccessKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	cfg.Accounts[0].SessionToken = ""
	require.NoError(t, mgr.SaveAWSConfig(cfg))

	keyID, secret, session, err := mgr.GetAWSCredentials("prod")
	require.NoError(t, err)
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", keyID)
	assert.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", secret)
	assert.Empty(t, session)
}

func TestGetAWSCredentials_EnvVarReferences(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAWSAccount("prod", "123456789012"))
	cfg, err := mgr.LoadAWSConfig()
	require.NoError(t, err)
	cfg.Accounts[0].AccessKeyID = "${TEST_AWS_ACCESS_KEY_ID}"
	cfg.Accounts[0].SecretAccessKey = "${TEST_AWS_SECRET_ACCESS_KEY}"
	cfg.Accounts[0].SessionToken = "${TEST_AWS_SESSION_TOKEN}"
	require.NoError(t, mgr.SaveAWSConfig(cfg))

	t.Setenv("TEST_AWS_ACCESS_KEY_ID", "key-from-env")
	t.Setenv("TEST_AWS_SECRET_ACCESS_KEY", "secret-from-env")
	t.Setenv("TEST_AWS_SESSION_TOKEN", "session-from-env")

	keyID, secret, session, err := mgr.GetAWSCredentials("prod")
	require.NoError(t, err)
	assert.Equal(t, "key-from-env", keyID)
	assert.Equal(t, "secret-from-env", secret)
	assert.Equal(t, "session-from-env", session)
}

func TestGetAWSCredentials_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, _, _, err := mgr.GetAWSCredentials("nonexistent")
	assert.Error(t, err)
}

func TestGetAzureCredentials_LiteralValues(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAzureSubscription("prod", "sub-id-123"))
	cfg, err := mgr.LoadAzureConfig()
	require.NoError(t, err)
	cfg.Subscriptions[0].TenantID = "tenant-abc"
	cfg.Subscriptions[0].ClientID = "client-def"
	cfg.Subscriptions[0].ClientSecret = "secret-ghi"
	require.NoError(t, mgr.SaveAzureConfig(cfg))

	tenantID, clientID, clientSecret, err := mgr.GetAzureCredentials("prod")
	require.NoError(t, err)
	assert.Equal(t, "tenant-abc", tenantID)
	assert.Equal(t, "client-def", clientID)
	assert.Equal(t, "secret-ghi", clientSecret)
}

func TestGetAzureCredentials_EnvVarReferences(t *testing.T) {
	mgr := newTestManager(t)

	require.NoError(t, mgr.AddAzureSubscription("prod", "sub-id-123"))
	cfg, err := mgr.LoadAzureConfig()
	require.NoError(t, err)
	cfg.Subscriptions[0].TenantID = "${TEST_AZURE_TENANT_ID}"
	cfg.Subscriptions[0].ClientID = "${TEST_AZURE_CLIENT_ID}"
	cfg.Subscriptions[0].ClientSecret = "${TEST_AZURE_CLIENT_SECRET}"
	require.NoError(t, mgr.SaveAzureConfig(cfg))

	t.Setenv("TEST_AZURE_TENANT_ID", "tenant-from-env")
	t.Setenv("TEST_AZURE_CLIENT_ID", "client-from-env")
	t.Setenv("TEST_AZURE_CLIENT_SECRET", "secret-from-env")

	tenantID, clientID, clientSecret, err := mgr.GetAzureCredentials("prod")
	require.NoError(t, err)
	assert.Equal(t, "tenant-from-env", tenantID)
	assert.Equal(t, "client-from-env", clientID)
	assert.Equal(t, "secret-from-env", clientSecret)
}

func TestGetAzureCredentials_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, _, _, err := mgr.GetAzureCredentials("nonexistent")
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
