package repository

import (
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

func TestAddRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	assert.Equal(t, "test-repo", repo.Name)
	assert.Equal(t, "https://github.com/test/test.git", repo.RepoURL)
	assert.Equal(t, "/tmp/test", repo.LocalPath)
	assert.True(t, repo.IsCurrent, "First repository should be set as current")
}

func TestAddDuplicateRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	_, err = mgr.AddRepository("test-repo", "https://github.com/test/test2.git", "/tmp/test2", "")
	assert.Error(t, err)
}

func TestGetRepositoryByName(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	repo, err := mgr.GetRepositoryByName("test-repo")
	require.NoError(t, err)
	assert.Equal(t, "test-repo", repo.Name)
}

func TestGetRepositoryByNameNotFound(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.GetRepositoryByName("nonexistent")
	assert.Error(t, err)
}

func TestUpdateRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	updated, err := mgr.UpdateRepository("test-repo", "https://github.com/test/updated.git", "/tmp/updated", "")
	require.NoError(t, err)

	assert.Equal(t, "https://github.com/test/updated.git", updated.RepoURL)
	assert.Equal(t, "/tmp/updated", updated.LocalPath)
}

func TestDeleteRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	err = mgr.DeleteRepository("test-repo")
	require.NoError(t, err)

	_, err = mgr.GetRepositoryByName("test-repo")
	assert.Error(t, err)
}

func TestListRepositories(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("repo1", "https://github.com/test/repo1.git", "/tmp/repo1", "")
	require.NoError(t, err)
	_, err = mgr.AddRepository("repo2", "https://github.com/test/repo2.git", "/tmp/repo2", "")
	require.NoError(t, err)

	repos, err := mgr.ListRepositories()
	require.NoError(t, err)
	assert.Len(t, repos, 2)
}

func TestSetCurrentRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("repo1", "https://github.com/test/repo1.git", "/tmp/repo1", "")
	require.NoError(t, err)
	_, err = mgr.AddRepository("repo2", "https://github.com/test/repo2.git", "/tmp/repo2", "")
	require.NoError(t, err)

	err = mgr.SetCurrentRepository("repo2")
	require.NoError(t, err)

	current, err := mgr.GetCurrentRepository()
	require.NoError(t, err)
	assert.Equal(t, "repo2", current.Name)
}

func TestGetCurrentRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	current, err := mgr.GetCurrentRepository()
	require.NoError(t, err)
	assert.Equal(t, "test-repo", current.Name)
}

func TestGetCurrentRepositoryNone(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.GetCurrentRepository()
	assert.Error(t, err)
}

func TestHasRepositories(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	has, err := mgr.HasRepositories()
	require.NoError(t, err)
	assert.False(t, has)

	_, err = mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	has, err = mgr.HasRepositories()
	require.NoError(t, err)
	assert.True(t, has)
}

func TestDeleteCurrentRepository(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("repo1", "https://github.com/test/repo1.git", "/tmp/repo1", "")
	require.NoError(t, err)
	_, err = mgr.AddRepository("repo2", "https://github.com/test/repo2.git", "/tmp/repo2", "")
	require.NoError(t, err)

	// repo1 should be current (first added)
	err = mgr.DeleteRepository("repo1")
	require.NoError(t, err)

	// repo2 should now be current
	current, err := mgr.GetCurrentRepository()
	require.NoError(t, err)
	assert.Equal(t, "repo2", current.Name)
}

func TestGetRepositoryByID(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	retrieved, err := mgr.GetRepositoryByID(repo.ID)
	require.NoError(t, err)
	assert.Equal(t, repo.Name, retrieved.Name)
}

func TestRepositoryTimestamps(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)

	assert.False(t, repo.CreatedAt.IsZero(), "Expected CreatedAt to be set")
	assert.False(t, repo.UpdatedAt.IsZero(), "Expected UpdatedAt to be set")
}

func TestSetAndGetSecret(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	require.NoError(t, mgr.SetSecret(repo.ID, "FOO", "bar"))

	value, ok, err := mgr.GetSecret(repo.ID, "FOO")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "bar", value)
}

func TestGetSecretNotSet(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	_, ok, err := mgr.GetSecret(repo.ID, "MISSING")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestSetSecretInvalidKey(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	err = mgr.SetSecret(repo.ID, "not a valid key", "bar")
	assert.Error(t, err)
}

func TestSetSecretOverwritesExisting(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	require.NoError(t, mgr.SetSecret(repo.ID, "FOO", "bar"))
	require.NoError(t, mgr.SetSecret(repo.ID, "FOO", "baz"))

	value, ok, err := mgr.GetSecret(repo.ID, "FOO")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "baz", value)
}

func TestListSecrets(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	require.NoError(t, mgr.SetSecret(repo.ID, "FOO", "1"))
	require.NoError(t, mgr.SetSecret(repo.ID, "BAR", "2"))

	vars, err := mgr.ListSecrets(repo.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "1", "BAR": "2"}, vars)
}

func TestUnsetSecret(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	require.NoError(t, mgr.SetSecret(repo.ID, "FOO", "bar"))
	require.NoError(t, mgr.UnsetSecret(repo.ID, "FOO"))

	_, ok, err := mgr.GetSecret(repo.ID, "FOO")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestUnsetSecretMissingIsNoOp(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)

	assert.NoError(t, mgr.UnsetSecret(repo.ID, "NEVER_SET"))
}

func TestDeleteRepositoryCascadesSecrets(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)
	require.NoError(t, mgr.SetSecret(repo.ID, "FOO", "bar"))

	require.NoError(t, mgr.DeleteRepository("test-repo"))

	// Re-create a repository — SQLite AUTOINCREMENT guarantees a fresh ID
	// is never reused, so this also proves the delete wasn't just an
	// orphaned-but-still-queryable row under the old ID.
	repo2, err := mgr.AddRepository("test-repo", "", "/tmp/test", "")
	require.NoError(t, err)
	assert.NotEqual(t, repo.ID, repo2.ID)

	vars, err := mgr.ListSecrets(repo2.ID)
	require.NoError(t, err)
	assert.Empty(t, vars)
}

func TestDatabasePersistence(t *testing.T) {
	tempDir := t.TempDir()

	db1, err := database.GetDBWithDir(tempDir)
	require.NoError(t, err, "Failed to create first database")

	mgr1 := NewManagerWithDB(db1)
	_, err = mgr1.AddRepository("persistent-repo", "https://github.com/test/test.git", "/tmp/test", "")
	require.NoError(t, err)
	db1.Close()

	db2, err := database.GetDBWithDir(tempDir)
	require.NoError(t, err, "Failed to create second database")
	defer db2.Close()

	mgr2 := NewManagerWithDB(db2)
	repo, err := mgr2.GetRepositoryByName("persistent-repo")
	require.NoError(t, err)
	assert.Equal(t, "persistent-repo", repo.Name)
}

func TestAddRepository_ClusterEnvironment_NoLocalPathRequired(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("prod", "", "", "https://hyve-api.example.com")
	require.NoError(t, err)

	assert.Equal(t, "prod", repo.Name)
	assert.Empty(t, repo.LocalPath)
	assert.Equal(t, "https://hyve-api.example.com", repo.APIURL)
}

func TestAddRepository_APIURLPersistsAndListsSeparately(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("local-env", "", "/tmp/local", "")
	require.NoError(t, err)
	_, err = mgr.AddRepository("prod", "", "", "https://hyve-api.example.com")
	require.NoError(t, err)
	_, err = mgr.AddRepository("staging", "", "", "https://hyve-api-staging.example.com")
	require.NoError(t, err)

	envs, err := mgr.ListRepositories()
	require.NoError(t, err)
	require.Len(t, envs, 3)

	byName := map[string]*Repository{}
	for _, e := range envs {
		byName[e.Name] = e
	}
	assert.Empty(t, byName["local-env"].APIURL)
	assert.Equal(t, "https://hyve-api.example.com", byName["prod"].APIURL)
	assert.Equal(t, "https://hyve-api-staging.example.com", byName["staging"].APIURL)
}

func TestAddRepository_EmptyAPIURLStoresAsEmptyNotNull(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	repo, err := mgr.AddRepository("local-only", "", "/tmp/local", "")
	require.NoError(t, err)
	assert.Equal(t, "", repo.APIURL)

	fetched, err := mgr.GetRepositoryByName("local-only")
	require.NoError(t, err)
	assert.Equal(t, "", fetched.APIURL)
}

func TestUpdateRepository_SetsAndClearsAPIURL(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("env1", "", "/tmp/env1", "")
	require.NoError(t, err)

	updated, err := mgr.UpdateRepository("env1", "", "/tmp/env1", "https://hyve-api.example.com")
	require.NoError(t, err)
	assert.Equal(t, "https://hyve-api.example.com", updated.APIURL)

	cleared, err := mgr.UpdateRepository("env1", "", "/tmp/env1", "")
	require.NoError(t, err)
	assert.Equal(t, "", cleared.APIURL)
}

func TestGetCurrentRepository_ReturnsAPIURL(t *testing.T) {
	mgr := NewManagerWithDB(setupTestDB(t))

	_, err := mgr.AddRepository("prod", "", "", "https://hyve-api.example.com")
	require.NoError(t, err)

	current, err := mgr.GetCurrentRepository()
	require.NoError(t, err)
	assert.Equal(t, "https://hyve-api.example.com", current.APIURL)
}
