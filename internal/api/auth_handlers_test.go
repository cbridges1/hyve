package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newTestServerWithUser(t *testing.T, username, password, role string) *Server {
	t.Helper()
	hash, err := HashPassword(password)
	require.NoError(t, err)

	bindingName := username + "-binding"
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: bindingName, Namespace: testNamespace},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: username},
			Role:    role,
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: UserCredentialsSecretName(bindingName), Namespace: testNamespace},
		Data:       map[string][]byte{"password-hash": []byte(hash)},
	}
	return &Server{
		Client:     newFakeClient(t, binding, secret),
		Namespace:  testNamespace,
		SigningKey: []byte("test-signing-key"),
	}
}

func doLogin(s *Server, username, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)
	return rec
}

func TestHandleLogin_Success(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	rec := doLogin(s, "cedric", "correct-password")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp loginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.SessionToken)
	assert.NotEmpty(t, resp.AccessTokenExpiresAt)
	assert.NotEmpty(t, resp.SessionExpiresAt)

	subject, err := VerifyToken(s.SigningKey, resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "cedric", subject)
}

// TestHandleLogin_CreatesRevocableSession confirms a login actually
// persists a HyveSession object — the thing that makes logout a real
// revocation instead of the old no-op.
func TestHandleLogin_CreatesRevocableSession(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	rec := doLogin(s, "cedric", "correct-password")
	require.Equal(t, http.StatusOK, rec.Code)
	var resp loginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	name, _, ok := splitSessionToken(resp.SessionToken)
	require.True(t, ok)

	var list hyvev1alpha1.HyveSessionList
	require.NoError(t, s.Client.List(context.Background(), &list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, name, list.Items[0].Name)
	assert.Equal(t, "cedric", list.Items[0].Spec.Subject)
	assert.NotEmpty(t, list.Items[0].Spec.TokenHash)
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	rec := doLogin(s, "cedric", "wrong-password")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleLogin_UnknownUser(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	rec := doLogin(s, "nobody", "anything")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleLogin_SameErrorForUnknownUserAndWrongPassword(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	unknownUser := doLogin(s, "nobody", "anything")
	wrongPassword := doLogin(s, "cedric", "wrong-password")

	assert.Equal(t, unknownUser.Code, wrongPassword.Code)
	assert.JSONEq(t, unknownUser.Body.String(), wrongPassword.Body.String(),
		"login must not reveal whether a username exists")
}

func TestHandleLogin_MissingFields(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	rec := doLogin(s, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleLogin_InvalidBody(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleLogout_NoBodyStillOK(t *testing.T) {
	s := &Server{Client: newFakeClient(t)}
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestHandleLogout_ActuallyRevokesSession is the regression test for the
// old design's biggest gap — logout used to be a documented no-op with
// nothing to invalidate. It now deletes the HyveSession, and a refresh
// against it afterward must fail.
func TestHandleLogout_ActuallyRevokesSession(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)
	loginRec := doLogin(s, "cedric", "correct-password")
	var loginResp loginResponse
	require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &loginResp))

	logoutBody, _ := json.Marshal(sessionTokenRequest{SessionToken: loginResp.SessionToken})
	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", bytes.NewReader(logoutBody))
	logoutRec := httptest.NewRecorder()
	s.handleLogout(logoutRec, logoutReq)
	require.Equal(t, http.StatusOK, logoutRec.Code)

	name, _, _ := splitSessionToken(loginResp.SessionToken)
	err := s.Client.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, &hyvev1alpha1.HyveSession{})
	assert.True(t, apierrors.IsNotFound(err), "session object should be gone after logout")

	refreshBody, _ := json.Marshal(sessionTokenRequest{SessionToken: loginResp.SessionToken})
	refreshReq := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(refreshBody))
	refreshRec := httptest.NewRecorder()
	s.handleRefresh(refreshRec, refreshReq)
	assert.Equal(t, http.StatusUnauthorized, refreshRec.Code, "refresh must fail once the session is revoked")
}

func TestHandleRefresh_Success(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)
	loginRec := doLogin(s, "cedric", "correct-password")
	var loginResp loginResponse
	require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &loginResp))

	body, _ := json.Marshal(sessionTokenRequest{SessionToken: loginResp.SessionToken})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp refreshResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	subject, err := VerifyToken(s.SigningKey, resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "cedric", subject)
}

func TestHandleRefresh_WrongSecretRejected(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)
	loginRec := doLogin(s, "cedric", "correct-password")
	var loginResp loginResponse
	require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &loginResp))

	name, _, _ := splitSessionToken(loginResp.SessionToken)
	body, _ := json.Marshal(sessionTokenRequest{SessionToken: name + ".not-the-real-secret"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleRefresh_UnknownSessionRejected(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace, SigningKey: []byte("key")}
	body, _ := json.Marshal(sessionTokenRequest{SessionToken: "does-not-exist.some-secret"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleRefresh_ExpiredSessionRejected(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace, SigningKey: []byte("key")}
	session := &hyvev1alpha1.HyveSession{
		ObjectMeta: metav1.ObjectMeta{Name: "expired-session", Namespace: testNamespace},
		Spec: hyvev1alpha1.HyveSessionSpec{
			Subject:   "cedric",
			TokenHash: HashSessionSecret("the-secret"),
			ExpiresAt: metav1.NewTime(time.Now().Add(-time.Hour)),
		},
	}
	require.NoError(t, s.Client.Create(context.Background(), session))

	body, _ := json.Marshal(sessionTokenRequest{SessionToken: "expired-session.the-secret"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleRefresh_MalformedTokenRejected(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace, SigningKey: []byte("key")}
	for _, bad := range []string{"", "no-dot-at-all", ".leading-dot-empty-name", "trailing-dot."} {
		body, _ := json.Marshal(sessionTokenRequest{SessionToken: bad})
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleRefresh(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for malformed token %q", bad)
	}
}
