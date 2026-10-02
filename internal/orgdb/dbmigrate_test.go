package orgdb

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrate_SQLiteToPostgres_RoundTrips is Milestone 7's own "Tests"
// requirement: populate one of each row type in a SQLite Store, Migrate it
// into a real Postgres Store, and confirm matching row counts per table
// plus a handful of field-level spot checks (ids preserved, FKs intact).
// Skipped without POSTGRES_TEST_DSN, same convention as store_test.go's
// own openTestPostgres.
func TestMigrate_SQLiteToPostgres_RoundTrips(t *testing.T) {
	ctx := context.Background()
	source, err := Open("sqlite", filepath.Join(t.TempDir(), "orgdb.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { source.Close() })

	dest := openTestPostgres(t)

	rc, err := source.CreateReconcilingCluster(ctx, ReconcilingCluster{Name: "cell-a", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)

	org, env, err := source.CreateOrganizationWithDefaults(ctx, Organization{Name: "acme", Namespace: "acme", ReconcilingClusterID: &rc.ID}, "", "")
	require.NoError(t, err)

	homeOrg, err := source.CreateOrganization(ctx, Organization{Name: "widget", Namespace: "widget"})
	require.NoError(t, err)
	extraEnv, err := source.CreateEnvironment(ctx, Environment{OrganizationID: homeOrg.ID, Name: "staging"})
	require.NoError(t, err)

	hash := "$2a$10$fakebcrypthash"
	binding, err := source.CreateBinding(ctx, Binding{
		Namespace: "acme", OrganizationID: &org.ID, EnvironmentID: &env.ID, SubjectType: SubjectTypeLocal,
		Identity: "alice", Role: "admin", ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "acme",
		PasswordHash: &hash,
	})
	require.NoError(t, err)
	superadmin, err := source.CreateBinding(ctx, Binding{
		Namespace: "hyve-system", SubjectType: SubjectTypeLocal, Identity: "root", Role: "superadmin",
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "hyve-system",
	})
	require.NoError(t, err)

	key, err := source.CreateSigningKey(ctx, SigningKey{Namespace: "hyve-system", KeyMaterial: "ZmFrZS1rZXk="})
	require.NoError(t, err)

	sess, err := source.CreateSession(ctx, Session{
		Subject: "alice", TenantNamespace: "acme", TokenHash: "deadbeef",
		ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	})
	require.NoError(t, err)

	summary, err := Migrate(ctx, source, dest)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.ReconcilingClusters)
	assert.Equal(t, 2, summary.Organizations)
	assert.Equal(t, 2, summary.Environments, "acme's default env + widget's staging env")
	assert.Equal(t, 2, summary.Bindings)
	assert.Equal(t, 1, summary.SigningKeys)
	assert.Equal(t, 1, summary.Sessions)

	destRC, err := dest.GetReconcilingCluster(ctx, rc.ID)
	require.NoError(t, err)
	assert.Equal(t, rc.Kubeconfig, destRC.Kubeconfig)

	destOrg, err := dest.GetOrganization(ctx, org.ID)
	require.NoError(t, err)
	require.NotNil(t, destOrg.ReconcilingClusterID)
	assert.Equal(t, rc.ID, *destOrg.ReconcilingClusterID, "FK to the just-migrated reconciling cluster must still resolve")

	destEnvs, err := dest.ListEnvironments(ctx, homeOrg.ID)
	require.NoError(t, err)
	require.Len(t, destEnvs, 1)
	assert.Equal(t, extraEnv.Name, destEnvs[0].Name)

	destBinding, err := dest.FindBindingBySubject(ctx, "acme", SubjectTypeLocal, "alice")
	require.NoError(t, err)
	assert.Equal(t, binding.ID, destBinding.ID)
	require.NotNil(t, destBinding.PasswordHash)
	assert.Equal(t, hash, *destBinding.PasswordHash)
	require.NotNil(t, destBinding.OrganizationID)
	assert.Equal(t, org.ID, *destBinding.OrganizationID)

	destSuperadmin, err := dest.FindBindingBySubject(ctx, "hyve-system", SubjectTypeLocal, "root")
	require.NoError(t, err)
	assert.Equal(t, superadmin.Role, destSuperadmin.Role)
	assert.Nil(t, destSuperadmin.OrganizationID)

	destKey, err := dest.GetSigningKeyByNamespace(ctx, "hyve-system")
	require.NoError(t, err)
	assert.Equal(t, key.KeyMaterial, destKey.KeyMaterial)

	destSess, err := dest.GetSession(ctx, sess.ID)
	require.NoError(t, err)
	assert.Equal(t, sess.TokenHash, destSess.TokenHash)
}

// TestMigrate_RefusesInFlightReconcilingClusterMigration proves the safety
// check: a source organization mid-PATCH-/organizations migration
// (reconciling_cluster_migration_status set) must abort the whole database
// migration cleanly rather than copying it into an inconsistent state.
func TestMigrate_RefusesInFlightReconcilingClusterMigration(t *testing.T) {
	ctx := context.Background()
	source := openTestSQLite(t)
	dest := openTestPostgres(t)

	org, err := source.CreateOrganization(ctx, Organization{Name: "acme", Namespace: "acme"})
	require.NoError(t, err)
	migrating := "migrating"
	require.NoError(t, source.SetOrganizationMigrationStatus(ctx, org.ID, &migrating))

	_, err = Migrate(ctx, source, dest)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "in-flight reconciling-cluster migration")
}

// TestMigrate_RefusesNonEmptyDestination proves the other safety check: a
// destination that already has data is refused outright, rather than
// silently duplicating or partially failing partway through.
func TestMigrate_RefusesNonEmptyDestination(t *testing.T) {
	ctx := context.Background()
	source := openTestSQLite(t)
	dest := openTestPostgres(t)

	_, err := dest.CreateOrganization(ctx, Organization{Name: "already-here", Namespace: "already-here"})
	require.NoError(t, err)

	_, err = Migrate(ctx, source, dest)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to migrate into a non-empty database")
}
