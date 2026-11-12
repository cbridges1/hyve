package signin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
)

func do(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTokenLifecycle(t *testing.T) {
	s, clk := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "ci", "ci-password-12345", "deployers")

	token, info, err := s.CreateToken(ctx, "ci", "github", "web's pipeline", "admin", 30*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, clk.t.Add(30*24*time.Hour), info.ExpiresAt)

	ident, err := s.Lookup(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "ci", ident.Username)
	assert.Equal(t, []string{"deployers"}, ident.Groups)
	assert.Equal(t, KindToken, ident.Kind)

	_, _, err = s.CreateToken(ctx, "ci", "github", "", "admin", time.Hour)
	assert.ErrorIs(t, err, ErrTokenExists)
	_, _, err = s.CreateToken(ctx, "ci", "forever", "", "admin", 2*MaxTokenTTL)
	assert.ErrorIs(t, err, ErrInvalidToken)
	_, _, err = s.CreateToken(ctx, "ci", "Bad Name", "", "admin", time.Hour)
	assert.ErrorIs(t, err, ErrInvalidToken)

	list, err := s.ListTokens(ctx, "ci")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "github", list[0].Name)
	assert.Equal(t, "web's pipeline", list[0].Description)
	assert.Equal(t, "admin", list[0].CreatedBy)

	// A password change signs sessions out, not tokens.
	session, _, err := s.Login(ctx, "ci", "ci-password-12345", "")
	require.NoError(t, err)
	require.NoError(t, s.RevokeSessions(ctx, "ci", ""))
	_, err = s.Lookup(ctx, session)
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = s.Lookup(ctx, token)
	assert.NoError(t, err)

	// Expired, it stops working, and cleanup removes it.
	clk.t = clk.t.Add(31 * 24 * time.Hour)
	_, err = s.Lookup(ctx, token)
	assert.ErrorIs(t, err, ErrInvalidSession)
	require.NoError(t, s.DeleteExpiredSessions(ctx))
	list, err = s.ListTokens(ctx, "ci")
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestRevokeToken(t *testing.T) {
	s, _ := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "ci", "ci-password-12345")
	token, _, err := s.CreateToken(ctx, "ci", "github", "", "ci", time.Hour)
	require.NoError(t, err)
	_, err = s.Lookup(ctx, token) // cached
	require.NoError(t, err)
	require.NoError(t, s.RevokeToken(ctx, "ci", "github"))
	_, err = s.Lookup(ctx, token)
	assert.ErrorIs(t, err, ErrInvalidSession, "a revoked token stops at once, cache or not")
	assert.ErrorIs(t, s.RevokeToken(ctx, "ci", "github"), ErrNoToken)
}

// A session started from a token (hyve ui's) ends with it.
func TestSessionFromTokenEndsWithIt(t *testing.T) {
	s, clk := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "ci", "ci-password-12345")
	token, _, err := s.CreateToken(ctx, "ci", "short", "", "ci", time.Hour)
	require.NoError(t, err)
	ident, err := s.Lookup(ctx, token)
	require.NoError(t, err)
	_, expiresAt, err := s.NewSession(ctx, ident)
	require.NoError(t, err)
	assert.Equal(t, clk.t.Add(time.Hour), expiresAt)
}

// A session handed on from a sign-in (hyve ui, hyve login through the
// browser) gets the full TTL, not what's left of the first.
func TestSessionFromSignInGetsFullTTL(t *testing.T) {
	s, clk := newStore(t, nil)
	ctx := context.Background()
	addUser(t, s, "alice", "correct-horse-battery")
	token, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)
	clk.t = clk.t.Add(10 * time.Hour)
	ident, err := s.Lookup(ctx, token)
	require.NoError(t, err)
	_, expiresAt, err := s.NewSession(ctx, ident)
	require.NoError(t, err)
	assert.Equal(t, clk.t.Add(DefaultSessionTTL), expiresAt)
}

func TestTokenEndpoints(t *testing.T) {
	allow := func(sar *authorizationv1.SubjectAccessReview) bool {
		ra := sar.Spec.ResourceAttributes
		return sar.Spec.User == "hyve:admin" && ra.Resource == "users" && ra.Subresource == "tokens"
	}
	s, _ := newStore(t, allow)
	ctx := context.Background()
	addUser(t, s, "admin", "admin-password-123")
	addUser(t, s, "alice", "correct-horse-battery")
	addUser(t, s, "ci", "ci-password-12345")
	h := NewServer(s, nil, "test").Handler()
	admin, _, err := s.Login(ctx, "admin", "admin-password-123", "")
	require.NoError(t, err)
	alice, _, err := s.Login(ctx, "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	// Your own.
	rec := postJSON(t, h, "/v1/tokens", alice, CreateTokenRequest{Name: "laptop", ExpiresIn: "7d"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var made CreateTokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &made))
	assert.Equal(t, "alice", made.User)
	assert.True(t, IsSessionToken(made.Token))

	// A token can't make tokens.
	rec = postJSON(t, h, "/v1/tokens", made.Token, CreateTokenRequest{Name: "another"})
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Someone else's needs users/tokens.
	rec = postJSON(t, h, "/v1/tokens", alice, CreateTokenRequest{User: "ci", Name: "github"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = postJSON(t, h, "/v1/tokens", admin, CreateTokenRequest{User: "ci", Name: "github", ExpiresIn: "90d"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = postJSON(t, h, "/v1/tokens", admin, CreateTokenRequest{User: "ci", Name: "github"})
	assert.Equal(t, http.StatusConflict, rec.Code)
	rec = postJSON(t, h, "/v1/tokens", admin, CreateTokenRequest{User: "ci", Name: "long", ExpiresIn: "5y"})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	rec = postJSON(t, h, "/v1/tokens", admin, CreateTokenRequest{User: "nobody", Name: "x"})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, h, http.MethodGet, "/v1/tokens?user=ci", alice)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = do(t, h, http.MethodGet, "/v1/tokens?all=true", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	var all []TokenInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &all))
	assert.Len(t, all, 2)
	rec = do(t, h, http.MethodGet, "/v1/tokens", alice)
	var mine []TokenInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mine))
	require.Len(t, mine, 1)
	assert.Equal(t, "laptop", mine[0].Name)

	rec = do(t, h, http.MethodDelete, "/v1/tokens/ci/github", alice)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = do(t, h, http.MethodDelete, "/v1/tokens/ci/github", admin)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = do(t, h, http.MethodDelete, "/v1/tokens/alice/laptop", made.Token)
	assert.Equal(t, http.StatusNoContent, rec.Code, "a token can revoke itself")
}

func TestParseTokenTTL(t *testing.T) {
	for in, want := range map[string]time.Duration{"": DefaultTokenTTL, "90d": 90 * 24 * time.Hour, "1y": 365 * 24 * time.Hour, "12h": 12 * time.Hour} {
		got, err := ParseTokenTTL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"soon", "-1d", "0d", "d"} {
		_, err := ParseTokenTTL(in)
		assert.Error(t, err, in)
	}
}
