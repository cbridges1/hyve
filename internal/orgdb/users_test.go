package orgdb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsers_CRUD(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { testUsersCRUD(t, openTestSQLite(t)) })
	t.Run("postgres", func(t *testing.T) { testUsersCRUD(t, openTestPostgres(t)) })
}

func TestEnsureUsersFromBindings(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { testEnsureUsersFromBindings(t, openTestSQLite(t)) })
	t.Run("postgres", func(t *testing.T) { testEnsureUsersFromBindings(t, openTestPostgres(t)) })
}

func testUsersCRUD(t *testing.T, s *Store) {
	ctx := t.Context()
	var err error

	u, err := s.CreateUser(ctx, User{Username: "alice", Email: ptr("alice@example.com"), PasswordHash: ptr("h1")})
	require.NoError(t, err)
	_, err = s.CreateUser(ctx, User{Username: "alice"})
	assert.Error(t, err, "usernames are unique")
	_, err = s.CreateUser(ctx, User{Username: "alice2", Email: ptr("alice@example.com")})
	assert.Error(t, err, "emails are unique")

	got, err := s.GetUserByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	require.NoError(t, s.SetUserPassword(ctx, u.ID, "h2"))
	require.NoError(t, s.SetUserEmail(ctx, u.ID, nil))
	got, err = s.GetUserByUsername(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, "h2", *got.PasswordHash)
	assert.Nil(t, got.Email)

	require.NoError(t, s.DeleteUser(ctx, u.ID))
	_, err = s.GetUserByUsername(ctx, "alice")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestEnsureUsersFromBindings: per-organization accounts with the same
// username merge into one user with the newest binding's password, the
// newest free email wins, and a second run changes nothing.
func testEnsureUsersFromBindings(t *testing.T, s *Store) {
	ctx := t.Context()
	var err error

	mk := func(ns, identity string, hash, email *string) {
		_, err := s.CreateBinding(ctx, Binding{
			Namespace: ns, SubjectType: SubjectTypeLocal, Identity: identity, Role: "admin",
			ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: ns, PasswordHash: hash, Email: email,
		})
		require.NoError(t, err)
		time.Sleep(1100 * time.Millisecond) // created_at has one-second resolution in SQLite
	}
	mk("acme", "alice", ptr("old-hash"), ptr("alice@acme.test"))
	mk("branlen", "alice", ptr("new-hash"), nil)
	mk("widget", "bob", ptr("bob-hash"), ptr("alice@acme.test")) // email taken by alice
	_, err = s.CreateBinding(ctx, Binding{Namespace: "acme", SubjectType: "oidc", Identity: "sso-user", Role: "admin", ServiceAccountName: "x", ServiceAccountNamespace: "acme"})
	require.NoError(t, err)

	n, err := s.EnsureUsersFromBindings(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "alice (merged) and bob; OIDC bindings have no local user")

	alice, err := s.GetUserByUsername(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, "new-hash", *alice.PasswordHash, "the most recently created binding's password wins")
	require.NotNil(t, alice.Email)
	assert.Equal(t, "alice@acme.test", *alice.Email)

	bob, err := s.GetUserByUsername(ctx, "bob")
	require.NoError(t, err)
	assert.Nil(t, bob.Email, "an email another user already has isn't carried over")

	memberships, err := s.ListBindingsForIdentity(ctx, SubjectTypeLocal, "alice")
	require.NoError(t, err)
	assert.Len(t, memberships, 2)

	n, err = s.EnsureUsersFromBindings(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "idempotent")
}
