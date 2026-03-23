package credentials

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/database"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.GetDBWithDir(t.TempDir())
	require.NoError(t, err, "Failed to create test database")
	t.Cleanup(func() { db.Close() })
	return db
}

// TestStoreAndGetCredentials tests storing and retrieving credentials
func TestStoreAndGetCredentials(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	creds, err := mgr.StoreCredentials("testuser", "testpassword123")
	require.NoError(t, err)
	assert.Equal(t, "testuser", creds.Username)

	password, err := creds.GetPassword()
	require.NoError(t, err)
	assert.Equal(t, "testpassword123", password)

	retrieved, err := mgr.GetCredentials()
	require.NoError(t, err)
	assert.Equal(t, creds.Username, retrieved.Username)
}

// TestUpdateCredentials tests updating existing credentials
func TestUpdateCredentials(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.StoreCredentials("user1", "password1")
	require.NoError(t, err)

	updated, err := mgr.StoreCredentials("user2", "password2")
	require.NoError(t, err)
	assert.Equal(t, "user2", updated.Username)

	password, err := updated.GetPassword()
	require.NoError(t, err)
	assert.Equal(t, "password2", password)
}

// TestClearCredentials tests clearing credentials
func TestClearCredentials(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.StoreCredentials("testuser", "testpassword")
	require.NoError(t, err)

	hasCreds, err := mgr.HasCredentials()
	require.NoError(t, err)
	assert.True(t, hasCreds)

	err = mgr.ClearCredentials()
	require.NoError(t, err)

	hasCredsAfter, err := mgr.HasCredentials()
	require.NoError(t, err)
	assert.False(t, hasCredsAfter)
}

// TestStoreAndGetCivoToken tests Civo token storage and retrieval
func TestStoreAndGetCivoToken(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	token := "test-civo-token-123"
	err := mgr.StoreCivoToken("myorg", token)
	require.NoError(t, err)

	retrievedToken, err := mgr.GetCivoToken("myorg")
	require.NoError(t, err)
	assert.Equal(t, token, retrievedToken)
}

// TestUpdateCivoToken tests updating an existing Civo token
func TestUpdateCivoToken(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	err := mgr.StoreCivoToken("myorg", "old-token")
	require.NoError(t, err)

	err = mgr.StoreCivoToken("myorg", "new-token")
	require.NoError(t, err)

	retrievedToken, err := mgr.GetCivoToken("myorg")
	require.NoError(t, err)
	assert.Equal(t, "new-token", retrievedToken)
}

// TestClearCivoToken tests removing a Civo token
func TestClearCivoToken(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	err := mgr.StoreCivoToken("myorg", "test-token")
	require.NoError(t, err)

	hasToken, err := mgr.HasCivoToken("myorg")
	require.NoError(t, err)
	assert.True(t, hasToken)

	err = mgr.ClearCivoToken("myorg")
	require.NoError(t, err)

	hasTokenAfter, err := mgr.HasCivoToken("myorg")
	require.NoError(t, err)
	assert.False(t, hasTokenAfter)
}

// TestEncryptionDecryption tests that encryption/decryption works correctly
func TestEncryptionDecryption(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	testPasswords := []string{
		"simple",
		"with spaces and symbols !@#$%^&*()",
		"unicode: 你好世界 🌍",
		"very-long-password-that-exceeds-typical-lengths-" +
			"and-contains-many-different-characters-1234567890",
	}

	for _, password := range testPasswords {
		t.Run(password, func(t *testing.T) {
			encrypted, err := mgr.encryptPassword(password)
			require.NoError(t, err)

			decrypted, err := mgr.decryptPassword(encrypted)
			require.NoError(t, err)
			assert.Equal(t, password, decrypted)
		})
	}
}

// TestEmptyValues tests handling of empty values
func TestEmptyValues(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.StoreCredentials("", "password")
	assert.Error(t, err, "Expected error for empty username")

	_, err = mgr.StoreCredentials("username", "")
	assert.Error(t, err, "Expected error for empty password")

	err = mgr.StoreCivoToken("myorg", "")
	assert.Error(t, err, "Expected error for empty token")
}

// TestGetNonExistentCivoToken tests retrieving a token that doesn't exist
func TestGetNonExistentCivoToken(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	token, err := mgr.GetCivoToken("myorg")
	require.NoError(t, err)
	assert.Empty(t, token)

	hasToken, err := mgr.HasCivoToken("myorg")
	require.NoError(t, err)
	assert.False(t, hasToken)
}

// TestMultipleSecrets tests storing multiple named secrets
func TestMultipleSecrets(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	type secretEntry struct {
		secretType string
		value      string
	}
	secrets := map[string]secretEntry{
		"myorg-token":  {SecretTypeCivo, "civo-token-123"},
		"docker-token": {"docker", "docker-token-456"},
		"github-token": {"github", "github-token-789"},
	}

	for name, entry := range secrets {
		err := mgr.StoreSecret(name, entry.secretType, entry.value)
		require.NoError(t, err)
	}

	for name, entry := range secrets {
		value, err := mgr.GetSecret(name, entry.secretType)
		require.NoError(t, err)
		assert.Equal(t, entry.value, value)

		hasSecret, err := mgr.HasSecret(name, entry.secretType)
		require.NoError(t, err)
		assert.True(t, hasSecret)

		wrongValue, _ := mgr.GetSecret(name, "wrong-type")
		assert.Empty(t, wrongValue)
	}

	err := mgr.ClearSecret("docker-token", "docker")
	require.NoError(t, err)

	hasDocker, _ := mgr.HasSecret("docker-token", "docker")
	assert.False(t, hasDocker)

	hasCivo, _ := mgr.HasSecret("myorg-token", SecretTypeCivo)
	assert.True(t, hasCivo)

	hasGithub, _ := mgr.HasSecret("github-token", "github")
	assert.True(t, hasGithub)
}

// TestStoreSecretValidation tests validation of secret storage
func TestStoreSecretValidation(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	err := mgr.StoreSecret("", SecretTypeCivo, "value")
	assert.Error(t, err, "Expected error for empty secret name")

	err = mgr.StoreSecret("myorg-token", SecretTypeCivo, "")
	assert.Error(t, err, "Expected error for empty secret value")
}

// TestDatabasePersistence tests that data persists across manager instances
func TestDatabasePersistence(t *testing.T) {
	tempDir := t.TempDir()

	db1, err := database.GetDBWithDir(tempDir)
	require.NoError(t, err, "Failed to create first database")

	mgr1 := NewManagerWithDB(db1)
	err = mgr1.StoreCivoToken("myorg", "persistent-token")
	require.NoError(t, err)
	db1.Close()

	db2, err := database.GetDBWithDir(tempDir)
	require.NoError(t, err, "Failed to create second database")
	defer db2.Close()

	mgr2 := NewManagerWithDB(db2)
	token, err := mgr2.GetCivoToken("myorg")
	require.NoError(t, err)
	assert.Equal(t, "persistent-token", token)

	_ = filepath.Join(tempDir, "hyve.db") // acknowledge expected DB path
}

// BenchmarkEncryption benchmarks the encryption operation
func BenchmarkEncryption(b *testing.B) {
	db, err := database.GetDBWithDir(b.TempDir())
	if err != nil {
		b.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	mgr := NewManagerWithDB(db)
	password := "benchmark-test-password-12345"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mgr.encryptPassword(password)
		if err != nil {
			b.Fatalf("Encryption failed: %v", err)
		}
	}
}

// BenchmarkDecryption benchmarks the decryption operation
func BenchmarkDecryption(b *testing.B) {
	db, err := database.GetDBWithDir(b.TempDir())
	if err != nil {
		b.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	mgr := NewManagerWithDB(db)
	password := "benchmark-test-password-12345"
	encrypted, err := mgr.encryptPassword(password)
	if err != nil {
		b.Fatalf("Failed to encrypt: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mgr.decryptPassword(encrypted)
		if err != nil {
			b.Fatalf("Decryption failed: %v", err)
		}
	}
}
