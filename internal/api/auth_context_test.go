package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestHandleAuthContext_DefaultClientSideServesDriverInfo(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Region = "us-east-1"
	cd.Spec.Params = map[string]string{"size": "small"}
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}

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

func TestHandleAuthContext_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	req := httptest.NewRequest(http.MethodGet, "/clusters/missing/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleAuthContext_RejectsPrimaryCluster(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace, PrimaryClusterName: "local"}

	req := httptest.NewRequest(http.MethodGet, "/clusters/local/auth-context", nil)
	rec := httptest.NewRecorder()
	newAuthContextMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}
