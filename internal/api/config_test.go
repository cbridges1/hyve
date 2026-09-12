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
	"k8s.io/apimachinery/pkg/types"
)

func newTestConfigMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerConfigRoutes(mux)
	return mux
}

// doConfigRequest mirrors doRequest (clusters_test.go) exactly, against
// registerConfigRoutes' own mux instead of registerClusterRoutes' — kept
// separate rather than parameterizing doRequest over which routes to
// register, matching this test file's existing one-mux-per-route-group
// precedent (newTestMux/newTestConfigMux).
func doConfigRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	newTestConfigMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleGetConfig_NotFound_ReturnsExistsFalse(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doConfigRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodGet, "/config", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto hyveConfigDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.False(t, dto.Exists)
}

func TestHandleGetConfig_AdminForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doConfigRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/config", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleGetConfig_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doConfigRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/config", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleGetConfig_ReturnsExistingSpec(t *testing.T) {
	cfg := &hyvev1alpha1.HyveConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "hyve-config", Namespace: testNamespace},
		Spec: hyvev1alpha1.HyveConfigSpec{
			StrictResourceDelete: true,
			DefaultAgentImage:    "ghcr.io/example/hyve-agent:v1",
			ImagePullSecrets:     []string{"ghcr-pull-secret"},
			ImageInstalls:        []hyvev1alpha1.ImageInstall{{Image: "alpine:3", Install: "apk add curl"}},
		},
	}
	s := &Server{Client: newFakeClient(t, cfg), Namespace: testNamespace}

	rec := doConfigRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodGet, "/config", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto hyveConfigDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.True(t, dto.Exists)
	assert.True(t, dto.StrictResourceDelete)
	assert.Equal(t, "ghcr.io/example/hyve-agent:v1", dto.DefaultAgentImage)
	require.Len(t, dto.ImageInstalls, 1)
	assert.Equal(t, "alpine:3", dto.ImageInstalls[0].Image)
	assert.Equal(t, []string{"ghcr-pull-secret"}, dto.ImagePullSecrets)
}

func TestHandleUpdateConfig_CreatesWhenMissing(t *testing.T) {
	fakeClient := newFakeClient(t)
	s := &Server{Client: fakeClient, Namespace: testNamespace}

	body := hyveConfigDTO{StrictResourceDelete: true, DefaultWorkflowImage: "alpine/k8s:1.30"}
	rec := doConfigRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/config", body)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto hyveConfigDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.True(t, dto.Exists)
	assert.Equal(t, "alpine/k8s:1.30", dto.DefaultWorkflowImage)

	var stored hyvev1alpha1.HyveConfig
	require.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "hyve-config"}, &stored))
	assert.Equal(t, "alpine/k8s:1.30", stored.Spec.DefaultWorkflowImage)
}

func TestHandleUpdateConfig_UpdatesWhenExists(t *testing.T) {
	cfg := &hyvev1alpha1.HyveConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "hyve-config", Namespace: testNamespace},
		Spec:       hyvev1alpha1.HyveConfigSpec{DefaultAgentImage: "old:tag"},
	}
	fakeClient := newFakeClient(t, cfg)
	s := &Server{Client: fakeClient, Namespace: testNamespace}

	body := hyveConfigDTO{DefaultAgentImage: "new:tag"}
	rec := doConfigRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/config", body)
	require.Equal(t, http.StatusOK, rec.Code)

	var stored hyvev1alpha1.HyveConfig
	require.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "hyve-config"}, &stored))
	assert.Equal(t, "new:tag", stored.Spec.DefaultAgentImage)
}

func TestHandleUpdateConfig_AdminForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doConfigRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/config", hyveConfigDTO{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateConfig_UsesConfiguredName(t *testing.T) {
	fakeClient := newFakeClient(t)
	s := &Server{Client: fakeClient, Namespace: testNamespace, ConfigName: "custom-config"}

	rec := doConfigRequest(t, s, hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/config", hyveConfigDTO{DefaultModuleImage: "x:y"})
	require.Equal(t, http.StatusOK, rec.Code)

	var stored hyvev1alpha1.HyveConfig
	require.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "custom-config"}, &stored))
	assert.Equal(t, "x:y", stored.Spec.DefaultModuleImage)
}
