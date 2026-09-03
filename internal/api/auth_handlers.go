package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// constantTimeEqual reports whether a and b are equal, in time independent
// of where they first differ — guards HashSessionSecret comparisons
// against a timing side-channel, same reasoning as any secret comparison.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse carries two distinct credentials — see HyveSession's own
// doc comment for why they're different in kind, not just in TTL:
// AccessToken is what's actually sent on every /api/* request (stateless,
// short-lived, verified locally); SessionToken is the longer-lived,
// revocable credential POST /auth/refresh consumes to mint new access
// tokens without the caller ever re-entering a password. SessionToken has
// the shape "<HyveSession object name>.<raw secret>" — the name half is an
// O(1) lookup key, the secret half is what's actually checked against the
// object's stored hash.
type loginResponse struct {
	AccessToken          string `json:"accessToken"`
	AccessTokenExpiresAt string `json:"accessTokenExpiresAt"`
	SessionToken         string `json:"sessionToken"`
	SessionExpiresAt     string `json:"sessionExpiresAt"`
}

// handleLogin authenticates a local (username/password) identity, creates
// a HyveSession object recording the login, and issues both halves of
// loginResponse. OIDC login (a browser redirect flow) is not implemented —
// see HyveAccessBindingSubject.Type's doc comment, SubjectTypeOIDC is
// reserved for later — local auth is the only login path today.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	binding, err := FindBindingBySubject(r.Context(), s.Client, s.Namespace, hyvev1alpha1.SubjectTypeLocal, req.Username)
	if err != nil {
		// Deliberately the same error as a wrong password below — a login
		// endpoint shouldn't reveal which usernames exist.
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	hash, err := LoadPasswordHash(r.Context(), s.Client, s.Namespace, binding.Name)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	if !VerifyPassword(hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	resp, err := s.issueSession(r.Context(), req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// issueSession creates a new HyveSession object for subject and returns
// both the access token and session token halves of loginResponse — shared
// by handleLogin (a fresh session) and used as the template for what
// handleRefresh returns (an existing session's new access token; the
// session token itself is not reissued — see handleRefresh).
func (s *Server) issueSession(ctx context.Context, subject string) (loginResponse, error) {
	secret, err := GenerateSessionSecret()
	if err != nil {
		return loginResponse{}, err
	}
	expiresAt := metav1.NewTime(time.Now().Add(SessionTTL))

	session := &hyvev1alpha1.HyveSession{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "session-",
			Namespace:    s.Namespace,
		},
		Spec: hyvev1alpha1.HyveSessionSpec{
			Subject:   subject,
			TokenHash: HashSessionSecret(secret),
			ExpiresAt: expiresAt,
		},
	}
	if err := s.Client.Create(ctx, session); err != nil {
		return loginResponse{}, err
	}

	accessToken, err := IssueAccessToken(s.SigningKey, subject)
	if err != nil {
		return loginResponse{}, err
	}

	return loginResponse{
		AccessToken:          accessToken,
		AccessTokenExpiresAt: time.Now().Add(AccessTokenTTL).Format(time.RFC3339),
		SessionToken:         session.Name + "." + secret,
		SessionExpiresAt:     expiresAt.Format(time.RFC3339),
	}, nil
}

// sessionTokenRequest is POST /auth/refresh and POST /auth/logout's shared
// body shape — both operate on a HyveSession identified by SessionToken,
// not the Authorization header (an access token payload carries no session
// identifier — see tokenPayload — so there'd be nothing to look up from it
// alone).
type sessionTokenRequest struct {
	SessionToken string `json:"sessionToken"`
}

// splitSessionToken parses "<HyveSession object name>.<raw secret>" — safe
// to split on the first '.' since GenerateName's suffix (lowercase
// alphanumerics only) and GenerateSessionSecret's base64url output (also
// no '.') never contain one.
func splitSessionToken(token string) (name, secret string, ok bool) {
	idx := strings.IndexByte(token, '.')
	if idx <= 0 || idx == len(token)-1 {
		return "", "", false
	}
	return token[:idx], token[idx+1:], true
}

// refreshResponse carries only a new access token — the session token
// itself is never reissued by a refresh (see handleRefresh).
type refreshResponse struct {
	AccessToken          string `json:"accessToken"`
	AccessTokenExpiresAt string `json:"accessTokenExpiresAt"`
}

// handleRefresh exchanges a still-valid session token for a new access
// token, without the caller re-entering a username/password — this is what
// makes unattended/automated use of hyve's API practical (see
// AccessTokenTTL/SessionTTL's own doc comments). The session token itself
// is deliberately not rotated/reissued here: it stays valid until its own
// ExpiresAt or an explicit POST /auth/logout, whichever comes first.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req sessionTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, secret, ok := splitSessionToken(req.SessionToken)
	if !ok {
		writeError(w, http.StatusBadRequest, "malformed session token")
		return
	}

	var session hyvev1alpha1.HyveSession
	if err := s.Client.Get(r.Context(), client.ObjectKey{Namespace: s.Namespace, Name: name}, &session); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}
	if time.Now().After(session.Spec.ExpiresAt.Time) {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}
	if !constantTimeEqual(HashSessionSecret(secret), session.Spec.TokenHash) {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}

	accessToken, err := IssueAccessToken(s.SigningKey, session.Spec.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue access token")
		return
	}
	writeJSON(w, http.StatusOK, refreshResponse{
		AccessToken:          accessToken,
		AccessTokenExpiresAt: time.Now().Add(AccessTokenTTL).Format(time.RFC3339),
	})
}

// handleLogout deletes the HyveSession named by the presented session
// token — real, immediate revocation, unlike the old fully-stateless
// design this replaces. Best-effort and always reports success: a missing/
// malformed session token or an already-gone session isn't an error from
// the caller's perspective, since the end state ("this session no longer
// works") is identical either way. Any access token already cached from
// this session keeps working until its own short AccessTokenTTL lapses —
// there's no cheaper way to invalidate an already-issued stateless token,
// see IssueAccessToken's doc comment.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req sessionTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
		if name, _, ok := splitSessionToken(req.SessionToken); ok {
			session := &hyvev1alpha1.HyveSession{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace}}
			if err := s.Client.Delete(r.Context(), session); err != nil && !apierrors.IsNotFound(err) {
				writeError(w, http.StatusInternalServerError, "failed to revoke session")
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}
