package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"
)

func TestSeedBootstrapAdmin(t *testing.T) {
	store := newTestOrgStore(t)
	ctx := t.Context()

	_, err := SeedBootstrapAdmin(ctx, store, testNamespace, "", "pw")
	assert.Error(t, err, "no superadmin yet and no username is a misconfiguration, not a silent skip")

	_, err = SeedBootstrapAdmin(ctx, store, testNamespace, "admin", "")
	assert.Error(t, err, "no superadmin yet and no password is a misconfiguration, not a silent skip")

	created, err := SeedBootstrapAdmin(ctx, store, testNamespace, "admin", "first-pw")
	require.NoError(t, err)
	assert.True(t, created)

	b, err := store.FindBindingBySubject(ctx, testNamespace, orgdb.SubjectTypeLocal, "admin")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleSuperadmin, b.Role)
	require.NotNil(t, passwordHashOf(t, store, b.Identity))
	assert.True(t, VerifyPassword(*passwordHashOf(t, store, b.Identity), "first-pw"))

	// A restart with different values never touches the existing superadmin.
	created, err = SeedBootstrapAdmin(ctx, store, testNamespace, "someone-else", "second-pw")
	require.NoError(t, err)
	assert.False(t, created)
	b, err = store.FindBindingBySubject(ctx, testNamespace, orgdb.SubjectTypeLocal, "admin")
	require.NoError(t, err)
	assert.True(t, VerifyPassword(*passwordHashOf(t, store, b.Identity), "first-pw"), "password must not be reset on restart")
	_, err = store.FindBindingBySubject(ctx, testNamespace, orgdb.SubjectTypeLocal, "someone-else")
	assert.ErrorIs(t, err, orgdb.ErrNotFound)

	// Once one exists, a missing username or password (e.g. the Secret was
	// removed) is fine.
	created, err = SeedBootstrapAdmin(ctx, store, testNamespace, "admin", "")
	require.NoError(t, err)
	assert.False(t, created)
	created, err = SeedBootstrapAdmin(ctx, store, testNamespace, "", "")
	require.NoError(t, err)
	assert.False(t, created)
}
