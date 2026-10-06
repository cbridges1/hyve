package controller

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTargetNamespaces_NoReconcilingClusterID_ReturnsCurrentNamespaceOnly(t *testing.T) {
	got, err := resolveTargetNamespaces(t.Context(), nil, "hyve-system", "", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"hyve-system"}, got)
}

// TestResolveTargetNamespaces_FiltersToOrganizationsMappedToThisID proves
// Milestone 6's own core filtering property (see
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 6 "Tests"
// section): a controller process given one reconciling-cluster id only
// reconciles the namespaces mapped to it, against a Store with multiple
// organizations spread across multiple ids (including some still on the
// home cluster, reconciling_cluster_id NULL).
func TestResolveTargetNamespaces_FiltersToOrganizationsMappedToThisID(t *testing.T) {
	store, err := orgdb.Open("sqlite", filepath.Join(t.TempDir(), "orgdb.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	ctx := t.Context()

	cellA, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)
	cellB, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-b", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)

	_, err = store.CreateOrganization(ctx, orgdb.Organization{Name: "acme", Namespace: "acme"}) // home cluster
	require.NoError(t, err)
	_, err = store.CreateOrganization(ctx, orgdb.Organization{Name: "widget", Namespace: "widget", ReconcilingClusterID: &cellA.ID})
	require.NoError(t, err)
	_, err = store.CreateOrganization(ctx, orgdb.Organization{Name: "acorn", Namespace: "acorn", ReconcilingClusterID: &cellA.ID})
	require.NoError(t, err)
	_, err = store.CreateOrganization(ctx, orgdb.Organization{Name: "bolt", Namespace: "bolt", ReconcilingClusterID: &cellB.ID})
	require.NoError(t, err)

	gotA, err := resolveTargetNamespaces(ctx, store, "hyve-system", cellA.ID, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"widget", "acorn"}, gotA, "cell-a's process must reconcile exactly the namespaces mapped to it — not acme (home cluster) or bolt (cell-b)")

	gotB, err := resolveTargetNamespaces(ctx, store, "hyve-system", cellB.ID, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"bolt"}, gotB)
}

func TestResolveTargetNamespaces_NoOrganizationsMappedYet_ReturnsEmptyNotError(t *testing.T) {
	store, err := orgdb.Open("sqlite", filepath.Join(t.TempDir(), "orgdb.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	ctx := t.Context()

	rc, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)

	got, err := resolveTargetNamespaces(ctx, store, "hyve-system", rc.ID, false)
	require.NoError(t, err)
	assert.Empty(t, got, "no organizations mapped to this id yet must be an empty result, not an error")
}

// TestResolveTargetNamespaces_WatchHome_AddsHomeOrganizations: the home
// cluster's controller in a multi-tenant install reconciles its own
// namespace first, then every organization with no reconciling cluster —
// never one assigned elsewhere, and never its own namespace twice.
func TestResolveTargetNamespaces_WatchHome_AddsHomeOrganizations(t *testing.T) {
	store, err := orgdb.Open("sqlite", filepath.Join(t.TempDir(), "orgdb.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	ctx := t.Context()

	cell, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)
	for _, org := range []orgdb.Organization{
		{Name: "hyve-system", Namespace: "hyve-system"}, // the control plane's own organization
		{Name: "acme", Namespace: "acme"},
		{Name: "branlen", Namespace: "branlen"},
		{Name: "widget", Namespace: "widget", ReconcilingClusterID: &cell.ID},
	} {
		_, err := store.CreateOrganization(ctx, org)
		require.NoError(t, err)
	}

	got, err := resolveTargetNamespaces(ctx, store, "hyve-system", "", true)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, "hyve-system", got[0])
	assert.ElementsMatch(t, []string{"hyve-system", "acme", "branlen"}, got)

	gotCell, err := resolveTargetNamespaces(ctx, store, "hyve-system", cell.ID, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"widget"}, gotCell, "a reconciling cluster's process never takes home organizations")
}

func TestWatchTargetNamespaces(t *testing.T) {
	current := []string{"hyve-system", "acme"}

	t.Run("a new organization ends the watch", func(t *testing.T) {
		err := watchTargetNamespaces(t.Context(), time.Millisecond, current, func(context.Context) ([]string, error) {
			return []string{"acme", "hyve-system", "branlen"}, nil
		})
		assert.ErrorIs(t, err, errTargetNamespacesChanged)
	})

	t.Run("a swapped organization ends the watch", func(t *testing.T) {
		err := watchTargetNamespaces(t.Context(), time.Millisecond, current, func(context.Context) ([]string, error) {
			return []string{"hyve-system", "branlen"}, nil
		})
		assert.ErrorIs(t, err, errTargetNamespacesChanged)
	})

	t.Run("same set in another order, or a failed lookup, keeps watching", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		calls := 0
		err := watchTargetNamespaces(ctx, time.Millisecond, current, func(context.Context) ([]string, error) {
			calls++
			if calls%2 == 0 {
				return nil, errors.New("database unavailable")
			}
			return []string{"acme", "hyve-system"}, nil
		})
		assert.NoError(t, err, "returns nil once ctx ends")
		assert.Greater(t, calls, 2)
	})
}
