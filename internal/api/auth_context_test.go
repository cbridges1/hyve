package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAuthContextMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAuthContextRoutes(mux)
	return mux
}

// newTestModulesDirWithAuth creates a temp ModulesDir containing
// modules/civo/{module.yaml,auth.yaml} — matching newClusterDef's
// "./modules/civo" driver source (a local, no-hyve.lock-needed source, see
// module.resolveLocal) — with declared tool requirements, so
// handleAuthContext has something real to resolve and read.
func newTestModulesDirWithAuth(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "modules", "civo")
	require.NoError(t, os.MkdirAll(moduleDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "module.yaml"), []byte(`apiVersion: v1
kind: Module
metadata:
  name: civo
  version: 1.0.0
spec:
  requirements:
    tools:
      - name: civo
        description: Civo CLI
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "auth.yaml"), []byte(`apiVersion: v1
kind: ClusterAuth
metadata:
  name: auth
spec:
  bootstrap:
    script: "civo kubernetes config $HYVE_CLUSTER_NAME --save"
`), 0644))
	return dir
}

func TestHandleAuthContext_DefaultClientSideServesDriverInfo(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Region = "us-east-1"
	cd.Spec.Params = map[string]string{"size": "small"}
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, ModulesDir: newTestModulesDirWithAuth(t)}

	req := httptest.NewRequest(http.MethodGet, "/clusters/prod/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var dto authContextDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, cd.Spec.Driver.Source, dto.DriverSource)
	assert.Equal(t, cd.Spec.Driver.Version, dto.DriverVersion)
	assert.Equal(t, "us-east-1", dto.Region)
	assert.Equal(t, "small", dto.Params["size"])
	assert.Equal(t, "should-never-appear-in-dto", dto.DriverOutputs["HYVE_SECRET_TOKEN"])
	assert.Equal(t, "auth.yaml", dto.AuthFileName)
	assert.Contains(t, dto.AuthFileContent, "civo kubernetes config")
	require.Len(t, dto.Tools, 1)
	assert.Equal(t, "civo", dto.Tools[0].Name)
	assert.Equal(t, "Civo CLI", dto.Tools[0].Description)
}

// TestHandleAuthContext_NoAuthOperation_Returns500 confirms a driver
// module with no auth.yaml/auth.sh/auth file at all is a clear server
// error, not a panic or an empty-content 200.
func TestHandleAuthContext_NoAuthOperation_Returns500(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "modules", "civo"), 0755))
	cd := newClusterDef("prod")
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, ModulesDir: dir}

	req := httptest.NewRequest(http.MethodGet, "/clusters/prod/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleAuthContext_RejectsServerSideOverride(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Access.Method = hyvev1alpha1.AccessMethodModuleAuth
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}

	req := httptest.NewRequest(http.MethodGet, "/clusters/prod/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleAuthContext_RejectsTunnel(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Access.Method = hyvev1alpha1.AccessMethodTunnel
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}

	req := httptest.NewRequest(http.MethodGet, "/clusters/prod/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandleAuthContext_RejectsAgentProxy is milestone 5's own regression
// test for a real gap found live: a cluster with spec.access.agent.proxy
// true but spec.access.method left unset (the normal, expected shape,
// since Agent/Proxy is deliberately orthogonal to Method) used to look
// exactly like an ordinary client-side-auth cluster here — `hyve cluster
// auth` would have run the driver module's own auth.yaml locally instead
// of ever reaching GET /api/kubeconfig's agent-proxy dispatch at all.
func TestHandleAuthContext_RejectsAgentProxy(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Access.Agent = &hyvev1alpha1.AgentSpec{Enabled: true, Proxy: true}
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}

	req := httptest.NewRequest(http.MethodGet, "/clusters/prod/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleAuthContext_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	req := httptest.NewRequest(http.MethodGet, "/clusters/missing/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleAuthContext_AllowsPrimaryCluster confirms access.method: primary
// (hyvev1alpha1.AccessMethodPrimary) no longer routes to a special
// server-minted path — it's a pure host-identification marker now (see its
// own doc comment), and a primary-marked cluster has a real driver module
// like any other default-auth cluster, so its kubeconfig comes from this
// same client-side auth-context endpoint.
func TestHandleAuthContext_AllowsPrimaryCluster(t *testing.T) {
	hostCD := newClusterDef("local")
	hostCD.Spec.Access.Method = hyvev1alpha1.AccessMethodPrimary
	s := &Server{Client: newFakeClient(t, hostCD), Namespace: testNamespace, ModulesDir: newTestModulesDirWithAuth(t)}

	req := httptest.NewRequest(http.MethodGet, "/clusters/local/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
