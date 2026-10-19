package signin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

const ns = "hyve-system"

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, hyvev1alpha1.AddToScheme(s))
	return s
}

// newStore returns a Store on a fake client. allow decides
// SubjectAccessReviews.
func newStore(t *testing.T, allow func(*authorizationv1.SubjectAccessReview) bool, objs ...client.Object) (*Store, *clock) {
	t.Helper()
	b := fake.NewClientBuilder().WithScheme(newScheme(t)).
		WithStatusSubresource(&hyvev1alpha1.User{}).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if sar, ok := obj.(*authorizationv1.SubjectAccessReview); ok {
					sar.Status.Allowed = allow != nil && allow(sar)
					return nil
				}
				return c.Create(ctx, obj, opts...)
			},
		})
	clk := &clock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	return &Store{Client: b.Build(), Namespace: ns, Now: clk.Now}, clk
}

func addUser(t *testing.T, s *Store, name, password string, groups ...string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, s.Client.Create(ctx, &hyvev1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: "uid-" + "x"},
		Spec:       hyvev1alpha1.UserSpec{Groups: groups},
	}))
	require.NoError(t, s.SetPassword(ctx, name, password))
}

func bootstrapPassword(t *testing.T, s *Store) string {
	t.Helper()
	var sec corev1.Secret
	require.NoError(t, s.Client.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "bootstrap-secret"}, &sec))
	return string(sec.Data["bootstrapPassword"])
}

func TestBootstrapAdminMustChangePassword(t *testing.T) {
	s, _ := newStore(t, nil)
	ctx := context.Background()
	require.NoError(t, s.EnsureBootstrapAdmin(ctx, "bootstrap-secret"))
	require.NoError(t, s.EnsureBootstrapAdmin(ctx, "bootstrap-secret"), "second start is a no-op")

	pw := bootstrapPassword(t, s)
	require.Len(t, pw, 24)

	_, _, err := s.Login(ctx, "admin", pw, "")
	assert.ErrorIs(t, err, ErrMustChangePassword)

	_, _, err = s.Login(ctx, "admin", pw, "short")
	assert.ErrorIs(t, err, ErrInvalidNewPassword)

	_, _, err = s.Login(ctx, "admin", pw, pw)
	assert.ErrorIs(t, err, ErrInvalidNewPassword, "the new password must differ")

	tok, exp, err := s.Login(ctx, "admin", pw, "a-much-better-password")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(tok, SessionPrefix))
	assert.Equal(t, s.now().Add(DefaultSessionTTL), exp)

	_, _, err = s.Login(ctx, "admin", pw, "")
	assert.ErrorIs(t, err, ErrInvalidCredentials, "the bootstrap password no longer works")
	_, _, err = s.Login(ctx, "admin", "a-much-better-password", "")
	assert.NoError(t, err, "and the flag is cleared")

	var u hyvev1alpha1.User
	require.NoError(t, s.Client.Get(ctx, client.ObjectKey{Name: "admin"}, &u))
	assert.NotNil(t, u.Status.LastLogin)
}

func TestBootstrapUsesExistingSecret(t *testing.T) {
	s, _ := newStore(t, nil, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bootstrap-secret"},
		Data:       map[string][]byte{"bootstrapPassword": []byte("chosen-by-the-installer")},
	})
	ctx := context.Background()
	require.NoError(t, s.EnsureBootstrapAdmin(ctx, "bootstrap-secret"))
	_, _, err := s.Login(ctx, "admin", "chosen-by-the-installer", "")
	assert.ErrorIs(t, err, ErrMustChangePassword)
}

func TestLoginAndLookup(t *testing.T) {
	s, clk := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "alice", "correct-horse-battery", "ops", "dev")

	for _, tc := range []struct{ user, pw string }{
		{"alice", "wrong-password-here"},
		{"bob", "correct-horse-battery"},
		{"Not_A_Name", "correct-horse-battery"},
	} {
		_, _, err := s.Login(ctx, tc.user, tc.pw, "")
		assert.ErrorIs(t, err, ErrInvalidCredentials, tc.user)
	}

	tok, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)
	ident, err := s.Lookup(ctx, tok)
	require.NoError(t, err)
	assert.Equal(t, "hyve:alice", ident.KubeUser())
	assert.Equal(t, []string{"hyve:ops", "hyve:dev", "system:authenticated"}, ident.KubeGroups())

	// A token with the right ID but the wrong secret is rejected.
	id, _, _ := parseSessionToken(tok)
	forged := SessionPrefix + id + ":" + strings.Repeat("a", 52)
	_, err = s.Lookup(ctx, forged)
	assert.ErrorIs(t, err, ErrInvalidSession)

	// Expiry.
	clk.t = clk.t.Add(DefaultSessionTTL)
	_, err = s.Lookup(ctx, tok)
	assert.ErrorIs(t, err, ErrInvalidSession)
	require.NoError(t, s.DeleteExpiredSessions(ctx))
	var list corev1.SecretList
	require.NoError(t, s.Client.List(ctx, &list, client.HasLabels{labelSessionFor}))
	assert.Empty(t, list.Items)
}

func TestDisabledUser(t *testing.T) {
	s, clk := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "alice", "correct-horse-battery")
	tok, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)
	_, err = s.Lookup(ctx, tok)
	require.NoError(t, err)

	var u hyvev1alpha1.User
	require.NoError(t, s.Client.Get(ctx, client.ObjectKey{Name: "alice"}, &u))
	u.Spec.Disabled = true
	require.NoError(t, s.Client.Update(ctx, &u))

	_, _, err = s.Login(ctx, "alice", "correct-horse-battery", "")
	assert.ErrorIs(t, err, ErrInvalidCredentials)

	_, err = s.Lookup(ctx, tok)
	assert.NoError(t, err, "cached for up to 30s")
	clk.t = clk.t.Add(identityCacheTTL)
	_, err = s.Lookup(ctx, tok)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestLogoutAndRevoke(t *testing.T) {
	s, _ := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "alice", "correct-horse-battery")
	t1, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)
	t2, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)
	t3, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	require.NoError(t, s.Logout(ctx, t1))
	_, err = s.Lookup(ctx, t1)
	assert.ErrorIs(t, err, ErrInvalidSession)

	id2, _, _ := parseSessionToken(t2)
	_, err = s.Lookup(ctx, t3) // warm the cache; revocation must clear it
	require.NoError(t, err)
	require.NoError(t, s.RevokeSessions(ctx, "alice", id2))
	_, err = s.Lookup(ctx, t2)
	assert.NoError(t, err, "the kept session survives")
	_, err = s.Lookup(ctx, t3)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestPasswordSecretsOwnedByUser(t *testing.T) {
	s, _ := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "alice", "correct-horse-battery")
	var sec corev1.Secret
	require.NoError(t, s.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: "hyve-password-alice"}, &sec))
	require.Len(t, sec.OwnerReferences, 1)
	assert.Equal(t, "User", sec.OwnerReferences[0].Kind)
	assert.NotContains(t, string(sec.Data[keyHash]), "correct-horse-battery")
}

// --- HTTP ---

func postJSON(t *testing.T, h http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = "192.0.2.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func putJSON(t *testing.T, h http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e apiError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	return e.Code
}

func TestLoginEndpoint(t *testing.T) {
	s, _ := newStore(t, nil)
	addUser(t, s, "alice", "correct-horse-battery")
	h := NewServer(s, nil, "test").Handler()

	rec := postJSON(t, h, "/v1-public/login", "", LoginRequest{Type: LocalProviderType, Username: "alice", Password: "correct-horse-battery", ResponseType: "kubeconfig"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp LoginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, strings.HasPrefix(resp.Token, SessionPrefix))
	assert.Equal(t, "alice", resp.UserID)
	_, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	assert.NoError(t, err)

	rec = postJSON(t, h, "/v1-public/login", "", LoginRequest{Type: "githubProvider", Username: "alice", Password: "x"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Logout ends the session.
	rec = postJSON(t, h, "/v1/logout", resp.Token, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	_, err = s.Lookup(context.Background(), resp.Token)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestLoginRateLimit(t *testing.T) {
	s, _ := newStore(t, nil)
	addUser(t, s, "alice", "correct-horse-battery")
	h := NewServer(s, nil, "test").Handler()
	for range 5 {
		rec := postJSON(t, h, "/v1-public/login", "", LoginRequest{Username: "alice", Password: "wrong-wrong-wrong"})
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	rec := postJSON(t, h, "/v1-public/login", "", LoginRequest{Username: "alice", Password: "correct-horse-battery"})
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "locked out, even with the right password")
}

func TestMustChangePasswordEndpoint(t *testing.T) {
	s, _ := newStore(t, nil)
	ctx := context.Background()
	require.NoError(t, s.EnsureBootstrapAdmin(ctx, "bootstrap-secret"))
	pw := bootstrapPassword(t, s)
	h := NewServer(s, nil, "test").Handler()

	rec := postJSON(t, h, "/v1-public/login", "", LoginRequest{Username: "admin", Password: pw})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "MustChangePassword", code(t, rec))

	rec = postJSON(t, h, "/v1-public/login", "", LoginRequest{Username: "admin", Password: pw, NewPassword: "short"})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = postJSON(t, h, "/v1-public/login", "", LoginRequest{Username: "admin", Password: pw, NewPassword: "a-much-better-password"})
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestSetPasswordEndpoint(t *testing.T) {
	allow := func(sar *authorizationv1.SubjectAccessReview) bool {
		ra := sar.Spec.ResourceAttributes
		return sar.Spec.User == "hyve:admin" && ra.Resource == "users" && ra.Subresource == "password" && ra.Verb == "update"
	}
	s, _ := newStore(t, allow)
	ctx := context.Background()
	addUser(t, s, "admin", "admin-password-123")
	addUser(t, s, "alice", "correct-horse-battery")
	h := NewServer(s, nil, "test").Handler()
	admin, _, err := s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)
	alice, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)
	alice2, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	// Your own: the current password is required.
	rec := putJSON(t, h, "/v1/users/alice/password", alice, SetPasswordRequest{CurrentPassword: "nope", NewPassword: "another-good-password"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = putJSON(t, h, "/v1/users/alice/password", alice, SetPasswordRequest{CurrentPassword: "correct-horse-battery", NewPassword: "another-good-password"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err = s.Lookup(ctx, alice)
	assert.NoError(t, err, "the session that changed it survives")
	_, err = s.Lookup(ctx, alice2)
	assert.ErrorIs(t, err, ErrInvalidSession, "other sessions are signed out")

	// Someone else's: needs the RBAC right.
	rec = putJSON(t, h, "/v1/users/admin/password", alice, SetPasswordRequest{NewPassword: "taking-over-admin"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = putJSON(t, h, "/v1/users/alice/password", admin, SetPasswordRequest{NewPassword: "reset-by-the-admin"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err = s.Lookup(ctx, alice)
	assert.ErrorIs(t, err, ErrInvalidSession, "a reset signs the user out everywhere")
	_, _, err = s.Login(ctx, "alice", "reset-by-the-admin", "")
	assert.NoError(t, err)

	rec = putJSON(t, h, "/v1/users/nobody/password", admin, SetPasswordRequest{NewPassword: "reset-by-the-admin"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = putJSON(t, h, "/v1/users/alice/password", "hs-bogus", SetPasswordRequest{NewPassword: "reset-by-the-admin"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestProvidersAndInfo(t *testing.T) {
	s, _ := newStore(t, nil)
	h := NewServer(s, nil, "v9").Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1-public/authproviders", nil))
	assert.JSONEq(t, `{"type":"collection","data":[{"id":"local","type":"localProvider"}]}`, rec.Body.String())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1-public/hyve", nil))
	assert.JSONEq(t, `{"kind":"hyve-host","version":"v9"}`, rec.Body.String())
}

func TestLimiterWindow(t *testing.T) {
	clk := &clock{t: time.Unix(0, 0)}
	l := newLimiter(3, time.Minute, 5*time.Minute)
	l.Now = clk.Now
	l.Fail("k")
	l.Fail("k")
	clk.t = clk.t.Add(2 * time.Minute) // the first two fall out of the window
	l.Fail("k")
	assert.False(t, l.Blocked("k"))
	l.Fail("k")
	l.Fail("k")
	assert.True(t, l.Blocked("k"))
	clk.t = clk.t.Add(5 * time.Minute)
	assert.False(t, l.Blocked("k"))
}
