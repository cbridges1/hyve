package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleWhoami_ReturnsResolvedIdentity(t *testing.T) {
	mux := http.NewServeMux()
	s := &Server{Namespace: "hyve-system"}
	s.registerWhoamiRoute(mux)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "cedric"))
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin))
	req = req.WithContext(contextWithNamespace(req.Context(), "acme"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp whoamiResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "cedric", resp.Username)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, resp.Role)
	assert.Equal(t, "acme", resp.Namespace)
}

func TestHandleWhoami_SuperadminNamespaceIsControlPlane(t *testing.T) {
	mux := http.NewServeMux()
	s := &Server{Namespace: "hyve-system"}
	s.registerWhoamiRoute(mux)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "civotest"))
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleSuperadmin))
	req = req.WithContext(contextWithNamespace(req.Context(), ""))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp whoamiResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "hyve-system", resp.Namespace)
}

// TestHandleWhoami_ShowsOwnOrganizationReconcilingCluster proves an
// ordinary admin can see which reconciling cluster their own organization
// is on without needing superadmin access to GET /organizations (which
// this caller could never reach — genuinely cross-tenant, superadmin-only
// by design).
func TestHandleWhoami_ShowsOwnOrganizationReconcilingCluster(t *testing.T) {
	store := newTestOrgStore(t)
	ctx := t.Context()
	rc, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: "apiVersion: v1\nkind: Config\n"})
	require.NoError(t, err)
	org, err := store.CreateOrganization(ctx, orgdb.Organization{Name: "acme", Namespace: "acme", ReconcilingClusterID: &rc.ID})
	require.NoError(t, err)
	migrating := "migrating"
	require.NoError(t, store.SetOrganizationMigrationStatus(ctx, org.ID, &migrating))

	mux := http.NewServeMux()
	s := &Server{Namespace: "hyve-system", OrgStore: store}
	s.registerWhoamiRoute(mux)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "alice"))
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin))
	req = req.WithContext(contextWithNamespace(req.Context(), "acme"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp whoamiResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "cell-a", resp.ReconcilingCluster)
	assert.True(t, resp.Migrating)
}

func TestWhoamiRoute_RequiresAuthAndRole(t *testing.T) {
	s := &Server{Client: newFakeClient(t), SigningKey: []byte("key")}

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
