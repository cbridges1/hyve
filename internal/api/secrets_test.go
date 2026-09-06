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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func newSecretsMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerSecretsRoutes(mux)
	return mux
}

func doSecretsRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	newSecretsMux(s).ServeHTTP(rec, req)
	return rec
}

func newCliSecretsDef(data map[string]string) *corev1.Secret {
	bytesData := make(map[string][]byte, len(data))
	for k, v := range data {
		bytesData[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: testNamespace},
		Data:       bytesData,
	}
}

func TestHandleListSecrets_KeysOnly_AnyRole(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "1", "BAR": "2"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/secrets", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var keys []string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &keys))
	assert.ElementsMatch(t, []string{"FOO", "BAR"}, keys)
	assert.NotContains(t, rec.Body.String(), "1", "values must never appear in the keys-only response")
}

func TestHandleListSecrets_NoSecretYet_EmptyKeys(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/secrets", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var keys []string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &keys))
	assert.Empty(t, keys)
}

func TestHandleListSecrets_WithValues_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "1"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/secrets?values=true", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleListSecrets_WithValues_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "1", "BAR": "2"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets?values=true", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var vars map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &vars))
	assert.Equal(t, map[string]string{"FOO": "1", "BAR": "2"}, vars)
}

func TestHandleGetSecret_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "1"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/secrets/FOO", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleGetSecret_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "bar"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/FOO", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "bar", out.Value)
}

func TestHandleGetSecret_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "bar"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/MISSING", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleSetSecret_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPut, "/secrets/FOO", setSecretRequest{Value: "bar"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleSetSecret_CreatesOnFirstWrite(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPut, "/secrets/FOO", setSecretRequest{Value: "bar"})
	require.Equal(t, http.StatusNoContent, rec.Code)

	getRec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/FOO", nil)
	require.Equal(t, http.StatusOK, getRec.Code)
	var out struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &out))
	assert.Equal(t, "bar", out.Value)
}

func TestHandleSetSecret_UpdatesExisting(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "old"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPut, "/secrets/FOO", setSecretRequest{Value: "new"})
	require.Equal(t, http.StatusNoContent, rec.Code)

	getRec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/FOO", nil)
	var out struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &out))
	assert.Equal(t, "new", out.Value)
}

func TestHandleSetSecret_InvalidKey(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPut, "/secrets/123-not-valid", setSecretRequest{Value: "bar"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUnsetSecret_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "bar"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodDelete, "/secrets/FOO", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUnsetSecret_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newCliSecretsDef(map[string]string{"FOO": "bar"})), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/secrets/FOO", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	getRec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/FOO", nil)
	assert.Equal(t, http.StatusNotFound, getRec.Code)
}

func TestHandleUnsetSecret_MissingSecretIsNoOp(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doSecretsRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/secrets/FOO", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// doSecretsRequestAs mirrors doSecretsRequest but also sets the caller's
// tenant namespace in context — needed to prove hyve-cli-secrets is
// per-tenant, not the single shared object it was before this fix.
func doSecretsRequestAs(t *testing.T, s *Server, namespace, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	ctx := contextWithRole(req.Context(), role)
	ctx = contextWithNamespace(ctx, namespace)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	newSecretsMux(s).ServeHTTP(rec, req)
	return rec
}

// TestSecrets_ScopedPerTenantNamespace is the regression test for the fix
// closing secrets.go's cross-tenant leak: hyve-cli-secrets used to be one
// object shared across every caller regardless of tenant — under Phase 2
// (one shared install, many tenants) that meant any tenant's admin could
// read/overwrite every other tenant's secrets. Setting a key in one
// tenant's namespace must not be visible from another's.
func TestSecrets_ScopedPerTenantNamespace(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	setRec := doSecretsRequestAs(t, s, "tenant-a", hyvev1alpha1.RoleAdmin, http.MethodPut, "/secrets/FOO", setSecretRequest{Value: "tenant-a-value"})
	require.Equal(t, http.StatusNoContent, setRec.Code)

	// Tenant A reads its own value back.
	getOwnRec := doSecretsRequestAs(t, s, "tenant-a", hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/FOO", nil)
	require.Equal(t, http.StatusOK, getOwnRec.Code)
	assert.Contains(t, getOwnRec.Body.String(), "tenant-a-value")

	// Tenant B, a completely different namespace, must see nothing.
	getOtherRec := doSecretsRequestAs(t, s, "tenant-b", hyvev1alpha1.RoleAdmin, http.MethodGet, "/secrets/FOO", nil)
	assert.Equal(t, http.StatusNotFound, getOtherRec.Code, "tenant B must not see tenant A's secret")

	// The control-plane namespace (s.Namespace) must not have received it
	// either — confirms this landed in "tenant-a", not the old fixed
	// s.Namespace this handler used before the fix.
	var controlPlaneSecret corev1.Secret
	err := s.Client.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: cliSecretsName}, &controlPlaneSecret)
	assert.Error(t, err, "must not have created hyve-cli-secrets in the control-plane namespace")
}
