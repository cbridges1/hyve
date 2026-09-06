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

func newWorkflowMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerWorkflowRoutes(mux)
	return mux
}

func doWorkflowRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	newWorkflowMux(s).ServeHTTP(rec, req)
	return rec
}

func newWorkflowDef(name string) *hyvev1alpha1.Workflow {
	return &hyvev1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: hyvev1alpha1.WorkflowSpec{
			Jobs: []hyvev1alpha1.WorkflowJob{
				{Name: "main", Steps: []hyvev1alpha1.WorkflowStep{{Name: "step1", Command: "echo hi"}}},
			},
		},
	}
}

func TestHandleListWorkflows(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newWorkflowDef("w1")), Namespace: testNamespace}

	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/workflows", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dtos []workflowDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "w1", dtos[0].Name)
}

func TestHandleGetWorkflow_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/workflows/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleCreateWorkflow_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPost, "/workflows", createWorkflowRequest{Name: "w1"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateWorkflow_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/workflows", createWorkflowRequest{
		Name: "w1",
		Spec: hyvev1alpha1.WorkflowSpec{
			Jobs: []hyvev1alpha1.WorkflowJob{{Name: "main", Steps: []hyvev1alpha1.WorkflowStep{{Name: "s1", Command: "echo hi"}}}},
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestHandleDeleteWorkflow_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newWorkflowDef("w1")), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/workflows/w1", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestHandleUpdateWorkflow_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newWorkflowDef("w1")), Namespace: testNamespace}

	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/workflows/w1", updateWorkflowRequest{
		Spec: hyvev1alpha1.WorkflowSpec{
			Jobs: []hyvev1alpha1.WorkflowJob{{Name: "updated", Steps: []hyvev1alpha1.WorkflowStep{{Name: "s", Command: "echo bye"}}}},
		},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto workflowDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.Spec)
	require.Len(t, dto.Spec.Jobs, 1)
	assert.Equal(t, "updated", dto.Spec.Jobs[0].Name)
}

func TestHandleUpdateWorkflow_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newWorkflowDef("w1")), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/workflows/w1", updateWorkflowRequest{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateWorkflow_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/workflows/missing", updateWorkflowRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A git-ref-backed workflow has no real Workflow CR named "gitref" — only a
// WorkflowRefStatus mirrored under a derived metadata.name — so PATCH must
// 404 rather than finding and overwriting anything, the same way DELETE
// already behaves for this case today.
func TestHandleUpdateWorkflow_GitRefBacked_NotFound(t *testing.T) {
	refStatus := &hyvev1alpha1.WorkflowRefStatus{
		ObjectMeta: metav1.ObjectMeta{Name: "derived-slug", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowRefStatusSpec{Name: "gitref", Source: "github.com/example/repo//workflows/gitref"},
	}
	s := &Server{Client: newFakeClient(t, refStatus), Namespace: testNamespace}
	rec := doWorkflowRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/workflows/gitref", updateWorkflowRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
