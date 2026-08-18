package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newClusterDef(name string) *hyvev1alpha1.ClusterDefinition {
	return &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/civo", Version: "latest"}},
		Status: hyvev1alpha1.ClusterDefinitionStatus{
			DriverOutputs: map[string]string{"HYVE_SECRET_TOKEN": "should-never-appear-in-dto"},
		},
	}
}

// newTestMux builds a request handler equivalent to what Server.Routes
// produces for /api/clusters, pre-authenticated as an already-role-
// resolved caller (bypassing requireAuth/requireRole, which have their own
// dedicated tests) so these tests focus purely on the handlers' behavior.
func newTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerClusterRoutes(mux)
	return mux
}

func doRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	rec := httptest.NewRecorder()
	newTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleListClusters_ExcludesDriverOutputs(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "should-never-appear-in-dto")

	var dtos []clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "prod", dtos[0].Name)
}

// TestHandleListClusters_ScopedToNamespace is the direct regression test
// for a real bug caught live: the original handler called s.Client.List
// with no namespace restriction — a genuine cluster-wide list attempt.
// hyve-api's own RBAC Role (deploy/helm/hyve-api/templates/rbac.yaml) is
// deliberately namespace-scoped, not a ClusterRole, so that unscoped call
// always 500'd against real RBAC (never caught by earlier tests, since the
// fake client used here has no RBAC layer to violate at all) — confirmed
// live: "clusterdefinitions.hyve.io is forbidden ... at the cluster
// scope". This test can't reproduce the RBAC rejection itself without a
// real API server, but it does prove the fix's actual effect: a
// ClusterDefinition in a different namespace must never appear in the
// response, which only holds if the List call is namespace-scoped.
func TestHandleListClusters_ScopedToNamespace(t *testing.T) {
	other := newClusterDef("other-ns-cluster")
	other.Namespace = "some-other-namespace"
	s := &Server{Client: newFakeClient(t, newClusterDef("prod"), other), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dtos []clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "prod", dtos[0].Name)
}

func TestHandleGetCluster_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetCluster_Found(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/prod", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "prod", dto.Name)
	assert.NotContains(t, rec.Body.String(), "should-never-appear-in-dto")
}

func TestHandleCreateCluster_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPost, "/clusters", createClusterRequest{Name: "new-cluster"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateCluster_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name: "new-cluster",
		Spec: hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/civo", Version: "latest"}},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	rec2 := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/new-cluster", nil)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

func TestHandleCreateCluster_MissingName(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateCluster_AlreadyExists(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{Name: "prod"})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleDeleteCluster_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodDelete, "/clusters/prod", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleDeleteCluster_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/clusters/prod", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec2 := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/prod", nil)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
}

func TestHandleDeleteCluster_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/clusters/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
