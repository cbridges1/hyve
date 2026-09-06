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
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestHandleCreateCluster_FromTemplate(t *testing.T) {
	tpl := newTemplateDef("t1")
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name: "from-tpl",
		Template: &createClusterFromTemplateRef{
			Name:   "t1",
			Region: "NYC1",
			Params: map[string]string{"node_size": "large"},
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "from-tpl", dto.Name)
	assert.Equal(t, tpl.Spec.Driver.Source, dto.Driver)

	var created hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: "from-tpl"}, &created))
	assert.Equal(t, "NYC1", created.Spec.Region)
	assert.Equal(t, "large", created.Spec.Params["node_size"])
}

// TestHandleCreateCluster_FromTemplate_WithSchedule_SetsExpiresAt is the
// regression test for a real bug: RenderClusterDefinitionSpec never looks
// at Template.Spec.Schedule (it can't — internal/template, where
// CronNextOccurrence lives, already imports this package the other way),
// so a cluster created here from a schedule-having template got no
// spec.expiresAt at all. internal/reconcile's expiry check
// (ReconcileOne: `if def.Spec.ExpiresAt != ""`) had nothing to act on, so
// scheduled deletion silently never happened for any cluster created via
// this endpoint — confirmed live against a real k3d deployment before this
// fix, where the equivalent local-mode path (cmd/cluster/create.go) has
// always computed this correctly.
func TestHandleCreateCluster_FromTemplate_WithSchedule_SetsExpiresAt(t *testing.T) {
	tpl := newTemplateDef("scheduled")
	tpl.Spec.Schedule = "0 0 * * *" // every day at midnight — just needs to resolve to *some* future time
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-scheduled-tpl",
		Template: &createClusterFromTemplateRef{Name: "scheduled"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: "from-scheduled-tpl"}, &created))
	assert.NotEmpty(t, created.Spec.ExpiresAt, "a cluster created from a schedule-having template must get spec.expiresAt set, or expiry can never fire")
}

func TestHandleCreateCluster_FromTemplate_NoSchedule_NoExpiresAt(t *testing.T) {
	tpl := newTemplateDef("unscheduled")
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-unscheduled-tpl",
		Template: &createClusterFromTemplateRef{Name: "unscheduled"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: "from-unscheduled-tpl"}, &created))
	assert.Empty(t, created.Spec.ExpiresAt)
}

func TestHandleCreateCluster_FromTemplate_InvalidSchedule_400(t *testing.T) {
	tpl := newTemplateDef("bad-schedule")
	tpl.Spec.Schedule = "not a cron expression"
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-bad-schedule-tpl",
		Template: &createClusterFromTemplateRef{Name: "bad-schedule"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateCluster_FromTemplate_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-tpl",
		Template: &createClusterFromTemplateRef{Name: "missing"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
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

func TestHandleGetClusterResources_ReadOnlyAllowed(t *testing.T) {
	def := newClusterDef("c1")
	def.Spec.Resources = []hyvev1alpha1.ResourceRef{{Name: "podinfo", Source: "./resource-files/podinfo.yaml"}}
	def.Status.AppliedResources = map[string]*hyvev1alpha1.AppliedResource{
		"podinfo": {SourceSHA256: "abc123", AppliedAt: "2026-01-01T00:00:00Z"},
	}
	s := &Server{Client: newFakeClient(t, def), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/c1/resources", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterResourcesDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Len(t, dto.Resources, 1)
	assert.Equal(t, "podinfo", dto.Resources[0].Name)
	require.Contains(t, dto.AppliedResources, "podinfo")
	assert.Equal(t, "abc123", dto.AppliedResources["podinfo"].SourceSHA256)
}

func TestHandleGetClusterResources_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/missing/resources", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetClusterResources_Empty(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/c1/resources", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterResourcesDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Empty(t, dto.Resources)
	assert.Empty(t, dto.AppliedResources)
}

func TestHandleUpdateCluster_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/clusters/c1", updateClusterRequest{
		Spec: hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/updated", Version: "v2"}},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "./modules/updated", dto.Driver)

	rec2 := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/c1", nil)
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &dto))
	require.NotNil(t, dto.Spec)
	assert.Equal(t, "v2", dto.Spec.Driver.Version)
}

func TestHandleUpdateCluster_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/clusters/c1", updateClusterRequest{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateCluster_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/clusters/missing", updateClusterRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
