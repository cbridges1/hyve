package kubeconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/database"
)

const sampleKubeconfig = `apiVersion: v1
kind: Config
current-context: test-cluster
clusters:
- name: test-cluster
  cluster:
    server: https://test.example.com:6443
    certificate-authority-data: dGVzdA==
contexts:
- name: test-cluster
  context:
    cluster: test-cluster
    user: test-user
users:
- name: test-user
  user:
    token: test-token-abc123
`

func setupTestKubeconfigDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.GetDBWithDir(t.TempDir())
	require.NoError(t, err, "failed to create test database")
	t.Cleanup(func() { db.Close() })
	return db
}

// encryptWithKey is a test helper that encrypts plaintext using a raw AES-GCM key,
// mirroring the production encryption logic so we can simulate old hostname-based ciphertext.
func encryptWithKey(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// --- Manager construction ---

func TestNewManagerWithDB(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	require.NotNil(t, mgr)
	assert.Equal(t, "test-repo", mgr.repositoryName)
}

func TestClose(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")
	assert.NoError(t, mgr.Close())
}

// --- StoreKubeconfig ---

func TestStoreAndGetKubeconfig(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	kc, err := mgr.StoreKubeconfig("my-cluster", sampleKubeconfig)
	require.NoError(t, err)
	require.NotNil(t, kc)
	assert.Equal(t, "my-cluster", kc.ClusterName)
	assert.Equal(t, "test-repo", kc.RepositoryName)

	retrieved, err := mgr.GetKubeconfig("my-cluster")
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	config, err := retrieved.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, sampleKubeconfig, config)
}

func TestStoreKubeconfig_Update(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("my-cluster", sampleKubeconfig)
	require.NoError(t, err)

	updatedConfig := sampleKubeconfig + "# updated\n"
	_, err = mgr.StoreKubeconfig("my-cluster", updatedConfig)
	require.NoError(t, err)

	retrieved, err := mgr.GetKubeconfig("my-cluster")
	require.NoError(t, err)

	config, err := retrieved.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, updatedConfig, config)

	// Should still be only one entry
	list, err := mgr.ListKubeconfigs()
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestStoreKubeconfig_EmptyClusterName(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("", sampleKubeconfig)
	assert.Error(t, err)
}

func TestStoreKubeconfig_EmptyConfig(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("my-cluster", "")
	assert.Error(t, err)
}

// --- GetKubeconfig ---

func TestGetKubeconfig_NotFound(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	kc, err := mgr.GetKubeconfig("nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, kc)
}

// --- ListKubeconfigs ---

func TestListKubeconfigs(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	for _, name := range []string{"cluster-a", "cluster-b", "cluster-c"} {
		_, err := mgr.StoreKubeconfig(name, sampleKubeconfig)
		require.NoError(t, err)
	}

	list, err := mgr.ListKubeconfigs()
	require.NoError(t, err)
	require.Len(t, list, 3)

	names := make([]string, len(list))
	for i, kc := range list {
		names[i] = kc.ClusterName
	}
	assert.Contains(t, names, "cluster-a")
	assert.Contains(t, names, "cluster-b")
	assert.Contains(t, names, "cluster-c")
}

func TestListKubeconfigs_Empty(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	list, err := mgr.ListKubeconfigs()
	require.NoError(t, err)
	assert.Empty(t, list)
}

// --- DeleteKubeconfig ---

func TestDeleteKubeconfig(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("my-cluster", sampleKubeconfig)
	require.NoError(t, err)

	require.NoError(t, mgr.DeleteKubeconfig("my-cluster"))

	kc, err := mgr.GetKubeconfig("my-cluster")
	assert.NoError(t, err)
	assert.Nil(t, kc)
}

func TestDeleteKubeconfig_NonExistent(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	// Deleting a non-existent entry should not error
	assert.NoError(t, mgr.DeleteKubeconfig("nonexistent"))
}

// --- Repository isolation ---

func TestRepositoryIsolation(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgrA := NewManagerWithDB(db, "repo-a")
	mgrB := NewManagerWithDB(db, "repo-b")

	_, err := mgrA.StoreKubeconfig("shared-cluster", sampleKubeconfig)
	require.NoError(t, err)

	// repo-b should not see repo-a's kubeconfig
	kc, err := mgrB.GetKubeconfig("shared-cluster")
	assert.NoError(t, err)
	assert.Nil(t, kc)

	// Lists should be isolated
	listA, err := mgrA.ListKubeconfigs()
	require.NoError(t, err)
	assert.Len(t, listA, 1)

	listB, err := mgrB.ListKubeconfigs()
	require.NoError(t, err)
	assert.Empty(t, listB)

	// Deleting from repo-a should not remove repo-b's entry
	_, err = mgrB.StoreKubeconfig("shared-cluster", sampleKubeconfig)
	require.NoError(t, err)

	require.NoError(t, mgrA.DeleteKubeconfig("shared-cluster"))

	kcB, err := mgrB.GetKubeconfig("shared-cluster")
	assert.NoError(t, err)
	assert.NotNil(t, kcB)
}

// --- CleanupOrphanedKubeconfigs ---

func TestCleanupOrphanedKubeconfigs(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("active-cluster", sampleKubeconfig)
	require.NoError(t, err)
	_, err = mgr.StoreKubeconfig("orphan-cluster", sampleKubeconfig)
	require.NoError(t, err)

	require.NoError(t, mgr.CleanupOrphanedKubeconfigs([]string{"active-cluster"}))

	kc, err := mgr.GetKubeconfig("active-cluster")
	assert.NoError(t, err)
	assert.NotNil(t, kc, "active cluster should remain")

	kc, err = mgr.GetKubeconfig("orphan-cluster")
	assert.NoError(t, err)
	assert.Nil(t, kc, "orphan should be removed")
}

func TestCleanupOrphanedKubeconfigs_AllOrphaned(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("cluster-a", sampleKubeconfig)
	require.NoError(t, err)
	_, err = mgr.StoreKubeconfig("cluster-b", sampleKubeconfig)
	require.NoError(t, err)

	// Empty active list means all are orphaned
	require.NoError(t, mgr.CleanupOrphanedKubeconfigs([]string{}))

	list, err := mgr.ListKubeconfigs()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestCleanupOrphanedKubeconfigs_NoneOrphaned(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.StoreKubeconfig("cluster-a", sampleKubeconfig)
	require.NoError(t, err)
	_, err = mgr.StoreKubeconfig("cluster-b", sampleKubeconfig)
	require.NoError(t, err)

	require.NoError(t, mgr.CleanupOrphanedKubeconfigs([]string{"cluster-a", "cluster-b"}))

	list, err := mgr.ListKubeconfigs()
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

// --- Kubeconfig.GetConfig ---

func TestGetConfig_NoManager(t *testing.T) {
	kc := &Kubeconfig{} // manager is nil
	_, err := kc.GetConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manager not available")
}

// --- Encryption ---

func TestEncryptionRoundtrip(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	original := "sensitive content with special chars: !@#$%^&*()\nnewline\ttab"

	encrypted, err := mgr.encryptConfig(original)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)
	assert.NotEqual(t, original, encrypted)

	decrypted, err := mgr.decryptConfig(encrypted)
	require.NoError(t, err)
	assert.Equal(t, original, decrypted)
}

func TestEncryptionIsNonDeterministic(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	enc1, err := mgr.encryptConfig(sampleKubeconfig)
	require.NoError(t, err)
	enc2, err := mgr.encryptConfig(sampleKubeconfig)
	require.NoError(t, err)

	// Random nonce means same plaintext produces different ciphertext each time
	assert.NotEqual(t, enc1, enc2)
}

func TestEncryptConfig_EmptyString(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	result, err := mgr.encryptConfig("")
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestDecryptConfig_EmptyString(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	result, err := mgr.decryptConfig("")
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestDecryptConfig_InvalidBase64(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	_, err := mgr.decryptConfig("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecryptConfig_TruncatedCiphertext(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	// Valid base64 but payload too short to contain a GCM nonce
	_, err := mgr.decryptConfig(base64.StdEncoding.EncodeToString([]byte("short")))
	assert.Error(t, err)
}

func TestDecryptConfig_WrongKey(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgrA := NewManagerWithDB(db, "repo-a")
	mgrB := NewManagerWithDB(db, "repo-b")

	encrypted, err := mgrA.encryptConfig(sampleKubeconfig)
	require.NoError(t, err)

	// A different repository's key should not decrypt repo-a's data
	_, err = mgrB.decryptConfig(encrypted)
	assert.Error(t, err)
}

// --- MigrateEncryption ---

func TestMigrateEncryption(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	oldHostname := "old-machine.local"

	// Simulate data encrypted with the old hostname-based key by manually inserting it
	oldKey := mgr.getEncryptionKeyWithHostname(oldHostname)
	oldEncrypted, err := encryptWithKey(oldKey, sampleKubeconfig)
	require.NoError(t, err)

	_, err = db.Conn().Exec(
		`INSERT INTO kubeconfigs (cluster_name, repository_name, encrypted_config) VALUES (?, ?, ?)`,
		"migrated-cluster", "test-repo", oldEncrypted,
	)
	require.NoError(t, err)

	// The new key cannot decrypt old data
	kc, err := mgr.GetKubeconfig("migrated-cluster")
	require.NoError(t, err)
	_, decErr := kc.GetConfig()
	assert.Error(t, decErr, "old encrypted data should not be decryptable with new key")

	// After migration it should be decryptable with the new key
	require.NoError(t, mgr.MigrateEncryption(oldHostname))

	kc, err = mgr.GetKubeconfig("migrated-cluster")
	require.NoError(t, err)
	require.NotNil(t, kc)

	config, err := kc.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, sampleKubeconfig, config)
}

func TestMigrateEncryption_NoKubeconfigs(t *testing.T) {
	db := setupTestKubeconfigDB(t)
	mgr := NewManagerWithDB(db, "test-repo")

	err := mgr.MigrateEncryption("some-hostname")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no kubeconfigs found")
}
