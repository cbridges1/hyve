package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequireOrganizationNotMigrating_LocksResourceRoutesDuringMigration
// proves the proposal's own decided lock-don't-dual-serve design end to
// end through a real route (GET /clusters, one of the ~19 handlers this
// middleware wraps): a request against an organization whose
// reconciling_cluster_migration_status is 'migrating' gets 423, and the
// same request succeeds normally once the lock clears.
func TestRequireOrganizationNotMigrating_LocksResourceRoutesDuringMigration(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	mux := http.NewServeMux()
	s.registerClusterRoutes(mux)

	doGetClusters := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/clusters", nil)
		req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin))
		req = req.WithContext(contextWithNamespace(req.Context(), "acme"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	before := doGetClusters()
	assert.Equal(t, http.StatusOK, before.Code, "no migration in progress — the route must work normally")

	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	migrating := "migrating"
	require.NoError(t, store.SetOrganizationMigrationStatus(t.Context(), org.ID, &migrating))

	during := doGetClusters()
	assert.Equal(t, http.StatusLocked, during.Code, "a migrating organization must lock every resource-type route, not just DELETE/POST")

	require.NoError(t, store.SetOrganizationMigrationStatus(t.Context(), org.ID, nil))
	after := doGetClusters()
	assert.Equal(t, http.StatusOK, after.Code, "clearing the lock must unblock the route again")
}

// TestRequireOrganizationNotMigrating_UnrelatedNamespaceUnaffected proves
// the lock is scoped to the specific organization migrating, not global.
func TestRequireOrganizationNotMigrating_UnrelatedNamespaceUnaffected(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "globex"}).Code)

	acme, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	migrating := "migrating"
	require.NoError(t, store.SetOrganizationMigrationStatus(t.Context(), acme.ID, &migrating))

	mux := http.NewServeMux()
	s.registerClusterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/clusters", nil)
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin))
	req = req.WithContext(contextWithNamespace(req.Context(), "globex"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "globex isn't migrating — acme's lock must not affect it")
}

// TestResourceClient_ControlPlaneNamespaceRoutesLikeAnyOrganization proves
// Milestone 10 Part B (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md,
// nexus-config/docs): once the control plane's own namespace has a real
// Organization row (Part A — cmd/api's ensureControlPlaneOrganization,
// simulated here directly against Store since cmd/api can't be imported
// from this package), resourceClient resolves it through the exact same
// path as any tenant — including actually routing to a reconciling
// cluster if one is assigned, which the old namespace == s.Namespace
// special case made structurally impossible regardless of what the
// organization row said.
func TestResourceClient_ControlPlaneNamespaceRoutesLikeAnyOrganization(t *testing.T) {
	store := newTestOrgStore(t)
	homeClient := newFakeClient(t)
	s := &Server{Client: homeClient, OrgStore: store, Namespace: "hyve-system"}
	ctx := t.Context()

	// No organization row at all yet (pre-Part-A state) — must still fall
	// back to the home client, not error.
	rc0, err := s.resourceClient(ctx, "hyve-system")
	require.NoError(t, err)
	assert.Same(t, homeClient, rc0, "with no organization row yet, the control plane's own namespace must still fall back to the home cluster")

	// Seed the control-plane organization the same way
	// ensureControlPlaneOrganization does, with no reconciling cluster —
	// must still resolve to the home client.
	_, _, err = store.CreateOrganizationWithDefaults(ctx, orgdb.Organization{Name: "hyve-system", Namespace: "hyve-system"}, "", "")
	require.NoError(t, err)
	rc1, err := s.resourceClient(ctx, "hyve-system")
	require.NoError(t, err)
	assert.Same(t, homeClient, rc1, "an organization row with no reconciling cluster assigned must still resolve to the home cluster")

	// Now assign the control plane's own organization to a reconciling
	// cluster — this is the actual new capability: routing the control
	// plane's own resources to a cluster other than the one hyve-api
	// itself runs on.
	destClient := newFakeClient(t)
	rc, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)
	org, err := store.GetOrganizationByName(ctx, "hyve-system")
	require.NoError(t, err)
	require.NoError(t, store.SetOrganizationReconcilingCluster(ctx, org.ID, &rc.ID))
	s.reconcilingClusterClients = map[string]*reconcilingClusterHandle{rc.ID: {Client: destClient}}

	rc2, err := s.resourceClient(ctx, "hyve-system")
	require.NoError(t, err)
	assert.Same(t, destClient, rc2, "the control plane's own namespace must route to its assigned reconciling cluster, exactly like any tenant")
}

func doPoolRequest(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleSuperadmin))
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	s.registerReconcilingClusterRoutes(mux)
	mux.ServeHTTP(rec, req)
	return rec
}

// The install-wide list shows the pool and every organization's own
// clusters, each organization-owned one labeled with its owner.
func TestPoolReconcilingCluster_OwnerLabels(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	org, err := store.CreateOrganization(t.Context(), orgdb.Organization{Name: "acme", Namespace: "acme"})
	require.NoError(t, err)
	_, err = store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "k3s", OrganizationID: &org.ID, Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)

	rec := doPoolRequest(t, s, http.MethodPost, "/reconciling-clusters", createReconcilingClusterRequest{Name: "cell-b", Kubeconfig: validTestKubeconfig})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	// Re-registering rotates in place.
	rec = doPoolRequest(t, s, http.MethodPost, "/reconciling-clusters", createReconcilingClusterRequest{Name: "cell-b", Kubeconfig: validTestKubeconfig})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = doPoolRequest(t, s, http.MethodGet, "/reconciling-clusters", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []reconcilingClusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	owners := map[string]string{}
	for _, rc := range list {
		owners[rc.Name] = rc.Organization
	}
	assert.Equal(t, map[string]string{"cell-b": "", "k3s": "acme"}, owners)
}
