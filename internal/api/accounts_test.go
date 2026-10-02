package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAccountsTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAccountRoutes(mux)
	return mux
}

func doAccountRequest(t *testing.T, s *Server, caller, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	if caller != "" {
		ctx = contextWithUsername(ctx, caller)
	}
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	newAccountsTestMux(s).ServeHTTP(rec, req)
	return rec
}

// newTestServer builds a Server whose TenantNamespace resolves to
// testNamespace with no Organization registered for it — the Phase-1,
// self-hosted single-tenant shape most of these tests exercise (see
// resolveResourceEnvironment's own doc comment): an ordinary admin/
// read-only binding lands with nil organization_id/environment_id here,
// exactly like a superadmin's, which is what makes testNamespace usable
// as a stand-in scope the same way the old CRD-based tests used it.
func newTestServer(t *testing.T) *Server {
	return &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
}

func TestHandleListAccounts_ReadOnlyForbidden(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodGet, "/accounts", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleListAccounts_ExcludesOIDCBindings(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeOIDC, Identity: "someone@example.com", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var accounts []accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &accounts))
	require.Len(t, accounts, 1)
	assert.Equal(t, "cedric", accounts[0].Username)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, accounts[0].Role)
}

// TestHandleListAccounts_ExcludesOtherOrganizations is the regression test
// for the cross-tenant leak organization scoping closes: an install
// serving `testNamespace` must never list another tenant's accounts.
func TestHandleListAccounts_ExcludesOtherOrganizations(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)
	newOrgWithEnvironments(t, s.OrgStore, "tenant-b", orgdb.DefaultEnvironmentName)
	newTestBinding(t, s.OrgStore, "tenant-b", "someone-else", hyvev1alpha1.RoleAdmin)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var accounts []accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &accounts))
	require.Len(t, accounts, 1)
	assert.Equal(t, "cedric", accounts[0].Username)
}

func TestHandleCreateAccount_ReadOnlyForbidden(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "pw", Role: hyvev1alpha1.RoleAdmin})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandleCreateAccount_CreatesBindingWithPasswordHash proves account
// creation writes a single Store row (Milestone 10 Part C — password_hash
// lives on the binding itself, no paired Kubernetes Secret anymore, see
// orgdb.Binding.PasswordHash's own doc comment).
func TestHandleCreateAccount_CreatesBindingWithPasswordHash(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "s3cret", Role: hyvev1alpha1.RoleReadOnly})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "new-user", dto.Username)
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, dto.Role)
	assert.NotContains(t, rec.Body.String(), "s3cret", "the plaintext password must never appear in the response")

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "new-user")
	require.NoError(t, err)
	assert.Equal(t, "hyve-access-readonly", binding.ServiceAccountName)
	assert.Nil(t, binding.OrganizationID, "no Organization exists for testNamespace, so this binding must land with nil org/env, exactly like a superadmin's")

	require.NotNil(t, binding.PasswordHash)
	assert.True(t, VerifyPassword(*binding.PasswordHash, "s3cret"))
	assert.False(t, VerifyPassword(*binding.PasswordHash, "wrong-password"))
}

// TestHandleCreateAccount_WithRealOrganization_ScopesBinding proves the
// other half: when a real Organization *does* exist for the target
// namespace, the binding gets a real organization_id/environment_id, not
// the nil-scope fallback the test above exercises.
func TestHandleCreateAccount_WithRealOrganization_ScopesBinding(t *testing.T) {
	s := newTestServer(t)
	newOrgWithEnvironments(t, s.OrgStore, "acme", orgdb.DefaultEnvironmentName)
	s.Namespace = "hyve-control-plane" // so "acme" != s.Namespace and resolves as a real organization

	rec := doAccountRequestAs(t, s, "acme", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "acme-user", Password: "s3cret", Role: hyvev1alpha1.RoleReadOnly})
	require.Equal(t, http.StatusCreated, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), "acme", orgdb.SubjectTypeLocal, "acme-user")
	require.NoError(t, err)
	require.NotNil(t, binding.OrganizationID)
	require.NotNil(t, binding.EnvironmentID)

	org, err := s.OrgStore.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	assert.Equal(t, org.ID, *binding.OrganizationID)
}

func TestHandleCreateAccount_DuplicateUsername_Conflict(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "existing", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "existing", Password: "pw", Role: hyvev1alpha1.RoleReadOnly})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleCreateAccount_InvalidRole_400(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "pw", Role: "custom"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateAccount_MissingFields_400(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Role: hyvev1alpha1.RoleAdmin})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleDeleteAccount_ReadOnlyForbidden(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodDelete, "/accounts/x", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleDeleteAccount_CannotDeleteSelf(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "cedric", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/cedric", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	_, err = s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	assert.NoError(t, err, "binding must survive a rejected self-delete")
}

// TestHandleDeleteAccount_RemovesBinding proves deletion removes the whole
// binding row — password_hash included, in the same write, unlike the old
// design's separate paired Secret (Milestone 10 Part C).
func TestHandleDeleteAccount_RemovesBinding(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("whatever")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/victim", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	_, err = s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	assert.ErrorIs(t, err, orgdb.ErrNotFound)
}

func TestHandleDeleteAccount_NotFound(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// doAccountRequestAs mirrors doAccountRequest but also sets the caller's
// own session namespace (via contextWithNamespace) — needed to exercise
// the superadmin explicit-namespace carve-out, where "the caller's own
// namespace" and "the namespace they're targeting" must be able to differ.
func doAccountRequestAs(t *testing.T, s *Server, callerNamespace, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	ctx = contextWithNamespace(ctx, callerNamespace)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	newAccountsTestMux(s).ServeHTTP(rec, req)
	return rec
}

// TestHandleCreateAccount_SuperadminExplicitNamespace is the regression
// test for the one carve-out a superadmin needs: they have no namespace of
// their own (RoleSuperadmin's whole point), so they must be able to target
// an explicit tenant namespace — e.g. creating a brand new tenant's first
// admin right after POST /organizations.
func TestHandleCreateAccount_SuperadminExplicitNamespace(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequestAs(t, s, "", hyvev1alpha1.RoleSuperadmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "acme-admin", Password: "s3cret", Role: hyvev1alpha1.RoleAdmin, Namespace: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), "acme", orgdb.SubjectTypeLocal, "acme-admin")
	require.NoError(t, err, "the binding must land in the explicitly-requested namespace, not the control-plane namespace")
	assert.Equal(t, hyvev1alpha1.RoleAdmin, binding.Role)
	require.NotNil(t, binding.PasswordHash)
	assert.True(t, VerifyPassword(*binding.PasswordHash, "s3cret"))
}

// TestHandleCreateAccount_OrdinaryAdminCannotTargetOtherNamespace proves the
// carve-out is superadmin-only: an ordinary tenant admin passing an
// explicit Namespace for some OTHER tenant must be silently confined to
// their own namespace instead, exactly as before this field existed.
func TestHandleCreateAccount_OrdinaryAdminCannotTargetOtherNamespace(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequestAs(t, s, "tenant-a", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "sneaky", Password: "s3cret", Role: hyvev1alpha1.RoleAdmin, Namespace: "tenant-b"})
	require.Equal(t, http.StatusCreated, rec.Code)

	_, err := s.findBindingBySubject(t.Context(), "tenant-a", orgdb.SubjectTypeLocal, "sneaky")
	assert.NoError(t, err, "an ordinary admin's explicit Namespace field must be ignored — the account belongs in their own session namespace")

	_, err = s.findBindingBySubject(t.Context(), "tenant-b", orgdb.SubjectTypeLocal, "sneaky")
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "must not have been created in the requested-but-unauthorized namespace")
}

// TestHandleCreateAccount_SuperadminCanCreateSuperadmin proves a superadmin
// can create another superadmin — the same "a role can create more of its
// own role" principle already true for admin/admin, not a new escalation.
func TestHandleCreateAccount_SuperadminCanCreateSuperadmin(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequestAs(t, s, "", hyvev1alpha1.RoleSuperadmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "second-super", Password: "s3cret", Role: hyvev1alpha1.RoleSuperadmin})
	require.Equal(t, http.StatusCreated, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "second-super")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleSuperadmin, binding.Role)
}

// TestHandleCreateAccount_OrdinaryAdminCannotCreateSuperadmin is the actual
// escalation-prevention boundary: only an existing superadmin may ever set
// role: superadmin, regardless of what namespace they're otherwise
// confined to.
func TestHandleCreateAccount_OrdinaryAdminCannotCreateSuperadmin(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "sneaky-super", Password: "s3cret", Role: hyvev1alpha1.RoleSuperadmin})
	assert.Equal(t, http.StatusForbidden, rec.Code)

	_, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "sneaky-super")
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "must not have been created at all")
}

// TestHandleCreateAccount_SuperadminCreation_IgnoresActAsNamespace is the
// regression test for the one wrinkle this design has to get right: a new
// superadmin's binding must always land in the control-plane namespace,
// never wherever the creating superadmin's "act as" selection currently
// points — otherwise the new account would be created somewhere login can
// never find it.
func TestHandleCreateAccount_SuperadminCreation_IgnoresActAsNamespace(t *testing.T) {
	s := newTestServer(t)

	rec := doAccountRequestAs(t, s, "acme", hyvev1alpha1.RoleSuperadmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "third-super", Password: "s3cret", Role: hyvev1alpha1.RoleSuperadmin, Namespace: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code)

	_, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "third-super")
	assert.NoError(t, err, "a superadmin binding must land in the control-plane namespace regardless of act-as or an explicit Namespace field")

	_, err = s.findBindingBySubject(t.Context(), "acme", orgdb.SubjectTypeLocal, "third-super")
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "must not have been created in the acted-as tenant namespace")
}

// TestHandleUpdateAccountPassword_SelfChangeSucceeds proves the core
// self-service path: correct currentPassword, new hash verifiable, old
// hash no longer works.
func TestHandleUpdateAccountPassword_SelfChangeSucceeds(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("old-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "cedric", hyvev1alpha1.RoleReadOnly, http.MethodPut, "/accounts/cedric/password",
		updateAccountPasswordRequest{CurrentPassword: "old-pw", NewPassword: "new-pw"})
	assert.Equal(t, http.StatusNoContent, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	require.NotNil(t, binding.PasswordHash)
	assert.True(t, VerifyPassword(*binding.PasswordHash, "new-pw"))
	assert.False(t, VerifyPassword(*binding.PasswordHash, "old-pw"))
}

// TestHandleUpdateAccountPassword_SelfChangeWrongCurrentPassword proves a
// self-change is refused, and leaves the stored hash untouched, when
// currentPassword doesn't match.
func TestHandleUpdateAccountPassword_SelfChangeWrongCurrentPassword(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("old-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "cedric", hyvev1alpha1.RoleReadOnly, http.MethodPut, "/accounts/cedric/password",
		updateAccountPasswordRequest{CurrentPassword: "wrong", NewPassword: "new-pw"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	assert.True(t, VerifyPassword(*binding.PasswordHash, "old-pw"), "hash must be untouched on a rejected self-change")
}

// TestHandleUpdateAccountPassword_SelfChangeRequiresCurrentPassword proves
// currentPassword is mandatory on the self-service path, not merely
// verified-when-present.
func TestHandleUpdateAccountPassword_SelfChangeRequiresCurrentPassword(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("old-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "cedric", hyvev1alpha1.RoleReadOnly, http.MethodPut, "/accounts/cedric/password",
		updateAccountPasswordRequest{NewPassword: "new-pw"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleUpdateAccountPassword_ReadOnlyCanChangeOwn proves the self-
// service branch is reachable by every role, not just admin/superadmin —
// the whole point of not gating it behind RequireRole.
func TestHandleUpdateAccountPassword_ReadOnlyCanChangeOwn(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("old-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "viewer", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "viewer", hyvev1alpha1.RoleReadOnly, http.MethodPut, "/accounts/viewer/password",
		updateAccountPasswordRequest{CurrentPassword: "old-pw", NewPassword: "new-pw"})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// TestHandleUpdateAccountPassword_AdminResetsAnotherAccount proves the
// admin-driven reset path: no currentPassword required, admin role
// sufficient.
func TestHandleUpdateAccountPassword_AdminResetsAnotherAccount(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("old-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPut, "/accounts/victim/password",
		updateAccountPasswordRequest{NewPassword: "reset-pw"})
	assert.Equal(t, http.StatusNoContent, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	require.NoError(t, err)
	assert.True(t, VerifyPassword(*binding.PasswordHash, "reset-pw"))
}

// TestHandleUpdateAccountPassword_ReadOnlyCannotResetSomeoneElse proves the
// admin-driven branch still enforces RequireRole — a read-only caller
// cannot reset a different account's password.
func TestHandleUpdateAccountPassword_ReadOnlyCannotResetSomeoneElse(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "viewer", hyvev1alpha1.RoleReadOnly, http.MethodPut, "/accounts/someone-else/password",
		updateAccountPasswordRequest{NewPassword: "pw"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestHandleUpdateAccountPassword_AdminCannotResetSuperadmin mirrors the
// list/delete visibility rule: an ordinary admin gets "not found," not
// "forbidden," when targeting a superadmin account.
func TestHandleUpdateAccountPassword_AdminCannotResetSuperadmin(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("super-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "root-super", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPut, "/accounts/root-super/password",
		updateAccountPasswordRequest{NewPassword: "pw"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleUpdateAccountPassword_NotFound proves a nonexistent username
// on the admin-driven path reports not found rather than crashing on a nil
// PasswordHash or similar.
func TestHandleUpdateAccountPassword_NotFound(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPut, "/accounts/missing/password",
		updateAccountPasswordRequest{NewPassword: "pw"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleUpdateAccountPassword_SuperadminSelfChange_IgnoresActAsNamespace
// is the regression test for the one wrinkle this design has to get right:
// a superadmin's own binding always lives in s.Namespace, so a self-change
// while "Viewing" some tenant must still resolve against s.Namespace, not
// wherever act-as currently points — mirroring
// TestHandleCreateAccount_SuperadminCreation_IgnoresActAsNamespace.
func TestHandleUpdateAccountPassword_SuperadminSelfChange_IgnoresActAsNamespace(t *testing.T) {
	s := newTestServer(t)
	hash, err := HashPassword("old-pw")
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "root-super", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace, PasswordHash: &hash,
	})
	require.NoError(t, err)

	data, err := json.Marshal(updateAccountPasswordRequest{CurrentPassword: "old-pw", NewPassword: "new-pw"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/accounts/root-super/password", bytes.NewReader(data))
	ctx := contextWithRole(req.Context(), hyvev1alpha1.RoleSuperadmin)
	ctx = contextWithUsername(ctx, "root-super")
	ctx = contextWithNamespace(ctx, "acme")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	newAccountsTestMux(s).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "root-super")
	require.NoError(t, err, "the superadmin's own binding must still resolve against s.Namespace, not the act-as namespace")
	assert.True(t, VerifyPassword(*binding.PasswordHash, "new-pw"))
}

func TestHandleUpdateAccount_ReadOnlyForbidden(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/accounts/x",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleAdmin)})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateAccount_NoFields_400(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/x", updateAccountRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpdateAccount_NotFound(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/missing",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleAdmin)})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleUpdateAccount_InvalidRole_400(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Role: strPtr("custom")})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleUpdateAccount_PromoteReadOnlyToAdmin proves the common,
// same-namespace role change: read-only ("regular") -> admin in place,
// including the paired ServiceAccount name flip.
func TestHandleUpdateAccount_PromoteReadOnlyToAdmin(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleAdmin)})
	require.Equal(t, http.StatusOK, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, binding.Role)
	assert.Equal(t, "hyve-access-admin", binding.ServiceAccountName)
	assert.Equal(t, testNamespace, binding.Namespace, "an in-place admin<->read-only change must not move the binding's namespace")
}

func TestHandleUpdateAccount_DemoteAdminToReadOnly(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleReadOnly)})
	require.Equal(t, http.StatusOK, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, binding.Role)
	assert.Equal(t, "hyve-access-readonly", binding.ServiceAccountName)
}

// TestHandleUpdateAccount_CannotChangeOwnRole mirrors
// TestHandleDeleteAccount_CannotDeleteSelf — the same self-lockout guard,
// applied to role changes instead of deletion.
func TestHandleUpdateAccount_CannotChangeOwnRole(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "cedric", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "cedric", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/cedric",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleReadOnly)})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, binding.Role, "role must survive a rejected self-change")
}

// TestHandleUpdateAccount_OrdinaryAdminCannotPromoteToSuperadmin is the
// escalation-prevention boundary for role changes, mirroring
// TestHandleCreateAccount_OrdinaryAdminCannotCreateSuperadmin.
func TestHandleUpdateAccount_OrdinaryAdminCannotPromoteToSuperadmin(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleSuperadmin)})
	assert.Equal(t, http.StatusForbidden, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, binding.Role, "must not have been promoted")
}

// TestHandleUpdateAccount_SuperadminPromotesToSuperadmin proves the
// unambiguous direction: any existing tenant binding can be promoted to
// superadmin by a superadmin caller, with no destination namespace needed —
// a superadmin's home is always the control plane.
func TestHandleUpdateAccount_SuperadminPromotesToSuperadmin(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "super-caller", hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleSuperadmin)})
	require.Equal(t, http.StatusOK, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleSuperadmin, binding.Role)
	assert.Nil(t, binding.OrganizationID, "a superadmin binding must have no organization/environment")
	assert.Nil(t, binding.EnvironmentID)
}

// TestHandleUpdateAccount_DemoteSuperadmin_RequiresNamespace proves the
// reverse direction needs an explicit destination — there's no "current
// tenant" to infer one from for a binding that currently has none.
func TestHandleUpdateAccount_DemoteSuperadmin_RequiresNamespace(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "root-super", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "super-caller", hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/accounts/root-super",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleAdmin)})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpdateAccount_DemoteSuperadmin_WithNamespace(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "root-super", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "super-caller", hyvev1alpha1.RoleSuperadmin, http.MethodPatch, "/accounts/root-super",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleReadOnly), Namespace: "acme"})
	require.Equal(t, http.StatusOK, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), "acme", orgdb.SubjectTypeLocal, "root-super")
	require.NoError(t, err, "the binding must now live in the requested destination namespace")
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, binding.Role)
	assert.Equal(t, "hyve-access-readonly", binding.ServiceAccountName)

	_, err = s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "root-super")
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "must no longer be found at the old control-plane namespace")
}

// TestHandleUpdateAccount_OrdinaryAdminCannotDemoteSuperadmin proves the
// boundary check applies symmetrically — an ordinary admin can't reach a
// superadmin binding at all (visibility rule fires first), let alone
// change its role.
func TestHandleUpdateAccount_OrdinaryAdminCannotDemoteSuperadmin(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "root-super", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/root-super",
		updateAccountRequest{Role: strPtr(hyvev1alpha1.RoleReadOnly), Namespace: testNamespace})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleUpdateAccount_UpdatesEmail(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Email: strPtr("victim@example.com")})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.Email)
	assert.Equal(t, "victim@example.com", *dto.Email)
}

func TestHandleUpdateAccount_InvalidEmail_400(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Email: strPtr("not-an-email")})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpdateAccount_DuplicateEmail_Conflict(t *testing.T) {
	s := newTestServer(t)
	existingEmail := "taken@example.com"
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "existing", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &existingEmail,
	})
	require.NoError(t, err)
	_, err = s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Email: strPtr("taken@example.com")})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleUpdateAccount_ClearEmail(t *testing.T) {
	s := newTestServer(t)
	email := "victim@example.com"
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &email,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPatch, "/accounts/victim",
		updateAccountRequest{Email: strPtr("")})
	require.Equal(t, http.StatusOK, rec.Code)

	binding, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "victim")
	require.NoError(t, err)
	assert.Nil(t, binding.Email)
}

func TestHandleCreateAccount_WithEmail(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "s3cret", Role: hyvev1alpha1.RoleReadOnly, Email: "new-user@example.com"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.Email)
	assert.Equal(t, "new-user@example.com", *dto.Email)
}

func TestHandleCreateAccount_DuplicateEmail_Conflict(t *testing.T) {
	s := newTestServer(t)
	existingEmail := "taken@example.com"
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "existing", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &existingEmail,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "s3cret", Role: hyvev1alpha1.RoleReadOnly, Email: "taken@example.com"})
	assert.Equal(t, http.StatusConflict, rec.Code)

	_, err = s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "new-user")
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "must not have been created at all")
}

func TestHandleGetAccount_ReturnsAccount(t *testing.T) {
	s := newTestServer(t)
	email := "victim@example.com"
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "victim", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &email,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts/victim", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "victim", dto.Username)
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, dto.Role)
	require.NotNil(t, dto.Email)
	assert.Equal(t, email, *dto.Email)
}

func TestHandleGetAccount_NotFound(t *testing.T) {
	s := newTestServer(t)
	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetAccount_OrdinaryAdminCannotSeeSuperadmin(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "root-super", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts/root-super", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleCreateAccount_NotificationFailureDoesNotFailRequest proves
// the fire-and-forget stance sendAccountNotification's own doc comment
// describes: even a real (not just "not configured") send failure must
// never turn a successful account action into a failed API response —
// SMTP is configured here, but pointed at a port nothing listens on, so
// the send genuinely fails rather than short-circuiting on
// ErrNotConfigured.
func TestHandleCreateAccount_NotificationFailureDoesNotFailRequest(t *testing.T) {
	s := newTestServer(t)
	_, err := s.OrgStore.UpsertEmailSettings(t.Context(), orgdb.EmailSettings{
		SMTPHost: "127.0.0.1", SMTPPort: 1, FromAddress: "no-reply@example.com",
	})
	require.NoError(t, err)

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "s3cret", Role: hyvev1alpha1.RoleReadOnly, Email: "new-user@example.com"})
	assert.Equal(t, http.StatusCreated, rec.Code, "a real notification-send failure must not fail the underlying account creation")
}
