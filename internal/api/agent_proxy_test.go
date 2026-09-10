package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newAgentProxyTestMux mirrors this file's own registerAgentProxyRoutes
// registration — a real http.ServeMux, not a direct function call,
// because handleAgentProxy relies on r.PathValue("name")/("rest"), which
// only a mux that actually parsed the "/agent-proxy/{name}/{rest...}"
// pattern populates.
func newAgentProxyTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAgentProxyRoutes(mux)
	return mux
}

func doAgentProxyRequest(t *testing.T, s *Server, namespace, role, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := contextWithUsername(req.Context(), "test-caller")
	ctx = contextWithRole(ctx, role)
	ctx = contextWithNamespace(ctx, namespace)
	rec := httptest.NewRecorder()
	newAgentProxyTestMux(s).ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func proxyEnabledCluster(namespace, name string) *hyvev1alpha1.ClusterDefinition {
	return &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Access: hyvev1alpha1.AccessSpec{Agent: &hyvev1alpha1.AgentSpec{Enabled: true, Proxy: true}}},
	}
}

func TestHandleAgentProxy_ClusterNotFound_404(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace, AgentRegistry: NewAgentRegistry()}
	rec := doAgentProxyRequest(t, s, "acme", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleAgentProxy_CrossTenantRejection is the plan's own required
// case: a namespace-B caller must not reach a namespace-A cluster, even
// one that's proxy-enabled and has a live agent connection registered —
// styled like TestHandleCreateEnvironment_RejectsReservedNames's own
// "prove the rejection, not just the happy path" approach. ns always
// comes from TenantNamespace(r) (here, contextWithNamespace — the
// production equivalent of a verified session token's own namespace),
// never a caller-suppliable URL segment, which is what makes this safe
// by the same construction as every other /api/* cluster-scoped handler.
func TestHandleAgentProxy_CrossTenantRejection(t *testing.T) {
	cd := proxyEnabledCluster("namespace-a", "web")
	s := &Server{
		Client:        newFakeClient(t, cd),
		Namespace:     testNamespace,
		AgentRegistry: NewAgentRegistry(),
	}
	key := AgentConnectionKey{Namespace: "namespace-a", ClusterName: "web"}
	s.AgentRegistry.Register(key, &AgentConnection{})

	rec := doAgentProxyRequest(t, s, "namespace-b", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a namespace-b caller must not even learn namespace-a's cluster exists")

	// Confirm the SAME cluster, from its own tenant, does reach past the
	// existence check (though it 503s here since agentConn has no
	// ServiceAccountToken and no real Conn — this call proves the
	// namespace check itself isn't just accidentally rejecting
	// everything).
	rec = doAgentProxyRequest(t, s, "namespace-a", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleAgentProxy_ProxyNotEnabled_403(t *testing.T) {
	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme"},
		// Agent.Enabled true but Proxy false — installed and connectable,
		// but never opted into proxying.
		Spec: hyvev1alpha1.ClusterDefinitionSpec{Access: hyvev1alpha1.AccessSpec{Agent: &hyvev1alpha1.AgentSpec{Enabled: true, Proxy: false}}},
	}
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, AgentRegistry: NewAgentRegistry()}
	rec := doAgentProxyRequest(t, s, "acme", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleAgentProxy_ProxyNotEnabled_NilAgentSpec_403(t *testing.T) {
	cd := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme"}}
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, AgentRegistry: NewAgentRegistry()}
	rec := doAgentProxyRequest(t, s, "acme", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleAgentProxy_NotConnected_503(t *testing.T) {
	cd := proxyEnabledCluster("acme", "web")
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, AgentRegistry: NewAgentRegistry()}
	rec := doAgentProxyRequest(t, s, "acme", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleAgentProxy_NilAgentRegistry_503(t *testing.T) {
	cd := proxyEnabledCluster("acme", "web")
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}
	rec := doAgentProxyRequest(t, s, "acme", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleAgentProxy_NoCredentialsYet_503(t *testing.T) {
	cd := proxyEnabledCluster("acme", "web")
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, AgentRegistry: NewAgentRegistry()}
	s.AgentRegistry.Register(AgentConnectionKey{Namespace: "acme", ClusterName: "web"}, &AgentConnection{})
	rec := doAgentProxyRequest(t, s, "acme", hyvev1alpha1.RoleAdmin, "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleAgentProxy_UnrecognizedRole_403(t *testing.T) {
	cd := proxyEnabledCluster("acme", "web")
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace, AgentRegistry: NewAgentRegistry()}
	conn := &AgentConnection{}
	conn.SetHeartbeat("dev", "fake-token")
	s.AgentRegistry.Register(AgentConnectionKey{Namespace: "acme", ClusterName: "web"}, conn)
	rec := doAgentProxyRequest(t, s, "acme", "made-up-role", "/agent-proxy/web/api/v1/pods")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAgentImpersonationGroup_Mapping(t *testing.T) {
	admin, err := agentImpersonationGroup(hyvev1alpha1.RoleAdmin)
	require.NoError(t, err)
	assert.Equal(t, "hyve:admin", admin)

	superadmin, err := agentImpersonationGroup(hyvev1alpha1.RoleSuperadmin)
	require.NoError(t, err)
	assert.Equal(t, admin, superadmin, "superadmin must map to the same group as admin")

	readOnly, err := agentImpersonationGroup(hyvev1alpha1.RoleReadOnly)
	require.NoError(t, err)
	assert.Equal(t, "hyve:read-only", readOnly)

	_, err = agentImpersonationGroup("nonsense")
	assert.Error(t, err)
}
