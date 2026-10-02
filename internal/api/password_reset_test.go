package api

import (
	"bytes"
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

func doRequestPasswordReset(s *Server, identifier, namespace string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(requestPasswordResetRequest{Identifier: identifier, Namespace: namespace})
	req := httptest.NewRequest(http.MethodPost, "/auth/request-password-reset", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleRequestPasswordReset(rec, req)
	return rec
}

func doResetPassword(s *Server, email, token, newPassword, namespace string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(resetPasswordRequest{Email: email, Token: token, NewPassword: newPassword, Namespace: namespace})
	req := httptest.NewRequest(http.MethodPost, "/auth/reset-password", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleResetPassword(rec, req)
	return rec
}

// TestHandleRequestPasswordReset_UnknownIdentifier_StillReports200 is the
// anti-enumeration regression test: a login endpoint's neighbor must not
// reveal which usernames/emails exist, mirroring Pangolin's own
// requestPasswordReset.ts stance exactly.
func TestHandleRequestPasswordReset_UnknownIdentifier_StillReports200(t *testing.T) {
	s := newTestServer(t)
	rec := doRequestPasswordReset(s, "no-such-user", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp requestPasswordResetResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Sent)
}

// TestHandleRequestPasswordReset_NoEmailOnFile_StillReports200 proves a
// real account with no email set (every pre-Milestone-9 binding) gets the
// same generic response — nothing to act on, not an error.
func TestHandleRequestPasswordReset_NoEmailOnFile_StillReports200(t *testing.T) {
	s := newTestServer(t)
	binding, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "no-email-user", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)

	rec := doRequestPasswordReset(s, "no-email-user", "")
	require.Equal(t, http.StatusOK, rec.Code)

	_, err = s.OrgStore.GetPasswordResetTokenByBindingID(t.Context(), binding.ID)
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "no token should be created for an account with no email")
}

// TestHandleRequestPasswordReset_RealAccount_CreatesToken proves the
// actual side effect: a real, single-use, hashed, TTL'd token row gets
// created for the matched binding. Email delivery itself isn't asserted
// here (no SMTP configured in this test Server — email.Send degrades to
// ErrNotConfigured, logged and swallowed, same as a real fresh install
// with no SMTP set up yet — see handleRequestPasswordReset's own doc
// comment for why that's expected, not a test gap).
func TestHandleRequestPasswordReset_RealAccount_CreatesToken(t *testing.T) {
	s := newTestServer(t)
	email := "alice@example.com"
	binding, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "alice", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &email,
	})
	require.NoError(t, err)

	rec := doRequestPasswordReset(s, "alice", "")
	require.Equal(t, http.StatusOK, rec.Code)

	token, err := s.OrgStore.GetPasswordResetTokenByBindingID(t.Context(), binding.ID)
	require.NoError(t, err, "a real token row must be created")
	assert.NotEmpty(t, token.TokenHash)
	assert.WithinDuration(t, time.Now().Add(passwordResetTokenTTL), token.ExpiresAt, time.Minute)

	// The raw token itself is never returned in the response — this test
	// server has no SMTP configured, so the token was logged, not
	// e-mailed, but the API response must not leak it either way.
	assert.NotContains(t, rec.Body.String(), token.TokenHash)
}

// TestHandleRequestPasswordReset_ByEmail proves the identifier accepts
// an email address too, same dual lookup as login.
func TestHandleRequestPasswordReset_ByEmail(t *testing.T) {
	s := newTestServer(t)
	email := "alice@example.com"
	binding, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "alice", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &email,
	})
	require.NoError(t, err)

	rec := doRequestPasswordReset(s, "alice@example.com", "")
	require.Equal(t, http.StatusOK, rec.Code)

	_, err = s.OrgStore.GetPasswordResetTokenByBindingID(t.Context(), binding.ID)
	assert.NoError(t, err)
}

// TestHandleRequestPasswordReset_SecondRequestReplacesToken proves at
// most one live token per binding — same "delete then recreate" shape
// CreatePasswordResetToken's own doc comment describes.
func TestHandleRequestPasswordReset_SecondRequestReplacesToken(t *testing.T) {
	s := newTestServer(t)
	email := "alice@example.com"
	binding, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "alice", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &email,
	})
	require.NoError(t, err)

	doRequestPasswordReset(s, "alice", "")
	first, err := s.OrgStore.GetPasswordResetTokenByBindingID(t.Context(), binding.ID)
	require.NoError(t, err)

	doRequestPasswordReset(s, "alice", "")
	second, err := s.OrgStore.GetPasswordResetTokenByBindingID(t.Context(), binding.ID)
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID, "a second request must mint a fresh token row, not reuse the first")
}

// setUpPasswordResetTarget creates a binding with an email and a
// deterministic (test-only) reset token, bypassing email delivery
// entirely — this file's own tests need the RAW token value, which the
// real flow only ever puts in an email/log line, never an API response
// (see TestHandleRequestPasswordReset_RealAccount_CreatesToken).
func setUpPasswordResetTarget(t *testing.T, s *Server, username, email, password string) (orgdb.Binding, string) {
	t.Helper()
	hash, err := HashPassword(password)
	require.NoError(t, err)
	binding, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: username, Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: &email, PasswordHash: &hash,
	})
	require.NoError(t, err)

	rawToken := "TESTTOK1"
	tokenHash, err := HashPassword(rawToken)
	require.NoError(t, err)
	_, err = s.OrgStore.CreatePasswordResetToken(t.Context(), orgdb.PasswordResetToken{
		BindingID: binding.ID, TokenHash: tokenHash, ExpiresAt: time.Now().Add(passwordResetTokenTTL),
	})
	require.NoError(t, err)
	return binding, rawToken
}

func TestHandleResetPassword_Success(t *testing.T) {
	s := newTestServer(t)
	binding, token := setUpPasswordResetTarget(t, s, "alice", "alice@example.com", "old-password")

	rec := doResetPassword(s, "alice@example.com", token, "new-password-123", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp resetPasswordResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Reset)

	updated, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "alice")
	require.NoError(t, err)
	require.NotNil(t, updated.PasswordHash)
	assert.True(t, VerifyPassword(*updated.PasswordHash, "new-password-123"))
	assert.False(t, VerifyPassword(*updated.PasswordHash, "old-password"))

	_, err = s.OrgStore.GetPasswordResetTokenByBindingID(t.Context(), binding.ID)
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "the token must be consumed (single-use)")
}

func TestHandleResetPassword_TokenIsSingleUse(t *testing.T) {
	s := newTestServer(t)
	_, token := setUpPasswordResetTarget(t, s, "alice", "alice@example.com", "old-password")

	rec := doResetPassword(s, "alice@example.com", token, "new-password-123", "")
	require.Equal(t, http.StatusOK, rec.Code)

	rec = doResetPassword(s, "alice@example.com", token, "another-password", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a second reset attempt with the same token must fail")
}

func TestHandleResetPassword_WrongToken(t *testing.T) {
	s := newTestServer(t)
	setUpPasswordResetTarget(t, s, "alice", "alice@example.com", "old-password")

	rec := doResetPassword(s, "alice@example.com", "WRONGTOK", "new-password-123", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	updated, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "alice")
	require.NoError(t, err)
	assert.True(t, VerifyPassword(*updated.PasswordHash, "old-password"), "password must be unchanged on a rejected reset")
}

func TestHandleResetPassword_ExpiredToken(t *testing.T) {
	s := newTestServer(t)
	binding, err := s.OrgStore.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, SubjectType: orgdb.SubjectTypeLocal, Identity: "alice", Role: hyvev1alpha1.RoleReadOnly,
		ServiceAccountName: "hyve-access-readonly", ServiceAccountNamespace: testNamespace, Email: strPtr("alice@example.com"),
	})
	require.NoError(t, err)
	tokenHash, err := HashPassword("TESTTOK1")
	require.NoError(t, err)
	_, err = s.OrgStore.CreatePasswordResetToken(t.Context(), orgdb.PasswordResetToken{
		BindingID: binding.ID, TokenHash: tokenHash, ExpiresAt: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)

	rec := doResetPassword(s, "alice@example.com", "TESTTOK1", "new-password-123", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleResetPassword_UnknownEmail(t *testing.T) {
	s := newTestServer(t)
	rec := doResetPassword(s, "no-such-user@example.com", "TESTTOK1", "new-password-123", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleResetPassword_RevokesOtherSessions proves the security
// posture Pangolin's own resetPassword.ts also has: a successful reset
// invalidates every other still-active session for that binding.
func TestHandleResetPassword_RevokesOtherSessions(t *testing.T) {
	s := newTestServer(t)
	_, token := setUpPasswordResetTarget(t, s, "alice", "alice@example.com", "old-password")

	sess, err := s.OrgStore.CreateSession(t.Context(), orgdb.Session{
		Subject: "alice", TenantNamespace: testNamespace, TokenHash: "deadbeef", ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	rec := doResetPassword(s, "alice@example.com", token, "new-password-123", "")
	require.Equal(t, http.StatusOK, rec.Code)

	_, err = s.OrgStore.GetSession(t.Context(), sess.ID)
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "a pre-existing session must be revoked by a successful reset")
}

func TestHandleResetPassword_MissingFields_400(t *testing.T) {
	s := newTestServer(t)
	rec := doResetPassword(s, "", "", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
