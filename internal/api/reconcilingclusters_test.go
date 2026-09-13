package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

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
