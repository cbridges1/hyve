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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestHandleListAccounts_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodGet, "/accounts", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleListAccounts_ExcludesOIDCBindings(t *testing.T) {
	local := newBinding("cedric", "cedric", hyvev1alpha1.RoleAdmin)
	oidc := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "okta-someone", Namespace: testNamespace},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeOIDC, Value: "someone@example.com"},
			Role:    hyvev1alpha1.RoleReadOnly,
		},
	}
	s := &Server{Client: newFakeClient(t, local, oidc), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var accounts []accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &accounts))
	require.Len(t, accounts, 1)
	assert.Equal(t, "cedric", accounts[0].Username)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, accounts[0].Role)
}

// TestHandleListAccounts_ExcludesOtherNamespaces is the regression test for
// the cross-tenant leak namespacing HyveAccessBinding closes: an install
// serving `testNamespace` must never list another tenant's accounts.
func TestHandleListAccounts_ExcludesOtherNamespaces(t *testing.T) {
	mine := newBinding("cedric", "cedric", hyvev1alpha1.RoleAdmin)
	other := newBindingInNamespace("someone-else", "tenant-b", "someone-else", hyvev1alpha1.RoleAdmin)
	s := &Server{Client: newFakeClient(t, mine, other), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodGet, "/accounts", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var accounts []accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &accounts))
	require.Len(t, accounts, 1)
	assert.Equal(t, "cedric", accounts[0].Username)
}

func TestHandleCreateAccount_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "pw", Role: hyvev1alpha1.RoleAdmin})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateAccount_CreatesSecretAndBinding(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "s3cret", Role: hyvev1alpha1.RoleReadOnly})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto accountDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "new-user", dto.Username)
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, dto.Role)
	assert.NotContains(t, rec.Body.String(), "s3cret", "the plaintext password must never appear in the response")

	var binding hyvev1alpha1.HyveAccessBinding
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Name: "new-user", Namespace: testNamespace}, &binding))
	assert.Equal(t, "hyve-access-readonly", binding.Spec.ServiceAccountRef.Name)

	var secret corev1.Secret
	require.NoError(t, s.Client.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "new-user-credentials"}, &secret))
	// The handler writes via Secret.StringData (matching cmd/api/create_user.go's
	// existing convention) — a real API server merges that into .Data
	// (base64-encoded) on write, which is what LoadPasswordHash reads back
	// in production. The fake client used here doesn't perform that same
	// merge, so fall back to StringData directly rather than asserting a
	// fake-client fidelity gap that doesn't reflect real behavior.
	hash := string(secret.Data[passwordHashDataKey])
	if hash == "" {
		hash = secret.StringData[passwordHashDataKey]
	}
	require.NotEmpty(t, hash)
	assert.True(t, VerifyPassword(hash, "s3cret"))
	assert.False(t, VerifyPassword(hash, "wrong-password"))
}

func TestHandleCreateAccount_DuplicateUsername_Conflict(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newBinding("existing", "existing", hyvev1alpha1.RoleAdmin)), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "existing", Password: "pw", Role: hyvev1alpha1.RoleReadOnly})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleCreateAccount_InvalidRole_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Password: "pw", Role: "custom"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateAccount_MissingFields_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "new-user", Role: hyvev1alpha1.RoleAdmin})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleDeleteAccount_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doAccountRequest(t, s, "someone", hyvev1alpha1.RoleReadOnly, http.MethodDelete, "/accounts/x", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleDeleteAccount_CannotDeleteSelf(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newBinding("cedric", "cedric", hyvev1alpha1.RoleAdmin)), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "cedric", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/cedric", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var binding hyvev1alpha1.HyveAccessBinding
	assert.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Name: "cedric", Namespace: testNamespace}, &binding), "binding must survive a rejected self-delete")
}

func TestHandleDeleteAccount_RemovesBindingAndSecret(t *testing.T) {
	binding := newBinding("victim", "victim", hyvev1alpha1.RoleReadOnly)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "victim-credentials", Namespace: testNamespace}, Data: map[string][]byte{passwordHashDataKey: []byte("hash")}}
	s := &Server{Client: newFakeClient(t, binding, secret), Namespace: testNamespace}

	rec := doAccountRequest(t, s, "admin-caller", hyvev1alpha1.RoleAdmin, http.MethodDelete, "/accounts/victim", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	var gone hyvev1alpha1.HyveAccessBinding
	err := s.Client.Get(t.Context(), client.ObjectKey{Name: "victim", Namespace: testNamespace}, &gone)
	assert.True(t, apierrors.IsNotFound(err))

	var goneSecret corev1.Secret
	err = s.Client.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "victim-credentials"}, &goneSecret)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestHandleDeleteAccount_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
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
// test for the one carve-out HYVE-MULTI-TENANCY-PLAN.md's "New endpoint:
// POST /environments" section calls for: a superadmin has no namespace of
// their own (RoleSuperadmin's whole point), so they must be able to target
// an explicit tenant namespace — e.g. creating a brand new tenant's first
// admin right after POST /environments.
func TestHandleCreateAccount_SuperadminExplicitNamespace(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doAccountRequestAs(t, s, "", hyvev1alpha1.RoleSuperadmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "acme-admin", Password: "s3cret", Role: hyvev1alpha1.RoleAdmin, Namespace: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var binding hyvev1alpha1.HyveAccessBinding
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Name: "acme-admin", Namespace: "acme"}, &binding),
		"the binding must land in the explicitly-requested namespace, not the control-plane namespace")

	var secret corev1.Secret
	require.NoError(t, s.Client.Get(t.Context(), types.NamespacedName{Namespace: "acme", Name: "acme-admin-credentials"}, &secret))
}

// TestHandleCreateAccount_OrdinaryAdminCannotTargetOtherNamespace proves the
// carve-out is superadmin-only: an ordinary tenant admin passing an
// explicit Namespace for some OTHER tenant must be silently confined to
// their own namespace instead, exactly as before this field existed.
func TestHandleCreateAccount_OrdinaryAdminCannotTargetOtherNamespace(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doAccountRequestAs(t, s, "tenant-a", hyvev1alpha1.RoleAdmin, http.MethodPost, "/accounts",
		createAccountRequest{Username: "sneaky", Password: "s3cret", Role: hyvev1alpha1.RoleAdmin, Namespace: "tenant-b"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var binding hyvev1alpha1.HyveAccessBinding
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Name: "sneaky", Namespace: "tenant-a"}, &binding),
		"an ordinary admin's explicit Namespace field must be ignored — the account belongs in their own session namespace")

	err := s.Client.Get(t.Context(), client.ObjectKey{Name: "sneaky", Namespace: "tenant-b"}, &hyvev1alpha1.HyveAccessBinding{})
	assert.True(t, apierrors.IsNotFound(err), "must not have been created in the requested-but-unauthorized namespace")
}
