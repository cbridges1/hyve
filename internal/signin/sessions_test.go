package signin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
)

func login(t *testing.T, h http.Handler, user, password, agent string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	b, err := json.Marshal(LoginRequest{Username: user, Password: password})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1-public/login", bytes.NewReader(b))
	req.Header.Set("User-Agent", agent)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out LoginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Token
}

func TestSessions(t *testing.T) {
	admins := func(sar *authorizationv1.SubjectAccessReview) bool {
		return sar.Spec.User == "hyve:root" && sar.Spec.ResourceAttributes.Subresource == "sessions"
	}
	s, _ := newStore(t, admins)
	addUser(t, s, "alice", "alice-password-123")
	addUser(t, s, "bob", "bob-password-12345")
	addUser(t, s, "root", "root-password-1234")
	h := NewServer(s, nil, "test").Handler()

	laptop := login(t, h, "alice", "alice-password-123", "Mozilla/5.0 (Macintosh)")
	cli := login(t, h, "alice", "alice-password-123", "hyve/1.0")
	_, _, err := s.CreateToken(t.Context(), "alice", "ci", "", "alice", time.Hour)
	require.NoError(t, err)

	rec := do(t, h, http.MethodGet, "/v1/sessions", laptop)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list []SessionInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2, "API tokens aren't sessions")
	agents := map[string]bool{}
	for _, si := range list {
		agents[si.UserAgent] = si.Current
		assert.Equal(t, "session", si.Kind)
		assert.NotEmpty(t, si.Address)
	}
	assert.Equal(t, map[string]bool{"Mozilla/5.0 (Macintosh)": true, "hyve/1.0": false}, agents)

	// Bob can't see or end Alice's; an admin can.
	bob := login(t, h, "bob", "bob-password-12345", "x")
	assert.Equal(t, http.StatusForbidden, do(t, h, http.MethodGet, "/v1/sessions?user=alice", bob).Code)
	assert.Equal(t, http.StatusForbidden, do(t, h, http.MethodDelete, "/v1/sessions/alice", bob).Code)
	root := login(t, h, "root", "root-password-1234", "x")
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/v1/sessions?user=alice", root).Code)

	// Bob can't end Alice's session by naming it as his.
	var cliID string
	for _, si := range list {
		if !si.Current {
			cliID = si.ID
		}
	}
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodDelete, "/v1/sessions/bob/"+cliID, bob).Code)

	// Signing out the others keeps this one.
	assert.Equal(t, http.StatusNoContent, do(t, h, http.MethodDelete, "/v1/sessions/alice", laptop).Code)
	_, err = s.Lookup(t.Context(), cli)
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = s.Lookup(t.Context(), laptop)
	assert.NoError(t, err)
	tokens, err := s.ListTokens(t.Context(), "alice")
	require.NoError(t, err)
	assert.Len(t, tokens, 1, "tokens stay")

	// One by one, as an admin.
	rec = do(t, h, http.MethodGet, "/v1/sessions", laptop)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	assert.Equal(t, http.StatusNoContent, do(t, h, http.MethodDelete, "/v1/sessions/alice/"+list[0].ID, root).Code)
	_, err = s.Lookup(t.Context(), laptop)
	assert.ErrorIs(t, err, ErrInvalidSession)
}
