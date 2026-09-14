package controller

import (
	"path/filepath"
	"testing"

	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTargetNamespaces_NoReconcilingClusterID_ReturnsCurrentNamespaceOnly(t *testing.T) {
	got, err := resolveTargetNamespaces(t.Context(), nil, "hyve-system", "")
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

	gotA, err := resolveTargetNamespaces(ctx, store, "hyve-system", cellA.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"widget", "acorn"}, gotA, "cell-a's process must reconcile exactly the namespaces mapped to it — not acme (home cluster) or bolt (cell-b)")

	gotB, err := resolveTargetNamespaces(ctx, store, "hyve-system", cellB.ID)
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

	got, err := resolveTargetNamespaces(ctx, store, "hyve-system", rc.ID)
	require.NoError(t, err)
	assert.Empty(t, got, "no organizations mapped to this id yet must be an empty result, not an error")
}
