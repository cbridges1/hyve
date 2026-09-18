package orgdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	_, err = s.db.Exec(`TRUNCATE bindings, environments, organizations, reconciling_clusters, signing_keys, sessions, email_settings, password_reset_tokens CASCADE`)
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
		PasswordHash: ptr("$2a$10$fakebcrypthash"),
	})
	require.NoError(t, err)
	require.NotNil(t, binding.EnvironmentID)
	assert.Equal(t, env.ID, *binding.EnvironmentID)
	require.NotNil(t, binding.PasswordHash, "Milestone 10 Part C: a local binding's password hash must round-trip through Store, not just a Kubernetes Secret")
	assert.Equal(t, "$2a$10$fakebcrypthash", *binding.PasswordHash)

	found, err := s.FindBindingBySubject(ctx, "acme", SubjectTypeLocal, "alice")
	require.NoError(t, err)
	assert.Equal(t, "admin", found.Role)
	require.NotNil(t, found.PasswordHash)
	assert.Equal(t, "$2a$10$fakebcrypthash", *found.PasswordHash)

	all, err := s.ListBindingsForScope(ctx, "acme")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "admin", all[0].Role)

	require.NoError(t, s.DeleteBinding(ctx, binding.ID))
	_, err = s.FindBindingBySubject(ctx, "acme", SubjectTypeLocal, "alice")
	assert.ErrorIs(t, err, ErrNotFound)

	rc, err := s.CreateReconcilingCluster(ctx, ReconcilingCluster{
		Name:       "cell-a",
		Kubeconfig: "apiVersion: v1\nkind: Config\n",
	})
	require.NoError(t, err)
	assert.Nil(t, rc.Reachable, "reachability is unknown, not false, before the first health check")

	require.NoError(t, s.SetReconcilingClusterHealth(ctx, rc.ID, true, nil, "v1.31.5+k3s1"))
	rc, err = s.GetReconcilingCluster(ctx, rc.ID)
	require.NoError(t, err)
	require.NotNil(t, rc.Reachable)
	assert.True(t, *rc.Reachable)
	assert.Nil(t, rc.LastError)
	require.NotNil(t, rc.KubernetesVersion)
	assert.Equal(t, "v1.31.5+k3s1", *rc.KubernetesVersion)

	// A failed check (empty version) must not erase the version already
	// observed on a prior successful one — a transient unreachable blip
	// shouldn't wipe out known-good detail.
	require.NoError(t, s.SetReconcilingClusterHealth(ctx, rc.ID, false, errors.New("dial timeout"), ""))
	rc, err = s.GetReconcilingCluster(ctx, rc.ID)
	require.NoError(t, err)
	require.NotNil(t, rc.Reachable)
	assert.False(t, *rc.Reachable)
	require.NotNil(t, rc.KubernetesVersion)
	assert.Equal(t, "v1.31.5+k3s1", *rc.KubernetesVersion, "must survive a failed check")

	byName, err := s.GetReconcilingClusterByName(ctx, "cell-a")
	require.NoError(t, err)
	assert.Equal(t, rc.ID, byName.ID)
	assert.Equal(t, "apiVersion: v1\nkind: Config\n", byName.Kubeconfig, "Milestone 10 Part C: kubeconfig content itself must round-trip through Store")

	require.NoError(t, s.SetReconcilingClusterKubeconfig(ctx, rc.ID, "apiVersion: v1\nkind: Config\n# rotated\n"))
	rotated, err := s.GetReconcilingCluster(ctx, rc.ID)
	require.NoError(t, err)
	assert.Contains(t, rotated.Kubeconfig, "# rotated", "a re-registration must actually rotate the stored kubeconfig content")

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

	// Milestone 10 Part C: hyve-api's own session-signing key.
	_, err = s.GetSigningKeyByNamespace(ctx, "hyve-system")
	assert.ErrorIs(t, err, ErrNotFound, "no signing key exists yet on a fresh database")

	key, err := s.CreateSigningKey(ctx, SigningKey{Namespace: "hyve-system", KeyMaterial: "ZmFrZS1rZXk="})
	require.NoError(t, err)
	assert.NotEmpty(t, key.ID)

	fetchedKey, err := s.GetSigningKeyByNamespace(ctx, "hyve-system")
	require.NoError(t, err)
	assert.Equal(t, key.ID, fetchedKey.ID)
	assert.Equal(t, "ZmFrZS1rZXk=", fetchedKey.KeyMaterial)

	_, err = s.CreateSigningKey(ctx, SigningKey{Namespace: "hyve-system", KeyMaterial: "should-collide"})
	assert.Error(t, err, "UNIQUE(namespace) must actually be enforced by both backends — one signing key per install")

	// Milestone 10 Part D: sessions (replaces the retired HyveSession CRD).
	sess, err := s.CreateSession(ctx, Session{
		Subject: "jbridges", TenantNamespace: "", TokenHash: "deadbeef", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)

	fetchedSess, err := s.GetSession(ctx, sess.ID)
	require.NoError(t, err)
	assert.Equal(t, "jbridges", fetchedSess.Subject)
	assert.Equal(t, "deadbeef", fetchedSess.TokenHash)
	assert.WithinDuration(t, sess.ExpiresAt, fetchedSess.ExpiresAt, time.Second)

	require.NoError(t, s.DeleteSession(ctx, sess.ID))
	_, err = s.GetSession(ctx, sess.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, s.DeleteSession(ctx, sess.ID), "deleting an already-gone session must not error — best-effort, matching handleLogout's own stance")

	// Bulk revocation — HYVE-EMAIL-IMPLEMENTATION-PLAN.md's Milestone 1.
	sessA, err := s.CreateSession(ctx, Session{Subject: "bob", TenantNamespace: "acme", TokenHash: "aaa", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	sessB, err := s.CreateSession(ctx, Session{Subject: "bob", TenantNamespace: "acme", TokenHash: "bbb", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	otherNsSess, err := s.CreateSession(ctx, Session{Subject: "bob", TenantNamespace: "other-tenant", TokenHash: "ccc", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	otherSubjSess, err := s.CreateSession(ctx, Session{Subject: "carol", TenantNamespace: "acme", TokenHash: "ddd", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)

	require.NoError(t, s.DeleteSessionsBySubject(ctx, "bob", "acme"))
	_, err = s.GetSession(ctx, sessA.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.GetSession(ctx, sessB.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.GetSession(ctx, otherNsSess.ID)
	assert.NoError(t, err, "a session for the same subject in a different namespace must survive")
	_, err = s.GetSession(ctx, otherSubjSess.ID)
	assert.NoError(t, err, "a different subject's session in the same namespace must survive")

	// Milestone 10 Part C-successor: install-wide email settings singleton.
	unconfigured, err := s.GetEmailSettings(ctx)
	require.NoError(t, err, "no row yet must not be an error")
	assert.False(t, unconfigured.Configured(), "a zero-value row (never saved) must report unconfigured")

	saved, err := s.UpsertEmailSettings(ctx, EmailSettings{
		SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPUsername: ptr("relay"), SMTPPassword: ptr("s3cret"),
		UseTLS: false, SkipVerify: false, FromAddress: "no-reply@example.com", FromName: ptr("hyve"),
	})
	require.NoError(t, err)
	assert.Equal(t, EmailSettingsID, saved.ID)
	assert.True(t, saved.Configured())
	require.NotNil(t, saved.SMTPPassword)
	assert.Equal(t, "s3cret", *saved.SMTPPassword)

	reFetched, err := s.GetEmailSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, "smtp.example.com", reFetched.SMTPHost)
	assert.Equal(t, 587, reFetched.SMTPPort)

	// A second Upsert must replace the singleton row in place, not create
	// a second one (proves ON CONFLICT actually fires on both backends).
	replaced, err := s.UpsertEmailSettings(ctx, EmailSettings{
		SMTPHost: "smtp2.example.com", SMTPPort: 465, UseTLS: true, SkipVerify: true, FromAddress: "hi@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, "smtp2.example.com", replaced.SMTPHost)
	assert.Nil(t, replaced.SMTPUsername, "omitting a field on the replacing call must actually clear it, not leave the old value behind")

	// Milestone 1: password reset tokens — one live token per binding.
	resetBinding, err := s.CreateBinding(ctx, Binding{
		Namespace: "acme", OrganizationID: &org.ID, EnvironmentID: &env.ID, SubjectType: SubjectTypeLocal,
		Identity: "reset-target", Role: "admin", ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "acme",
	})
	require.NoError(t, err)

	_, err = s.GetPasswordResetTokenByBindingID(ctx, resetBinding.ID)
	assert.ErrorIs(t, err, ErrNotFound, "no token exists yet")

	firstToken, err := s.CreatePasswordResetToken(ctx, PasswordResetToken{
		BindingID: resetBinding.ID, TokenHash: "hash-one", ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, firstToken.ID)

	secondToken, err := s.CreatePasswordResetToken(ctx, PasswordResetToken{
		BindingID: resetBinding.ID, TokenHash: "hash-two", ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	require.NoError(t, err)

	current, err := s.GetPasswordResetTokenByBindingID(ctx, resetBinding.ID)
	require.NoError(t, err)
	assert.Equal(t, secondToken.ID, current.ID, "requesting a new token must replace, not accumulate alongside, the old one")
	assert.Equal(t, "hash-two", current.TokenHash)

	require.NoError(t, s.DeletePasswordResetTokensForBinding(ctx, resetBinding.ID))
	_, err = s.GetPasswordResetTokenByBindingID(ctx, resetBinding.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.DeletePasswordResetTokensForBinding(ctx, resetBinding.ID), "deleting an already-gone token must not error")

	// DeleteBinding must also clean up any live token — no ON DELETE
	// CASCADE backs this (see migrations/*/0005_password_reset_tokens.sql).
	_, err = s.CreatePasswordResetToken(ctx, PasswordResetToken{
		BindingID: resetBinding.ID, TokenHash: "hash-three", ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, s.DeleteBinding(ctx, resetBinding.ID))
	_, err = s.GetPasswordResetTokenByBindingID(ctx, resetBinding.ID)
	assert.ErrorIs(t, err, ErrNotFound, "deleting the binding must delete its outstanding reset token too")
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
