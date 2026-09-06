package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func doAccessMethodRequest(t *testing.T, s *Server, role, method, path string, body ...interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if len(body) > 0 && body[0] != nil {
		data, err := json.Marshal(body[0])
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newAccessMethodMux(s).ServeHTTP(rec, req)
	return rec
}

func newAccessMethodDef(name string) *hyvev1alpha1.AccessMethod {
	return &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: hyvev1alpha1.AccessMethodSpec{
			Driver:    hyvev1alpha1.DriverRef{Source: "github.com/hyve-modules/rancher-access", Version: "v1.0.0"},
			ServerURL: "https://rancher.example.com",
		},
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
	assert.Equal(t, "github.com/hyve-modules/rancher-access", dtos[0].Spec.Driver.Source)
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

// TestHandleGetAccessMethod_InlineAuth_ReturnsDeclaredRequiredEnv confirms
// the InlineAuth case returns spec.requiredEnv directly, with no module
// resolution attempted at all (ModulesDir is left unset here on purpose).
func TestHandleGetAccessMethod_InlineAuth_ReturnsDeclaredRequiredEnv(t *testing.T) {
	am := &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-rancher", Namespace: testNamespace},
		Spec: hyvev1alpha1.AccessMethodSpec{
			ServerURL:   "https://rancher.example.com",
			InlineAuth:  "echo hi",
			RequiredEnv: []string{"RANCHER_USERNAME", "RANCHER_PASSWORD"},
		},
	}
	s := &Server{Client: newFakeClient(t, am), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods/corp-rancher")
	require.Equal(t, http.StatusOK, rec.Code)
	var dto accessMethodDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, []string{"RANCHER_USERNAME", "RANCHER_PASSWORD"}, dto.RequiredEnv)
}

// TestHandleGetAccessMethod_ResolvesRequiredEnv confirms the single-object
// GET resolves the driver module server-side and surfaces its declared
// spec.requirements.env names — the exact list `hyve cluster auth` uses to
// decide which of the caller's own local env vars to forward to
// POST .../mint, and nothing broader.
func TestHandleGetAccessMethod_ResolvesRequiredEnv(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "modules", "rancher-access")
	require.NoError(t, os.MkdirAll(moduleDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "module.yaml"), []byte(`apiVersion: v1
kind: Module
metadata:
  name: rancher-access
  version: 1.0.0
  type: authOnly
spec:
  requirements:
    env:
      - name: RANCHER_TOKEN
        description: Rancher API token
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "auth.yaml"), []byte(`apiVersion: v1
kind: ClusterAuth
metadata:
  name: auth
spec:
  methods:
    - name: default
      auth:
        script: "true"
      exports: KUBECONFIG
`), 0644))

	am := &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-rancher", Namespace: testNamespace},
		Spec: hyvev1alpha1.AccessMethodSpec{
			Driver:    hyvev1alpha1.DriverRef{Source: "./modules/rancher-access", Version: "v1.0.0"},
			ServerURL: "https://rancher.example.com",
		},
	}
	s := &Server{Client: newFakeClient(t, am), Namespace: testNamespace, ModulesDir: dir}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods/corp-rancher")
	require.Equal(t, http.StatusOK, rec.Code)
	var dto accessMethodDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, []string{"RANCHER_TOKEN"}, dto.RequiredEnv)
}

// TestHandleGetAccessMethod_OtherNamespaceInvisible confirms the namespace
// scoping every other cluster-mode lookup already enforces also applies
// here — a tenant's API must never resolve another tenant's AccessMethod,
// same class of regression test as HYVE-MULTI-TENANCY-PLAN.md added for
// HyveAccessBinding.
func TestHandleGetAccessMethod_OtherNamespaceInvisible(t *testing.T) {
	other := &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-rancher", Namespace: "tenant-b"},
		Spec: hyvev1alpha1.AccessMethodSpec{
			Driver:    hyvev1alpha1.DriverRef{Source: "github.com/hyve-modules/rancher-access", Version: "v1.0.0"},
			ServerURL: "https://tenant-b-rancher.example.com",
		},
	}
	s := &Server{Client: newFakeClient(t, other), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/access-methods/corp-rancher")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleCreateAccessMethod_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPost, "/access-methods", createAccessMethodRequest{Name: "am1"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateAccessMethod_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/access-methods", createAccessMethodRequest{
		Name: "am1",
		Spec: hyvev1alpha1.AccessMethodSpec{
			InlineAuth: "echo hi",
			ServerURL:  "https://rancher.example.com",
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var dto accessMethodDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "am1", dto.Name)
}

func TestHandleCreateAccessMethod_AlreadyExists(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("am1")), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/access-methods", createAccessMethodRequest{Name: "am1"})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleUpdateAccessMethod_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("am1")), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/access-methods/am1", updateAccessMethodRequest{
		Spec: hyvev1alpha1.AccessMethodSpec{InlineAuth: "echo updated", ServerURL: "https://updated.example.com"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var dto accessMethodDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "https://updated.example.com", dto.Spec.ServerURL)
}

func TestHandleUpdateAccessMethod_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("am1")), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/access-methods/am1", updateAccessMethodRequest{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateAccessMethod_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/access-methods/missing", updateAccessMethodRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleDeleteAccessMethod_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("am1")), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/access-methods/am1")
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestHandleDeleteAccessMethod_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newAccessMethodDef("am1")), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodDelete, "/access-methods/am1")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleDeleteAccessMethod_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccessMethodRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/access-methods/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
