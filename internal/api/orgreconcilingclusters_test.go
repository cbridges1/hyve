package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"
)

func doOrgRCRequest(t *testing.T, s *Server, method, path, role, callerNamespace string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(contextWithRole(req.Context(), role))
	req = req.WithContext(contextWithNamespace(req.Context(), callerNamespace))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	return rec
}

// newOrgRCTestServer creates organization acme and returns the server, its
// store, and acme's row.
func newOrgRCTestServer(t *testing.T) (*Server, *orgdb.Store, orgdb.Organization) {
	t.Helper()
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)
	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	return s, store, org
}

// seedReachable pre-seeds a fake client for rc, standing in for the real
// dial buildReconcilingClusterHandle would make, so a switch onto rc can run
// its migration in-process.
func seedReachable(t *testing.T, s *Server, rc orgdb.ReconcilingCluster) {
	t.Helper()
	if s.reconcilingClusterClients == nil {
		s.reconcilingClusterClients = map[string]*reconcilingClusterHandle{}
	}
	s.reconcilingClusterClients[rc.ID] = &reconcilingClusterHandle{Client: newFakeClient(t)}
}

func decodeList(t *testing.T, rec *httptest.ResponseRecorder) map[string]orgReconcilingClusterEntryDTO {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list []orgReconcilingClusterEntryDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	out := map[string]orgReconcilingClusterEntryDTO{}
	for _, e := range list {
		out[e.Name+"/"+e.Ownership] = e
	}
	return out
}

func TestGetOrgReconcilingCluster_OnHomeCluster(t *testing.T) {
	s, _, _ := newOrgRCTestServer(t)

	rec := doOrgRCRequest(t, s, http.MethodGet, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto orgReconcilingClusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.True(t, dto.OnHomeCluster)
	assert.Empty(t, dto.Name)
}

// An admin naming a different organization in the URL is rejected on every
// reconciling-cluster endpoint, not silently redirected to their own.
func TestOrgReconcilingCluster_AdminCannotReachAnotherOrganization(t *testing.T) {
	s, _, _ := newOrgRCTestServer(t)
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "globex"}).Code)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/organizations/acme/reconciling-cluster", nil},
		{http.MethodPut, "/organizations/acme/reconciling-cluster", orgReconcilingClusterRequest{Kubeconfig: validTestKubeconfig}},
		{http.MethodDelete, "/organizations/acme/reconciling-cluster", nil},
		{http.MethodGet, "/organizations/acme/reconciling-clusters", nil},
		{http.MethodPost, "/organizations/acme/reconciling-clusters", orgReconcilingClusterRequest{Name: "prod", Kubeconfig: validTestKubeconfig}},
		{http.MethodDelete, "/organizations/acme/reconciling-clusters/prod", nil},
	} {
		rec := doOrgRCRequest(t, s, tc.method, tc.path, hyvev1alpha1.RoleAdmin, "globex", tc.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s", tc.method, tc.path)
	}
}

// Adding clusters stores them without switching, so an organization can
// keep several on hand; the list shows only its own, never the pool.
func TestAddOrgReconcilingCluster_StoresSeveralWithoutSwitching(t *testing.T) {
	s, store, _ := newOrgRCTestServer(t)

	for _, name := range []string{"k3s", "civo"} {
		rec := doOrgRCRequest(t, s, http.MethodPost, "/organizations/acme/reconciling-clusters", hyvev1alpha1.RoleAdmin, "acme",
			orgReconcilingClusterRequest{Name: name, Kubeconfig: validTestKubeconfig})
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}
	_, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)

	list := decodeList(t, doOrgRCRequest(t, s, http.MethodGet, "/organizations/acme/reconciling-clusters", hyvev1alpha1.RoleAdmin, "acme", nil))
	assert.Len(t, list, 2)
	require.Contains(t, list, "k3s/organization")
	require.Contains(t, list, "civo/organization")
	assert.NotEmpty(t, list["k3s/organization"].Server)
	for _, e := range list {
		assert.False(t, e.Active, "adding must not switch: %s", e.Name)
	}

	placement := doOrgRCRequest(t, s, http.MethodGet, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", nil)
	var dto orgReconcilingClusterDTO
	require.NoError(t, json.Unmarshal(placement.Body.Bytes(), &dto))
	assert.True(t, dto.OnHomeCluster)

	// Re-posting a name rotates its kubeconfig in place.
	rotated := validTestKubeconfig + "# rotated\n"
	rec := doOrgRCRequest(t, s, http.MethodPost, "/organizations/acme/reconciling-clusters", hyvev1alpha1.RoleAdmin, "acme",
		orgReconcilingClusterRequest{Name: "k3s", Kubeconfig: rotated})
	require.Equal(t, http.StatusOK, rec.Code)
	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	rc, err := store.GetOrgReconcilingClusterByName(t.Context(), org.ID, "k3s")
	require.NoError(t, err)
	assert.Equal(t, rotated, rc.Kubeconfig)
}

func TestAddOrgReconcilingCluster_Validation(t *testing.T) {
	s, _, _ := newOrgRCTestServer(t)

	for _, body := range []orgReconcilingClusterRequest{
		{Name: "", Kubeconfig: validTestKubeconfig},
		{Name: "Not_A_Label", Kubeconfig: validTestKubeconfig},
		{Name: "prod", Kubeconfig: ""},
		{Name: "prod", Kubeconfig: "not a kubeconfig"},
	} {
		rec := doOrgRCRequest(t, s, http.MethodPost, "/organizations/acme/reconciling-clusters", hyvev1alpha1.RoleAdmin, "acme", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%+v", body)
	}
}

// Switching by name moves the organization between its own stored
// clusters with no kubeconfig re-entered.
func TestPutOrgReconcilingCluster_SwitchesByName(t *testing.T) {
	s, store, org := newOrgRCTestServer(t)

	k3s, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "k3s", OrganizationID: &org.ID, Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	civo, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "civo", OrganizationID: &org.ID, Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	seedReachable(t, s, k3s)
	seedReachable(t, s, civo)

	for _, name := range []string{"k3s", "civo"} {
		rec := doOrgRCRequest(t, s, http.MethodPut, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", orgReconcilingClusterRequest{Name: name})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var dto orgReconcilingClusterDTO
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		assert.Equal(t, name, dto.Name)
		assert.Equal(t, ownershipOrganization, dto.Ownership)
	}

	list := decodeList(t, doOrgRCRequest(t, s, http.MethodGet, "/organizations/acme/reconciling-clusters", hyvev1alpha1.RoleAdmin, "acme", nil))
	assert.True(t, list["civo/organization"].Active)
	assert.False(t, list["k3s/organization"].Active)
}

// A pool cluster isn't the organization's to select — and says so the same
// way as a name that doesn't exist at all, so the response doesn't reveal
// what the pool holds. One a superadmin assigned directly still shows up in
// the list while it's active.
func TestPutOrgReconcilingCluster_PoolCluster(t *testing.T) {
	s, store, org := newOrgRCTestServer(t)
	cellA, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)

	for _, name := range []string{"cell-a", "does-not-exist"} {
		rec := doOrgRCRequest(t, s, http.MethodPut, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", orgReconcilingClusterRequest{Name: name})
		assert.Equal(t, http.StatusNotFound, rec.Code, name)
	}

	require.NoError(t, store.SetOrganizationReconcilingCluster(t.Context(), org.ID, &cellA.ID))
	list := decodeList(t, doOrgRCRequest(t, s, http.MethodGet, "/organizations/acme/reconciling-clusters", hyvev1alpha1.RoleAdmin, "acme", nil))
	require.Contains(t, list, "cell-a/pool")
	assert.True(t, list["cell-a/pool"].Active)

	rec := doOrgRCRequest(t, s, http.MethodPut, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", orgReconcilingClusterRequest{Name: "cell-a"})
	assert.Equal(t, http.StatusOK, rec.Code, "selecting the cluster it's already on is a no-op")
}

// The original one-cluster-per-organization request shape — a bare
// kubeconfig — still registers a cluster named after the namespace. The
// switch itself can't complete here (the id is minted inside the handler,
// so there's no fake client to pre-seed), so this only checks the row.
func TestPutOrgReconcilingCluster_KubeconfigOnly_RegistersUnderNamespaceName(t *testing.T) {
	s, store, org := newOrgRCTestServer(t)

	doOrgRCRequest(t, s, http.MethodPut, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", orgReconcilingClusterRequest{Kubeconfig: validTestKubeconfig})

	rc, err := store.GetOrgReconcilingClusterByName(t.Context(), org.ID, "acme")
	require.NoError(t, err)
	assert.Equal(t, validTestKubeconfig, rc.Kubeconfig)
}

func TestPutOrgReconcilingCluster_InvalidKubeconfig_400(t *testing.T) {
	s, store, org := newOrgRCTestServer(t)

	rec := doOrgRCRequest(t, s, http.MethodPut, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", orgReconcilingClusterRequest{Kubeconfig: "not a kubeconfig"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	owned, err := store.ListOrgReconcilingClusters(t.Context(), org.ID)
	require.NoError(t, err)
	assert.Empty(t, owned, "an invalid kubeconfig must not register anything")
}

func TestPutOrgReconcilingCluster_EmptyBody_MigratesHome(t *testing.T) {
	s, _, _ := newOrgRCTestServer(t)

	// Already on the home cluster: a no-op switch, needing no reachable
	// destination.
	rec := doOrgRCRequest(t, s, http.MethodPut, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", orgReconcilingClusterRequest{})
	require.Equal(t, http.StatusOK, rec.Code)
	var dto orgReconcilingClusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.True(t, dto.OnHomeCluster)
}

// DELETE on the singular placement moves back home but keeps every stored
// cluster, so switching back later needs no kubeconfig.
func TestDeleteOrgReconcilingCluster_MovesHomeKeepsStoredClusters(t *testing.T) {
	s, store, org := newOrgRCTestServer(t)
	own, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "k3s", OrganizationID: &org.ID, Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	seedReachable(t, s, own)
	require.NoError(t, store.SetOrganizationReconcilingCluster(t.Context(), org.ID, &own.ID))

	rec := doOrgRCRequest(t, s, http.MethodDelete, "/organizations/acme/reconciling-cluster", hyvev1alpha1.RoleAdmin, "acme", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	updated, err := store.GetOrganization(t.Context(), org.ID)
	require.NoError(t, err)
	assert.Nil(t, updated.ReconcilingClusterID)
	_, err = store.GetReconcilingCluster(t.Context(), own.ID)
	assert.NoError(t, err, "the stored cluster must survive moving home")
}

func TestRemoveOrgReconcilingCluster(t *testing.T) {
	s, store, org := newOrgRCTestServer(t)
	active, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "active", OrganizationID: &org.ID, Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	spare, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "spare", OrganizationID: &org.ID, Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	pool, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "cell-a", Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	require.NoError(t, store.SetOrganizationReconcilingCluster(t.Context(), org.ID, &active.ID))

	rec := doOrgRCRequest(t, s, http.MethodDelete, "/organizations/acme/reconciling-clusters/active", hyvev1alpha1.RoleAdmin, "acme", nil)
	assert.Equal(t, http.StatusConflict, rec.Code, "the active cluster can't be removed out from under the organization")

	rec = doOrgRCRequest(t, s, http.MethodDelete, "/organizations/acme/reconciling-clusters/cell-a", hyvev1alpha1.RoleAdmin, "acme", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "an organization can't remove a pool cluster")
	_, err = store.GetReconcilingCluster(t.Context(), pool.ID)
	assert.NoError(t, err)

	rec = doOrgRCRequest(t, s, http.MethodDelete, "/organizations/acme/reconciling-clusters/spare", hyvev1alpha1.RoleAdmin, "acme", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	_, err = store.GetReconcilingCluster(t.Context(), spare.ID)
	assert.ErrorIs(t, err, orgdb.ErrNotFound)
}
