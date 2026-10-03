package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireAuth_MissingHeader_401(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	rec := httptest.NewRecorder()

	s.requireAuth(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_MalformedHeader_401(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Authorization", "not-bearer-format")
	rec := httptest.NewRecorder()

	s.requireAuth(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_InvalidToken_401(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()

	s.requireAuth(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_ValidToken_PassesUsernameToContext(t *testing.T) {
	key := []byte("key")
	s := &Server{SigningKey: key}
	token, err := IssueAccessToken(key, "cedric", "")
	require.NoError(t, err)

	var gotUsername string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsername, _ = UsernameFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.requireAuth(next).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "cedric", gotUsername)
}

// TestRequireRole_UnboundIdentity_401 confirms an authenticated identity
// with no binding at all gets 401, not 403 — deliberately the same status
// as an expired/invalid session, since there's nothing the caller can do
// but log in again either way. See requireRole's own doc comment for why
// this is distinct from RequireRole's role-mismatch case (a bound
// identity lacking permission for one action), which correctly stays 403.
func TestRequireRole_UnboundIdentity_401(t *testing.T) {
	s := &Server{Client: newFakeClient(t)}

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "nobody"))
	rec := httptest.NewRecorder()

	s.requireRole(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireRole_BoundIdentity_PassesRoleToContext(t *testing.T) {
	store := newTestOrgStore(t)
	_, err := createAccount(context.Background(), store, orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	var gotRole string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRole, _ = RoleFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "cedric"))
	rec := httptest.NewRecorder()

	s.requireRole(next).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, gotRole)
}

func TestRequireAuth_Then_RequireRole_UnauthenticatedNeverReachesRoleCheck(t *testing.T) {
	// requireRole run standalone (as it would be if requireAuth were
	// somehow skipped) must reject rather than silently proceeding —
	// belt-and-suspenders against a future refactor reordering the chain.
	s := &Server{Client: newFakeClient(t)}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	rec := httptest.NewRecorder()

	s.requireRole(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireRole_DistinguishesAdminFromReadOnly(t *testing.T) {
	for _, tc := range []struct {
		role     string
		allowed  []string
		expectOK bool
	}{
		{hyvev1alpha1.RoleAdmin, []string{hyvev1alpha1.RoleAdmin}, true},
		{hyvev1alpha1.RoleReadOnly, []string{hyvev1alpha1.RoleAdmin}, false},
		{hyvev1alpha1.RoleReadOnly, []string{hyvev1alpha1.RoleReadOnly, hyvev1alpha1.RoleAdmin}, true},
		// A superadmin satisfies any RoleAdmin-only gate — confirmed live
		// this is required: the "act as" environment switcher is useless
		// if every RoleAdmin-gated mutation (clusters/templates/workflows/
		// resources/accessmethods/secrets/workflow-runs) still rejects
		// superadmin outright regardless of which tenant it's acting as.
		{hyvev1alpha1.RoleSuperadmin, []string{hyvev1alpha1.RoleAdmin}, true},
		// The reverse never holds — an ordinary admin gets nothing extra
		// from a superadmin-exclusive gate (POST/GET /environments, the
		// host-cluster kubeconfig path).
		{hyvev1alpha1.RoleAdmin, []string{hyvev1alpha1.RoleSuperadmin}, false},
		{hyvev1alpha1.RoleReadOnly, []string{hyvev1alpha1.RoleSuperadmin}, false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
		req = req.WithContext(contextWithRole(req.Context(), tc.role))
		rec := httptest.NewRecorder()

		ok := RequireRole(rec, req, tc.allowed...)
		assert.Equal(t, tc.expectOK, ok, "role=%s allowed=%v", tc.role, tc.allowed)
		if !tc.expectOK {
			assert.Equal(t, http.StatusForbidden, rec.Code)
		}
	}
}

// TestResolveAccess covers how requireRole picks the organization a
// request acts in: from the caller's memberships, a selection header, or
// a superadmin's reach into any organization.
func TestResolveAccess(t *testing.T) {
	s := newTestServer(t)
	ctx := t.Context()
	_, err := s.OrgStore.CreateOrganization(ctx, orgdb.Organization{Name: "acme-co", Namespace: "acme"}) // renamed
	require.NoError(t, err)
	member := func(ns, identity, role string) {
		_, err := createAccount(ctx, s.OrgStore, orgdb.Binding{
			Namespace: ns, SubjectType: orgdb.SubjectTypeLocal, Identity: identity, Role: role,
			ServiceAccountName: orgdb.ServiceAccountNameForRole(role), ServiceAccountNamespace: ns,
		})
		require.NoError(t, err)
	}
	member(testNamespace, "root", hyvev1alpha1.RoleSuperadmin)
	member("acme", "alice", hyvev1alpha1.RoleAdmin)
	member("widget", "alice", hyvev1alpha1.RoleReadOnly)
	member("widget", "bob", hyvev1alpha1.RoleAdmin)
	member("widget", "root", hyvev1alpha1.RoleReadOnly) // a superadmin's own membership never lowers them

	for _, tc := range []struct {
		name, user, header, value string
		wantNS, wantRole          string
		wantStatus                int
	}{
		{name: "member of several, nothing selected: first by namespace", user: "alice", wantNS: "acme", wantRole: hyvev1alpha1.RoleAdmin},
		{name: "selected by organization name", user: "alice", header: organizationHeader, value: "acme-co", wantNS: "acme", wantRole: hyvev1alpha1.RoleAdmin},
		{name: "selected by namespace", user: "alice", header: organizationHeader, value: "widget", wantNS: "widget", wantRole: hyvev1alpha1.RoleReadOnly},
		{name: "not a member", user: "bob", header: organizationHeader, value: "acme-co", wantStatus: http.StatusForbidden},
		{name: "only organization", user: "bob", wantNS: "widget", wantRole: hyvev1alpha1.RoleAdmin},
		{name: "legacy act-as is superadmin-only", user: "bob", header: actAsNamespaceHeader, value: "acme", wantNS: "widget", wantRole: hyvev1alpha1.RoleAdmin},
		{name: "superadmin defaults to the control plane", user: "root", wantNS: testNamespace, wantRole: hyvev1alpha1.RoleSuperadmin},
		{name: "superadmin reaches any organization", user: "root", header: organizationHeader, value: "acme-co", wantNS: "acme", wantRole: hyvev1alpha1.RoleSuperadmin},
		{name: "superadmin legacy act-as", user: "root", header: actAsNamespaceHeader, value: "widget", wantNS: "widget", wantRole: hyvev1alpha1.RoleSuperadmin},
		{name: "no memberships at all", user: "ghost", wantStatus: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			ns, role, status, _ := s.resolveAccess(req, tc.user)
			assert.Equal(t, tc.wantStatus, status)
			assert.Equal(t, tc.wantNS, ns)
			assert.Equal(t, tc.wantRole, role)
		})
	}

	t.Run("a pre-redesign session's own namespace still selects it", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
		req = req.WithContext(contextWithNamespace(req.Context(), "widget"))
		ns, role, status, _ := s.resolveAccess(req, "alice")
		assert.Zero(t, status)
		assert.Equal(t, "widget", ns)
		assert.Equal(t, hyvev1alpha1.RoleReadOnly, role)
	})
}

func TestTenantNamespace_FallsBackToControlPlane(t *testing.T) {
	s := &Server{Namespace: testNamespace}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	require.Equal(t, testNamespace, s.TenantNamespace(req))
	req = req.WithContext(contextWithNamespace(req.Context(), "acme"))
	require.Equal(t, "acme", s.TenantNamespace(req))
}

func TestEmitClusterEvent_NilClientset_NoOp(t *testing.T) {
	// Must not panic — the only assertion possible for a deliberate no-op.
	emitClusterEvent(context.Background(), nil, "acme", "web", "AgentConnected", "hyve-agent connected")
}

func TestEmitClusterEvent_CreatesRealEvent(t *testing.T) {
	clientset := fake.NewClientset()
	emitClusterEvent(context.Background(), clientset, "acme", "web", "AgentConnected", "hyve-agent connected")

	list, err := clientset.CoreV1().Events("acme").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	ev := list.Items[0]
	assert.Equal(t, "AgentConnected", ev.Reason)
	assert.Equal(t, "hyve-agent connected", ev.Message)
	assert.Equal(t, "ClusterDefinition", ev.InvolvedObject.Kind)
	assert.Equal(t, "web", ev.InvolvedObject.Name)
	assert.Equal(t, "acme", ev.InvolvedObject.Namespace)
	assert.Equal(t, corev1.EventTypeNormal, ev.Type)
}
