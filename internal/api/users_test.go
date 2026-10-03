package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"
)

// newMultiOrgServer: alice is admin of acme and read-only in widget, with
// one password; carol is admin of widget only.
func newMultiOrgServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServerWithUser(t, "root", "root-pw", hyvev1alpha1.RoleSuperadmin)
	hash, err := HashPassword("alice-pw")
	require.NoError(t, err)
	email := "alice@example.com"
	for _, m := range []struct{ ns, who, role string }{
		{"acme", "alice", hyvev1alpha1.RoleAdmin},
		{"widget", "alice", hyvev1alpha1.RoleReadOnly},
		{"widget", "carol", hyvev1alpha1.RoleAdmin},
	} {
		b := orgdb.Binding{Namespace: m.ns, SubjectType: orgdb.SubjectTypeLocal, Identity: m.who, Role: m.role,
			ServiceAccountName: orgdb.ServiceAccountNameForRole(m.role), ServiceAccountNamespace: m.ns}
		if m.who == "alice" {
			b.PasswordHash, b.Email = &hash, &email
		}
		_, err := createAccount(t.Context(), s.OrgStore, b)
		require.NoError(t, err)
	}
	return s
}

// whoamiWith logs in and calls GET /api/whoami through the real routes,
// optionally selecting an organization.
func whoamiWith(t *testing.T, s *Server, username, password, org string) (int, whoamiResponse) {
	t.Helper()
	rec := doLogin(s, username, password)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var login loginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &login))

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+login.AccessToken)
	if org != "" {
		req.Header.Set(organizationHeader, org)
	}
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	var who whoamiResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &who)
	return rec.Code, who
}

func TestOneLoginReachesEveryOrganization(t *testing.T) {
	s := newMultiOrgServer(t)

	code, who := whoamiWith(t, s, "alice", "alice-pw", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "acme", who.Namespace, "first organization by default")
	assert.Equal(t, hyvev1alpha1.RoleAdmin, who.Role)
	assert.Equal(t, []whoamiOrganization{
		{Name: "acme", Namespace: "acme", Role: hyvev1alpha1.RoleAdmin},
		{Name: "widget", Namespace: "widget", Role: hyvev1alpha1.RoleReadOnly},
	}, who.Organizations)

	code, who = whoamiWith(t, s, "alice", "alice-pw", "widget")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "widget", who.Namespace)
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, who.Role)
}

func TestLoginByEmail_NoOrganization(t *testing.T) {
	s := newMultiOrgServer(t)
	rec := doLogin(s, "alice@example.com", "alice-pw")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, http.StatusUnauthorized, doLogin(s, "alice", "wrong").Code)
}

func TestSelectingAnotherUsersOrganization_403(t *testing.T) {
	s := newMultiOrgServer(t)
	hash, err := HashPassword("carol-pw")
	require.NoError(t, err)
	carol, err := s.OrgStore.GetUserByUsername(t.Context(), "carol")
	require.NoError(t, err)
	require.NoError(t, s.OrgStore.SetUserPassword(t.Context(), carol.ID, hash))

	code, _ := whoamiWith(t, s, "carol", "carol-pw", "acme")
	assert.Equal(t, http.StatusForbidden, code)

	code, who := whoamiWith(t, s, "root", "root-pw", "acme")
	require.Equal(t, http.StatusOK, code, "a superadmin reaches any organization")
	assert.Equal(t, hyvev1alpha1.RoleSuperadmin, who.Role)
	require.NotEmpty(t, who.Organizations)
	assert.Equal(t, whoamiOrganization{Name: testNamespace, Namespace: testNamespace, Role: hyvev1alpha1.RoleSuperadmin, ControlPlane: true},
		who.Organizations[0], "the control plane is one entry, listed first")
}

func TestCreateAccount_AddsExistingUser(t *testing.T) {
	s := newMultiOrgServer(t)
	rec := doAccountRequestAs(t, s, "other", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "alice", Role: hyvev1alpha1.RoleReadOnly})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp createAccountResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.ExistingUser)
	assert.False(t, resp.EmailSent)
	require.NotNil(t, resp.Email)
	assert.Equal(t, "alice@example.com", *resp.Email)

	assert.True(t, VerifyPassword(*passwordHashOf(t, s.OrgStore, "alice"), "alice-pw"), "keeps their own password")

	rec = doAccountRequestAs(t, s, "other", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "alice", Role: hyvev1alpha1.RoleReadOnly})
	assert.Equal(t, http.StatusConflict, rec.Code, "already a member")

	rec = doAccountRequestAs(t, s, "other", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "dave", Role: hyvev1alpha1.RoleReadOnly})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a new user needs a password and an email")
}

func TestSharedUser_AdminCannotChangeCredentials(t *testing.T) {
	s := newMultiOrgServer(t)
	newEmail := "evil@example.com"
	rec := doAccountRequestAs(t, s, "acme", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/alice",
		updateAccountRequest{Email: &newEmail})
	assert.Equal(t, http.StatusForbidden, rec.Code, "alice also belongs to widget")

	body, _ := json.Marshal(updateAccountPasswordRequest{NewPassword: "x"})
	req := httptest.NewRequest(http.MethodPut, "/accounts/alice/password", bytes.NewReader(body))
	req = req.WithContext(contextWithNamespace(contextWithUsername(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin), "acme-admin"), "acme"))
	rec = httptest.NewRecorder()
	newAccountsTestMux(s).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.True(t, VerifyPassword(*passwordHashOf(t, s.OrgStore, "alice"), "alice-pw"))
	assert.Equal(t, "alice@example.com", *emailOf(t, s.OrgStore, "alice"))

	rec = doAccountRequestAs(t, s, "acme", hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/accounts/alice",
		updateAccountRequest{Email: &newEmail})
	assert.Equal(t, http.StatusOK, rec.Code, "a superadmin may")
}

func TestDeleteAccount_RemovesMembershipThenUser(t *testing.T) {
	s := newMultiOrgServer(t)
	rec := doAccountRequestAs(t, s, "acme", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/alice", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	_, err := s.OrgStore.GetUserByUsername(t.Context(), "alice")
	require.NoError(t, err, "still a member of widget")
	assert.Equal(t, http.StatusOK, doLogin(s, "alice", "alice-pw").Code)

	rec = doAccountRequestAs(t, s, "widget", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/alice", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	_, err = s.OrgStore.GetUserByUsername(t.Context(), "alice")
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "no memberships left: the user goes too")
	assert.Equal(t, http.StatusUnauthorized, doLogin(s, "alice", "alice-pw").Code)
}

func TestOrganizationFromAgentProxyPath(t *testing.T) {
	var gotPath, gotOrg string
	h := organizationFromAgentProxyPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotOrg = r.URL.Path, r.Header.Get(organizationHeader)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent-proxy/org/branlen/default-branclust/api/v1/pods", nil))
	assert.Equal(t, "/agent-proxy/default-branclust/api/v1/pods", gotPath)
	assert.Equal(t, "branlen", gotOrg)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent-proxy/web/api/v1/pods", nil))
	assert.Equal(t, "/agent-proxy/web/api/v1/pods", gotPath, "the old form passes through")
	assert.Empty(t, gotOrg)
}

func TestAgentProviderKubeconfig_NamesTheOrganization(t *testing.T) {
	p := &AgentProvider{PublicBaseURL: "https://hyve.example.com/"}
	ctx := context.WithValue(t.Context(), contextKeyToken, "tok")
	kc, err := p.Kubeconfig(ctx, &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "default-branclust", Namespace: "branlen"}})
	require.NoError(t, err)
	assert.Contains(t, string(kc), "server: https://hyve.example.com/api/agent-proxy/org/branlen/default-branclust")
}
