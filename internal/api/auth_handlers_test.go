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
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServerWithUser(t *testing.T, username, password, role string) *Server {
	t.Helper()
	hash, err := HashPassword(password)
	require.NoError(t, err)

	store := newTestOrgStore(t)
	_, err = store.CreateBinding(context.Background(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: username, Role: role,
		ServiceAccountName: orgdb.ServiceAccountNameForRole(role), ServiceAccountNamespace: testNamespace,
		PasswordHash: &hash,
	})
	require.NoError(t, err)
	return &Server{
		Client:     newFakeClient(t),
		OrgStore:   store,
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

func doLoginWithNamespace(s *Server, username, password, namespace string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password, "namespace": namespace})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)
	return rec
}

// TestHandleLogin_ResolvesRenamedOrganizationName proves resolveLoginNamespace's
// whole reason for existing: PATCH /organizations/{name}'s own rename
// (organizations.go) actually takes effect for login, not just display.
// After renaming "acme" to "acme-corp" (its own Namespace, "acme", never
// changes — see that handler's own doc comment), logging in with the *new*
// name resolves to the same real namespace the binding itself lives in.
func TestHandleLogin_ResolvesRenamedOrganizationName(t *testing.T) {
	hash, err := HashPassword("correct-password")
	require.NoError(t, err)
	store := newTestOrgStore(t)
	_, _, err = store.CreateOrganizationWithDefaults(t.Context(), orgdb.Organization{Name: "acme", Namespace: "acme"}, "", "")
	require.NoError(t, err)
	_, err = store.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: "acme", SubjectType: orgdb.SubjectTypeLocal, Identity: "alice", Role: hyvev1alpha1.RoleAdmin,
		ServiceAccountName: orgdb.ServiceAccountNameForRole(hyvev1alpha1.RoleAdmin), ServiceAccountNamespace: "acme",
		PasswordHash: &hash,
	})
	require.NoError(t, err)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace, SigningKey: []byte("test-signing-key")}

	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	require.NoError(t, store.RenameOrganization(t.Context(), org.ID, "acme-corp"))

	rec := doLoginWithNamespace(s, "alice", "correct-password", "acme-corp")
	require.Equal(t, http.StatusOK, rec.Code)
	var resp loginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	_, namespace, err := VerifyToken(s.SigningKey, resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "acme", namespace, "the token must carry the real namespace, never the renamable display name")

	// The original name/namespace value must keep working too — nothing
	// forces every client to learn about a rename immediately, and it's
	// still literally the organization's own real namespace.
	rec = doLoginWithNamespace(s, "alice", "correct-password", "acme")
	assert.Equal(t, http.StatusOK, rec.Code)
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

	subject, _, err := VerifyToken(s.SigningKey, resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "cedric", subject)
}

// TestHandleLogin_CreatesRevocableSession confirms a login actually
// persists a Session row (Milestone 10 Part D) — the thing that makes
// logout a real revocation instead of the old no-op.
func TestHandleLogin_CreatesRevocableSession(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)

	rec := doLogin(s, "cedric", "correct-password")
	require.Equal(t, http.StatusOK, rec.Code)
	var resp loginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	id, _, ok := splitSessionToken(resp.SessionToken)
	require.True(t, ok)

	sess, err := s.OrgStore.GetSession(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "cedric", sess.Subject)
	assert.NotEmpty(t, sess.TokenHash)
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
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t)}
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestHandleLogout_ActuallyRevokesSession is the regression test for the
// old design's biggest gap — logout used to be a documented no-op with
// nothing to invalidate. It now deletes the Session row, and a refresh
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

	id, _, _ := splitSessionToken(loginResp.SessionToken)
	_, err := s.OrgStore.GetSession(context.Background(), id)
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "session row should be gone after logout")

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
	subject, _, err := VerifyToken(s.SigningKey, resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "cedric", subject)
}

func TestHandleRefresh_WrongSecretRejected(t *testing.T) {
	s := newTestServerWithUser(t, "cedric", "correct-password", hyvev1alpha1.RoleAdmin)
	loginRec := doLogin(s, "cedric", "correct-password")
	var loginResp loginResponse
	require.NoError(t, json.Unmarshal(loginRec.Body.Bytes(), &loginResp))

	id, _, _ := splitSessionToken(loginResp.SessionToken)
	body, _ := json.Marshal(sessionTokenRequest{SessionToken: id + ".not-the-real-secret"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleRefresh_UnknownSessionRejected(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace, SigningKey: []byte("key")}
	body, _ := json.Marshal(sessionTokenRequest{SessionToken: "does-not-exist.some-secret"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleRefresh_ExpiredSessionRejected(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace, SigningKey: []byte("key")}
	sess, err := s.OrgStore.CreateSession(context.Background(), orgdb.Session{
		Subject:   "cedric",
		TokenHash: HashSessionSecret("the-secret"),
		ExpiresAt: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(sessionTokenRequest{SessionToken: sess.ID + ".the-secret"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleRefresh_MalformedTokenRejected(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace, SigningKey: []byte("key")}
	for _, bad := range []string{"", "no-dot-at-all", ".leading-dot-empty-name", "trailing-dot."} {
		body, _ := json.Marshal(sessionTokenRequest{SessionToken: bad})
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleRefresh(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for malformed token %q", bad)
	}
}
