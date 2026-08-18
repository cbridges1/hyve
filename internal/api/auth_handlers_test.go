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
)

func newTestServerWithUser(t *testing.T, username, password, role string) *Server {
	t.Helper()
	hash, err := HashPassword(password)
	require.NoError(t, err)

	bindingName := username + "-binding"
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: bindingName},
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
	assert.NotEmpty(t, resp.Token)

	subject, err := VerifyToken(s.SigningKey, resp.Token)
	require.NoError(t, err)
	assert.Equal(t, "cedric", subject)
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

func TestHandleLogout_AlwaysOK(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
