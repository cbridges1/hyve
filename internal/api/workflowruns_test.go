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
	k8stypes "k8s.io/apimachinery/pkg/types"
)

func newWorkflowRunMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerWorkflowRunRoutes(mux)
	return mux
}

func doWorkflowRunRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	newWorkflowRunMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleCreateWorkflowRun_RequiresAdmin(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRunRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPost, "/workflow-runs",
		createWorkflowRunRequest{Workflow: "install-podinfo", Cluster: "demo"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateWorkflowRun_RequiresCluster(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRunRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/workflow-runs",
		createWorkflowRunRequest{Workflow: "install-podinfo"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateWorkflowRun_RequiresExactlyOneOfWorkflowOrSource(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doWorkflowRunRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/workflow-runs",
		createWorkflowRunRequest{Cluster: "demo"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "neither workflow nor source set")

	rec = doWorkflowRunRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/workflow-runs",
		createWorkflowRunRequest{Workflow: "install-podinfo", Source: "github.com/org/repo//workflows/x.yaml", Cluster: "demo"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "both workflow and source set")
}

func TestHandleCreateWorkflowRun_CreatesWorkflowRunCR(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRunRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/workflow-runs",
		createWorkflowRunRequest{Workflow: "install-podinfo", Cluster: "demo", Params: map[string]string{"FOO": "bar"}})
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp createWorkflowRunResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Contains(t, resp.Name, "install-podinfo-")

	var cr hyvev1alpha1.WorkflowRun
	require.NoError(t, s.Client.Get(t.Context(), k8stypes.NamespacedName{Namespace: testNamespace, Name: resp.Name}, &cr))
	assert.Equal(t, "install-podinfo", cr.Spec.WorkflowRef.Name)
	assert.Equal(t, "demo", cr.Spec.ClusterRef)
	assert.Equal(t, "bar", cr.Spec.Params["FOO"])
}

func TestHandleGetWorkflowRun_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRunRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/workflow-runs/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetWorkflowRun_ReturnsStatus(t *testing.T) {
	now := metav1.Now()
	cr := &hyvev1alpha1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "install-podinfo-abc123", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowRunSpec{WorkflowRef: hyvev1alpha1.WorkflowRef{Name: "install-podinfo"}, ClusterRef: "demo"},
		Status: hyvev1alpha1.WorkflowRunStatus{
			Phase: hyvev1alpha1.WorkflowRunPhaseSucceeded, Message: "workflow completed",
			Output: "✅ podinfo installed", StartedAt: &now, CompletedAt: &now,
		},
	}
	s := &Server{Client: newFakeClient(t, cr), Namespace: testNamespace}
	rec := doWorkflowRunRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/workflow-runs/install-podinfo-abc123", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto workflowRunStatusDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, hyvev1alpha1.WorkflowRunPhaseSucceeded, dto.Phase)
	assert.Equal(t, "✅ podinfo installed", dto.Output)
	require.NotNil(t, dto.StartedAt)
	require.NotNil(t, dto.CompletedAt)
}
