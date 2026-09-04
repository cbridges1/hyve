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

func newAccessMethodMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAccessMethodRoutes(mux)
	return mux
}

func doAccessMethodRequest(t *testing.T, s *Server, role, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newAccessMethodMux(s).ServeHTTP(rec, req)
	return rec
}

func newAccessMethodDef(name string) *hyvev1alpha1.AccessMethod {
	return &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       hyvev1alpha1.AccessMethodSpec{Provider: hyvev1alpha1.AccessMethodProviderRancher, ServerURL: "https://rancher.example.com"},
	}
}

func TestHandleListAccessMethods_AnyRole(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("corp-rancher")), Namespace: testNamespace}

	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods")
	require.Equal(t, http.StatusOK, rec.Code)
	var dtos []accessMethodDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "corp-rancher", dtos[0].Name)
	assert.Equal(t, hyvev1alpha1.AccessMethodProviderRancher, dtos[0].Spec.Provider)
}

func TestHandleGetAccessMethod_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetAccessMethod_Found(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("corp-rancher")), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods/corp-rancher")
	require.Equal(t, http.StatusOK, rec.Code)
	var dto accessMethodDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "https://rancher.example.com", dto.Spec.ServerURL)
}

// TestHandleGetAccessMethod_OtherNamespaceInvisible confirms the namespace
// scoping every other cluster-mode lookup already enforces also applies
// here — a tenant's API must never resolve another tenant's AccessMethod,
// same class of regression test as HYVE-MULTI-TENANCY-PLAN.md added for
// HyveAccessBinding.
func TestHandleGetAccessMethod_OtherNamespaceInvisible(t *testing.T) {
	other := &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-rancher", Namespace: "tenant-b"},
		Spec:       hyvev1alpha1.AccessMethodSpec{Provider: hyvev1alpha1.AccessMethodProviderRancher, ServerURL: "https://tenant-b-rancher.example.com"},
	}
	s := &Server{Client: newFakeClient(t, other), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods/corp-rancher")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
