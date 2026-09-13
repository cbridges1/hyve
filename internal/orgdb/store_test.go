package orgdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestSQLite(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "orgdb.sqlite")
	s, err := Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// openTestPostgres opens a Store against a real local Postgres for tests
// that want to prove both dialects behave identically — skipped (not
// failed) when POSTGRES_TEST_DSN isn't set, mirroring
// internal/api/agent_proxy_rbac_test.go's own envtest-skip precedent for
// a comparable "needs real infra this dev machine might not have" case.
func openTestPostgres(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — skipping (see internal/orgdb's own test helper)")
	}
	s, err := Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// Unlike SQLite's fresh-temp-file-per-test isolation, a real Postgres
	// given via POSTGRES_TEST_DSN is a persistent, shared database across
	// runs — truncate before each test so leftover rows from a prior run
	// (or a prior test in this same run) can't collide with this one's
	// fixed fixture names/unique constraints.
	_, err = s.db.Exec(`TRUNCATE bindings, environments, organizations, reconciling_clusters CASCADE`)
	require.NoError(t, err)

	return s
}

func TestOpen_AppliesMigrationsIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orgdb.sqlite")
	s1, err := Open("sqlite", dbPath)
	require.NoError(t, err)
	s1.Close()

	s2, err := Open("sqlite", dbPath)
	require.NoError(t, err, "re-opening an already-migrated database must not error")
	s2.Close()
}

func testCRUDRoundTrip(t *testing.T, s *Store) {
	ctx := context.Background()

	org, err := s.CreateOrganization(ctx, Organization{Name: "acme", Namespace: "acme"})
	require.NoError(t, err)
	assert.NotEmpty(t, org.ID)
	assert.False(t, org.PendingDeletion)
	assert.Nil(t, org.ReconcilingClusterID, "a fresh organization has no reconciling cluster override")

	fetched, err := s.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, org.ID, fetched.ID)

	_, err = s.GetOrganization(ctx, "does-not-exist")
	assert.ErrorIs(t, err, ErrNotFound)

	env, err := s.CreateEnvironment(ctx, Environment{OrganizationID: org.ID, Name: DefaultEnvironmentName})
	require.NoError(t, err)
	assert.Equal(t, org.ID, env.OrganizationID)

	// UNIQUE(organization_id, name) must actually be enforced by both
	// backends, not just documented in the migration file's comment.
	_, err = s.CreateEnvironment(ctx, Environment{OrganizationID: org.ID, Name: DefaultEnvironmentName})
	assert.Error(t, err, "a second environment with the same name in the same org must be rejected")

	binding, err := s.CreateBinding(ctx, Binding{
		Namespace: "acme", OrganizationID: &org.ID, EnvironmentID: &env.ID, SubjectType: SubjectTypeLocal,
		Identity: "alice", Role: "admin", ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "acme",
	})
	require.NoError(t, err)
	require.NotNil(t, binding.EnvironmentID)
	assert.Equal(t, env.ID, *binding.EnvironmentID)

	found, err := s.FindBindingBySubject(ctx, "acme", SubjectTypeLocal, "alice")
	require.NoError(t, err)
	assert.Equal(t, "admin", found.Role)

	all, err := s.ListBindingsForScope(ctx, "acme")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "admin", all[0].Role)

	require.NoError(t, s.DeleteBinding(ctx, binding.ID))
	_, err = s.FindBindingBySubject(ctx, "acme", SubjectTypeLocal, "alice")
	assert.ErrorIs(t, err, ErrNotFound)

	rc, err := s.CreateReconcilingCluster(ctx, ReconcilingCluster{
		Name:                      "cell-a",
		KubeconfigSecretNamespace: "hyve-system",
		KubeconfigSecretName:      "cell-a-kubeconfig",
	})
	require.NoError(t, err)
	assert.Nil(t, rc.Reachable, "reachability is unknown, not false, before the first health check")

	require.NoError(t, s.SetReconcilingClusterHealth(ctx, rc.ID, true, nil))
	rc, err = s.GetReconcilingCluster(ctx, rc.ID)
	require.NoError(t, err)
	require.NotNil(t, rc.Reachable)
	assert.True(t, *rc.Reachable)
	assert.Nil(t, rc.LastError)

	byName, err := s.GetReconcilingClusterByName(ctx, "cell-a")
	require.NoError(t, err)
	assert.Equal(t, rc.ID, byName.ID)

	list, err := s.ListReconcilingClusters(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "cell-a", list[0].Name)

	homeOrgs, err := s.ListOrganizationsByReconcilingCluster(ctx, nil)
	require.NoError(t, err)
	require.Len(t, homeOrgs, 1, "the fresh org created above has no reconciling cluster, so it's on the home cluster")
	assert.Equal(t, org.ID, homeOrgs[0].ID)

	cellOrgs, err := s.ListOrganizationsByReconcilingCluster(ctx, &rc.ID)
	require.NoError(t, err)
	assert.Empty(t, cellOrgs, "nothing has been migrated to cell-a yet")

	hasAny, err := s.AnyOrganizationHasReconcilingCluster(ctx)
	require.NoError(t, err)
	assert.False(t, hasAny)

	require.NoError(t, s.SetOrganizationMigrationStatus(ctx, org.ID, ptr("migrating")))
	migrating, err := s.GetOrganization(ctx, org.ID)
	require.NoError(t, err)
	require.NotNil(t, migrating.ReconcilingClusterMigrationStatus)
	assert.Equal(t, "migrating", *migrating.ReconcilingClusterMigrationStatus)

	require.NoError(t, s.SetOrganizationReconcilingCluster(ctx, org.ID, &rc.ID))
	moved, err := s.GetOrganization(ctx, org.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ReconcilingClusterID)
	assert.Equal(t, rc.ID, *moved.ReconcilingClusterID)
	assert.Nil(t, moved.ReconcilingClusterMigrationStatus, "flipping the reconciling cluster must clear the migration lock in the same write")

	hasAny, err = s.AnyOrganizationHasReconcilingCluster(ctx)
	require.NoError(t, err)
	assert.True(t, hasAny, "the deployment gate must see the org that was just moved off the home cluster")

	cellOrgsAfterMove, err := s.ListOrganizationsByReconcilingCluster(ctx, &rc.ID)
	require.NoError(t, err)
	require.Len(t, cellOrgsAfterMove, 1)
	assert.Equal(t, org.ID, cellOrgsAfterMove[0].ID)

	require.NoError(t, s.SetOrganizationReconcilingCluster(ctx, org.ID, nil))
	movedBack, err := s.GetOrganization(ctx, org.ID)
	require.NoError(t, err)
	assert.Nil(t, movedBack.ReconcilingClusterID, "moving back to nil (the home cluster) must actually clear the FK, not just leave it stale")
}

func ptr(s string) *string { return &s }

func TestCRUDRoundTrip_SQLite(t *testing.T) {
	testCRUDRoundTrip(t, openTestSQLite(t))
}

func TestCRUDRoundTrip_Postgres(t *testing.T) {
	testCRUDRoundTrip(t, openTestPostgres(t))
}

func TestCreateBinding_RequiresEnvironmentID(t *testing.T) {
	s := openTestSQLite(t)
	ctx := context.Background()

	org, err := s.CreateOrganization(ctx, Organization{Name: "acme", Namespace: "acme"})
	require.NoError(t, err)

	_, err = s.CreateBinding(ctx, Binding{Namespace: "acme", OrganizationID: &org.ID, Identity: "alice", Role: "admin"})
	assert.Error(t, err, "an organization-scoped binding with no resolved environment must be rejected, not silently treated as org-wide")
}

// TestCreateBinding_SuperadminNeedsNoOrganization proves the one exception:
// a superadmin binding has no organization or environment at all — just a
// namespace, exactly like any other binding.
func TestCreateBinding_SuperadminNeedsNoOrganization(t *testing.T) {
	s := openTestSQLite(t)
	ctx := context.Background()

	b, err := s.CreateBinding(ctx, Binding{
		Namespace: "hyve-system", SubjectType: SubjectTypeLocal, Identity: "root", Role: "superadmin",
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "hyve-system",
	})
	require.NoError(t, err)
	assert.Nil(t, b.OrganizationID)
	assert.Nil(t, b.EnvironmentID)

	found, err := s.FindBindingBySubject(ctx, "hyve-system", SubjectTypeLocal, "root")
	require.NoError(t, err)
	assert.Equal(t, "superadmin", found.Role)
}

// TestFindBindingBySubject_DifferentNamespacesNoOrganization_StayIsolated is
// the direct regression test for the cross-tenant leak this whole revision
// exists to close: two different namespaces, *neither* with a registered
// Organization, must never share an identity/binding scope just because
// both resolve to nil organization_id/environment_id.
func TestFindBindingBySubject_DifferentNamespacesNoOrganization_StayIsolated(t *testing.T) {
	s := openTestSQLite(t)
	ctx := context.Background()

	_, err := s.CreateBinding(ctx, Binding{
		Namespace: "tenant-a", SubjectType: SubjectTypeLocal, Identity: "cedric", Role: "admin",
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "tenant-a",
	})
	require.NoError(t, err)
	_, err = s.CreateBinding(ctx, Binding{
		Namespace: "tenant-b", SubjectType: SubjectTypeLocal, Identity: "someone-else", Role: "admin",
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "tenant-b",
	})
	require.NoError(t, err)

	_, err = s.FindBindingBySubject(ctx, "tenant-a", SubjectTypeLocal, "someone-else")
	assert.ErrorIs(t, err, ErrNotFound, "tenant-b's binding must not be visible from tenant-a's namespace")

	found, err := s.FindBindingBySubject(ctx, "tenant-a", SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	assert.Equal(t, "tenant-a", found.Namespace)
}
