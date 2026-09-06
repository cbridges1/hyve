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
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func newEnvironmentsTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerEnvironmentRoutes(mux)
	return mux
}

func doEnvironmentRequest(t *testing.T, s *Server, role string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/environments", bytes.NewReader(data))
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newEnvironmentsTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleCreateEnvironment_RequiresSuperadmin(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doEnvironmentRequest(t, s, hyvev1alpha1.RoleAdmin, createEnvironmentRequest{Name: "acme"})
	assert.Equal(t, http.StatusForbidden, rec.Code, "an ordinary tenant admin must not be able to create a new environment")
}

func TestHandleCreateEnvironment_MissingName_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doEnvironmentRequest(t, s, hyvev1alpha1.RoleSuperadmin, createEnvironmentRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateEnvironment_CreatesNamespaceRBACAndRegistryObject(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doEnvironmentRequest(t, s, hyvev1alpha1.RoleSuperadmin, createEnvironmentRequest{Name: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto environmentDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "acme", dto.Name)
	assert.Equal(t, "acme", dto.Namespace)

	ctx := t.Context()

	var ns corev1.Namespace
	require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Name: "acme"}, &ns), "the tenant namespace must exist")

	for _, want := range []struct{ sa, clusterRole string }{
		{"hyve-access-admin", "cluster-admin"},
		{"hyve-access-readonly", "view"},
	} {
		var sa corev1.ServiceAccount
		require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Namespace: "acme", Name: want.sa}, &sa),
			"ServiceAccount %s must exist in the new tenant namespace", want.sa)

		var rb rbacv1.RoleBinding
		require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Namespace: "acme", Name: want.sa}, &rb),
			"RoleBinding %s must exist in the new tenant namespace", want.sa)
		assert.Equal(t, want.clusterRole, rb.RoleRef.Name)
		assert.Equal(t, "ClusterRole", rb.RoleRef.Kind, "must scope the ClusterRole via a namespaced RoleBinding, never a ClusterRoleBinding")
		require.Len(t, rb.Subjects, 1)
		assert.Equal(t, "acme", rb.Subjects[0].Namespace, "the RoleBinding must only grant within the new tenant's own namespace")
	}

	var env hyvev1alpha1.HyveEnvironment
	require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "acme"}, &env),
		"the HyveEnvironment registry object must live in the control-plane namespace, not the tenant's")
	assert.Equal(t, "acme", env.Spec.Namespace)
}

// TestHandleCreateEnvironment_IdempotentAfterPartialFailure proves a re-POST
// fills in whatever's missing instead of erroring on "already exists" — the
// whole point of checking existence before creating each of the three
// steps (see handleCreateEnvironment's own doc comment).
func TestHandleCreateEnvironment_IdempotentAfterPartialFailure(t *testing.T) {
	// Simulate a partial failure: the namespace and RBAC scaffolding
	// already exist (as if a first POST got that far), but the
	// HyveEnvironment registry object was never created (as if the
	// process crashed, or that step failed).
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	adminSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "hyve-access-admin", Namespace: "acme"}}
	roSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "hyve-access-readonly", Namespace: "acme"}}
	s := &Server{Client: newFakeClient(t, ns, adminSA, roSA), Namespace: testNamespace}

	rec := doEnvironmentRequest(t, s, hyvev1alpha1.RoleSuperadmin, createEnvironmentRequest{Name: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code, "re-POST must succeed and fill in the missing HyveEnvironment, not fail on the parts that already exist")

	var env hyvev1alpha1.HyveEnvironment
	require.NoError(t, s.Client.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "acme"}, &env))

	// Re-running again once everything already exists must also succeed
	// (fully idempotent), not just the one-missing-piece case above.
	rec2 := doEnvironmentRequest(t, s, hyvev1alpha1.RoleSuperadmin, createEnvironmentRequest{Name: "acme"})
	assert.Equal(t, http.StatusCreated, rec2.Code, "re-POST once everything already exists must still succeed")
}
