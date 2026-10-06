package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// createAccount is how these tests seed a loginable account: b as a
// membership, plus — for a local subject — the user its PasswordHash and
// Email describe (credentials live on the user now, not the binding). An
// email another user already has is left off rather than failing.
func createAccount(ctx context.Context, store *orgdb.Store, b orgdb.Binding) (orgdb.Binding, error) {
	hash, email := b.PasswordHash, b.Email
	b.PasswordHash, b.Email = nil, nil
	created, err := store.CreateBinding(ctx, b)
	if err != nil || (b.SubjectType != "" && b.SubjectType != orgdb.SubjectTypeLocal) {
		return created, err
	}
	if email != nil {
		if _, err := store.GetUserByEmail(ctx, *email); err == nil {
			email = nil
		}
	}
	_, _, err = store.EnsureUser(ctx, b.Identity, hash, email)
	return created, err
}

// passwordHashOf and emailOf read username's credentials from their user.
func passwordHashOf(t *testing.T, store *orgdb.Store, username string) *string {
	t.Helper()
	u, err := store.GetUserByUsername(t.Context(), username)
	require.NoError(t, err)
	return u.PasswordHash
}

func emailOf(t *testing.T, store *orgdb.Store, username string) *string {
	t.Helper()
	u, err := store.GetUserByUsername(t.Context(), username)
	require.NoError(t, err)
	return u.Email
}

func userIDOf(t *testing.T, store *orgdb.Store, username string) string {
	t.Helper()
	u, err := store.GetUserByUsername(t.Context(), username)
	require.NoError(t, err)
	return u.ID
}
