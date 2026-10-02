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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func newOrgConfigTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerOrganizationRoutes(mux)
	s.registerOrgConfigRoutes(mux)
	return mux
}

func doOrgConfigRequest(t *testing.T, s *Server, role, callerNamespace, method, orgName string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/organizations/"+orgName+"/config", reader)
	req = req.WithContext(contextWithRole(req.Context(), role))
	req = req.WithContext(contextWithNamespace(req.Context(), callerNamespace))
	rec := httptest.NewRecorder()
	newOrgConfigTestMux(s).ServeHTTP(rec, req)
	return rec
}

// TestHandleGetOrgConfig_NoReconcilingCluster_400 proves the gate
// requireOrgOwnReconcilingCluster exists for: an organization still on the
// control plane's own home cluster has no namespace-scoped HyveConfig of
// its own here — that's what GET /config (superadmin, control plane view)
// already manages.
func TestHandleGetOrgConfig_NoReconcilingCluster_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec := doOrgConfigRequest(t, s, hyvev1alpha1.RoleAdmin, "acme", http.MethodGet, "acme", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestOrgConfig_AdminCannotReachAnotherOrganization mirrors this file's own
// established precedent (TestOrgEnvironments_AdminCannotReachAnotherOrganization,
// TestOrgReconcilingCluster_AdminCannotReachAnotherOrganization).
func TestOrgConfig_AdminCannotReachAnotherOrganization(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "globex"}).Code)

	rec := doOrgConfigRequest(t, s, hyvev1alpha1.RoleAdmin, "globex", http.MethodGet, "acme", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestOrgConfig_AdminManagesOwnDedicatedReconcilingClusterConfig proves the
// end-to-end self-service flow: once an organization has a dedicated
// reconciling cluster of its own, its own admin (not a superadmin) can
// read and write a HyveConfig singleton scoped to that organization's own
// namespace on that cluster — distinct from, and never touching, the
// control plane's own home-cluster HyveConfig.
func TestOrgConfig_AdminManagesOwnDedicatedReconcilingClusterConfig(t *testing.T) {
	store := newTestOrgStore(t)
	homeClient := newFakeClient(t)
	s := &Server{Client: homeClient, OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	destClient := newFakeClient(t)
	rc, err := store.CreateReconcilingCluster(t.Context(), orgdb.ReconcilingCluster{Name: "acme", Kubeconfig: validTestKubeconfig})
	require.NoError(t, err)
	s.reconcilingClusterClients = map[string]*reconcilingClusterHandle{rc.ID: {Client: destClient}}
	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	require.NoError(t, store.SetOrganizationReconcilingCluster(t.Context(), org.ID, &rc.ID))

	getRec := doOrgConfigRequest(t, s, hyvev1alpha1.RoleAdmin, "acme", http.MethodGet, "acme", nil)
	require.Equal(t, http.StatusOK, getRec.Code)
	var got hyveConfigDTO
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &got))
	assert.False(t, got.Exists, "no HyveConfig created yet on the dedicated cluster")

	updateRec := doOrgConfigRequest(t, s, hyvev1alpha1.RoleAdmin, "acme", http.MethodPatch, "acme", hyveConfigDTO{
		StrictResourceDelete: true,
		DefaultWorkflowImage: "alpine/k8s:1.30",
	})
	require.Equal(t, http.StatusOK, updateRec.Code)
	var updated hyveConfigDTO
	require.NoError(t, json.Unmarshal(updateRec.Body.Bytes(), &updated))
	assert.True(t, updated.Exists)
	assert.True(t, updated.StrictResourceDelete)
	assert.Equal(t, "alpine/k8s:1.30", updated.DefaultWorkflowImage)

	// Written onto the dedicated destination cluster, in the organization's
	// own namespace — never the control plane's home cluster.
	var cfg hyvev1alpha1.HyveConfig
	require.NoError(t, destClient.Get(t.Context(), types.NamespacedName{Namespace: "acme", Name: defaultConfigName}, &cfg))
	assert.True(t, cfg.Spec.StrictResourceDelete)

	var onHome hyvev1alpha1.HyveConfig
	err = homeClient.Get(t.Context(), types.NamespacedName{Namespace: "acme", Name: defaultConfigName}, &onHome)
	assert.True(t, apierrors.IsNotFound(err), "the control plane's own home cluster must never receive this write")
}
