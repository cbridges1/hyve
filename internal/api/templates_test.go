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

func newTemplateMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerTemplateRoutes(mux)
	return mux
}

func doTemplateRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	newTemplateMux(s).ServeHTTP(rec, req)
	return rec
}

func newTemplateDef(name string) *hyvev1alpha1.Template {
	return &hyvev1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: hyvev1alpha1.TemplateSpec{
			Driver: hyvev1alpha1.DriverRef{Source: "github.com/example/civo", Version: "v1.0.0"},
			Region: "PHX1",
			Params: map[string]string{"node_size": "small"},
		},
	}
}

func TestHandleListTemplates(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newTemplateDef("t1")), Namespace: testNamespace}

	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/templates", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dtos []templateDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "t1", dtos[0].Name)
}

func TestHandleGetTemplate_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/templates/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleCreateTemplate_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPost, "/templates", createTemplateRequest{Name: "t1"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateTemplate_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/templates", createTemplateRequest{
		Name: "t1",
		Spec: hyvev1alpha1.TemplateSpec{Driver: hyvev1alpha1.DriverRef{Source: "github.com/example/civo", Version: "v1.0.0"}},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestHandleDeleteTemplate_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newTemplateDef("t1")), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/templates/t1", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestHandleRenderTemplate_MergesParamsAndRegion(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newTemplateDef("t1")), Namespace: testNamespace}

	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/templates/t1/render",
		renderTemplateRequest{Region: "NYC1", Params: map[string]string{"node_size": "large", "node_count": "3"}})
	require.Equal(t, http.StatusOK, rec.Code)

	var spec hyvev1alpha1.ClusterDefinitionSpec
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
	assert.Equal(t, "NYC1", spec.Region, "explicit region overrides the template default")
	assert.Equal(t, "large", spec.Params["node_size"], "explicit param overrides the template default")
	assert.Equal(t, "3", spec.Params["node_count"])
	assert.Equal(t, "github.com/example/civo", spec.Driver.Source)
}

func TestHandleRenderTemplate_NoOverrides_UsesTemplateDefaults(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newTemplateDef("t1")), Namespace: testNamespace}

	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/templates/t1/render", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var spec hyvev1alpha1.ClusterDefinitionSpec
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
	assert.Equal(t, "PHX1", spec.Region)
	assert.Equal(t, "small", spec.Params["node_size"])
}

// TestHandleRenderTemplate_WithSchedule_SetsExpiresAt: this preview must
// reflect the same spec.expiresAt a real POST /clusters with this template
// would produce — see handleCreateCluster's identical fix and comment for
// why this can't live inside RenderClusterDefinitionSpec itself.
func TestHandleRenderTemplate_WithSchedule_SetsExpiresAt(t *testing.T) {
	tpl := newTemplateDef("t1")
	tpl.Spec.Schedule = "0 0 * * *"
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/templates/t1/render", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var spec hyvev1alpha1.ClusterDefinitionSpec
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
	assert.NotEmpty(t, spec.ExpiresAt)
}

func TestHandleRenderTemplate_InvalidSchedule_400(t *testing.T) {
	tpl := newTemplateDef("t1")
	tpl.Spec.Schedule = "not a cron expression"
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/templates/t1/render", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRenderTemplate_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/templates/missing/render", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleUpdateTemplate_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newTemplateDef("t1")), Namespace: testNamespace}

	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/templates/t1", updateTemplateRequest{
		Spec: hyvev1alpha1.TemplateSpec{Driver: hyvev1alpha1.DriverRef{Source: "github.com/example/civo", Version: "v2.0.0"}, Region: "NYC1"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto templateDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "NYC1", dto.Spec.Region)
	assert.Equal(t, "v2.0.0", dto.Spec.Driver.Version)
}

func TestHandleUpdateTemplate_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newTemplateDef("t1")), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/templates/t1", updateTemplateRequest{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateTemplate_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doTemplateRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/templates/missing", updateTemplateRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
