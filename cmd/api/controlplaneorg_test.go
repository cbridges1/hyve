package api

import (
	"path/filepath"
	"testing"

	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureControlPlaneOrganization_SeedsOnceThenIsIdempotent(t *testing.T) {
	store, err := orgdb.Open("sqlite", filepath.Join(t.TempDir(), "orgdb.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	ctx := t.Context()

	seeded, err := ensureControlPlaneOrganization(ctx, store, "hyve-system")
	require.NoError(t, err)
	assert.True(t, seeded, "the first call against a fresh datastore must actually create the row")

	org, err := store.GetOrganizationByName(ctx, "hyve-system")
	require.NoError(t, err)
	assert.Equal(t, "hyve-system", org.Namespace)

	env, err := store.GetEnvironmentByName(ctx, org.ID, orgdb.DefaultEnvironmentName)
	require.NoError(t, err)
	assert.Equal(t, orgdb.DefaultEnvironmentName, env.Name)

	seededAgain, err := ensureControlPlaneOrganization(ctx, store, "hyve-system")
	require.NoError(t, err)
	assert.False(t, seededAgain, "re-running against an already-seeded install must be a no-op, not a second row or an error")
}
