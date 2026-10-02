package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newModuleMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerModuleRoutes(mux)
	return mux
}

func doModuleRequest(t *testing.T, s *Server, role, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newModuleMux(s).ServeHTTP(rec, req)
	return rec
}

func newModuleDef(name string) *hyvev1alpha1.Module {
	return &hyvev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       hyvev1alpha1.ModuleSpec{Source: "github.com/example/civo", Version: "main"},
		Status:     hyvev1alpha1.ModuleStatus{Resolved: true, SHA256: "abc123"},
	}
}

func TestHandleListModules_AnyRole(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newModuleDef("m1")), Namespace: testNamespace}

	rec := doModuleRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/modules")
	require.Equal(t, http.StatusOK, rec.Code)
	var dtos []moduleDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "m1", dtos[0].Name)
	assert.True(t, dtos[0].Status.Resolved)
}

func TestHandleGetModule_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doModuleRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/modules/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetModule_Found(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newModuleDef("m1")), Namespace: testNamespace}
	rec := doModuleRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/modules/m1")
	require.Equal(t, http.StatusOK, rec.Code)
	var dto moduleDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "github.com/example/civo", dto.Spec.Source)
	assert.Equal(t, "abc123", dto.Status.SHA256)
}
