package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cbridges1/hyve/internal/orgdb"
)

// constantTimeEqual reports whether a and b are equal, in time independent
// of where they first differ — guards HashSessionSecret comparisons
// against a timing side-channel, same reasoning as any secret comparison.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

type loginRequest struct {
	// Username accepts either a binding's Identity (its actual username)
	// or its Email — see handleLogin's own lookup order. The wire field
	// name stays "username" for backward compatibility with every
	// existing caller (CLI included); email-or-username is purely an
	// additive relaxation of what value it accepts, not a new field.
	Username string `json:"username"`
	Password string `json:"password"`

	// Namespace is which tenant to log into — empty means the install's
	// own control-plane namespace (s.Namespace), the superadmin tier's
	// home. The CLI's --org flag passes this straight through unresolved
	// (see cmd/shared.ResolveOrgToNamespace's own doc comment) — despite
	// the field's name, a non-empty value here is resolved server-side
	// (see resolveLoginNamespace) as an organization's current Name first,
	// falling back to treating it as a literal Kubernetes namespace only
	// when no organization is registered under that name. That fallback is
	// what keeps this working unchanged for every organization that's
	// never been renamed (Name and Namespace start out equal at creation
	// and this is the only path that can make them diverge), and for
	// installs with no Organization rows registered at all.
	Namespace string `json:"namespace,omitempty"`
}

// loginResponse carries two distinct credentials — see orgdb.Session's own
// doc comment for why they're different in kind, not just in TTL:
// AccessToken is what's actually sent on every /api/* request (stateless,
// short-lived, verified locally); SessionToken is the longer-lived,
// revocable credential POST /auth/refresh consumes to mint new access
// tokens without the caller ever re-entering a password. SessionToken has
// the shape "<Session id>.<raw secret>" — the id half is an O(1) lookup
// key, the secret half is what's actually checked against the row's stored
// hash.
type loginResponse struct {
	AccessToken          string `json:"accessToken"`
	AccessTokenExpiresAt string `json:"accessTokenExpiresAt"`
	SessionToken         string `json:"sessionToken"`
	SessionExpiresAt     string `json:"sessionExpiresAt"`
}

// handleLogin authenticates a local (username/password) identity, creates
// a Session row recording the login, and issues both halves of
// loginResponse. OIDC login (a browser redirect flow) is not implemented —
// see orgdb.SubjectTypeOIDC's doc comment, reserved for later — local auth
// is the only login path today.
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

	resolvedNamespace := req.Namespace
	if resolvedNamespace != "" {
		resolvedNamespace = s.resolveLoginNamespace(r.Context(), resolvedNamespace)
	}
	ns := resolvedNamespace
	if ns == "" {
		ns = s.Namespace
	}

	// Username first (the common case, and unambiguous — see
	// bindings_namespace_env_identity), falling back to Email (unique
	// per-namespace — see bindings_namespace_email) only when that fails.
	// Either way the actual identity (binding.Identity) is what carries
	// forward into the session below, never the raw value the caller
	// typed — see issueSession's own call site for why that matters.
	binding, err := s.findBindingBySubject(r.Context(), ns, orgdb.SubjectTypeLocal, req.Username)
	if err != nil {
		binding, err = s.findBindingByEmail(r.Context(), ns, req.Username)
	}
	if err != nil {
		// Deliberately the same error as a wrong password below — a login
		// endpoint shouldn't reveal which usernames/emails exist.
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if binding.SubjectType != orgdb.SubjectTypeLocal {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// PasswordHash lives on the binding row itself (Milestone 10 Part C) —
	// nil here means either a pre-Part-C binding that was never migrated,
	// or (in principle) an OIDC binding, though findBindingBySubject above
	// already filtered to SubjectTypeLocal, so only the former is actually
	// reachable — either way, "no password set" and "wrong password" get
	// the identical response, same reasoning as the unknown-username case
	// above.
	if binding.PasswordHash == nil || !VerifyPassword(*binding.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// resolvedNamespace (possibly empty), not raw req.Namespace — the
	// session/access-token namespace must always be the real, immutable
	// Kubernetes namespace this binding actually resolved against, never
	// an organization's current (renamable) display Name, or a later
	// rename would silently point an already-issued session at the wrong
	// place. Empty stays empty rather than baking in today's s.Namespace
	// value — issueSession re-derives "empty means control-plane
	// namespace" itself on every use, so this stays correct even if
	// s.Namespace itself is ever reconfigured.
	resp, err := s.issueSession(r.Context(), binding.Identity, resolvedNamespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveLoginNamespace resolves ns — a non-empty loginRequest.Namespace,
// historically always a literal Kubernetes namespace and still accepted as
// one — to the real namespace a login should actually authenticate
// against. If an Organization is currently registered under that exact
// Name, its own (immutable) Namespace is used instead; otherwise ns is
// returned unchanged, treated as a literal namespace directly, exactly
// this function's predecessor's entire behavior before organization
// renaming existed. This one lookup is what makes PATCH
// /organizations/{name}'s own rename (see handlePatchOrganization) take
// effect for login, not just for display: a caller passing an org's new
// name resolves through to the correct underlying namespace, and a caller
// still passing its original name/namespace value (nothing forces every
// client to learn about a rename immediately) keeps working too, since
// Name and Namespace are always equal until the first rename ever happens.
func (s *Server) resolveLoginNamespace(ctx context.Context, ns string) string {
	if s.OrgStore == nil {
		return ns
	}
	if org, err := s.OrgStore.GetOrganizationByName(ctx, ns); err == nil {
		return org.Namespace
	}
	return ns
}

// issueSession creates a new Session row for subject (Milestone 10 Part D —
// see orgdb.Session's own doc comment; replaces the retired HyveSession
// CRD, Store-backed the same way sessions.go's own bindings already are)
// and returns both the access token and session token halves of
// loginResponse — shared by handleLogin (a fresh session) and used as the
// template for what handleRefresh returns (an existing session's new access
// token; the session token itself is not reissued — see handleRefresh).
func (s *Server) issueSession(ctx context.Context, subject, namespace string) (loginResponse, error) {
	secret, err := GenerateSessionSecret()
	if err != nil {
		return loginResponse{}, err
	}
	expiresAt := time.Now().Add(SessionTTL)

	session, err := s.OrgStore.CreateSession(ctx, orgdb.Session{
		Subject:         subject,
		TenantNamespace: namespace,
		TokenHash:       HashSessionSecret(secret),
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return loginResponse{}, err
	}

	accessToken, err := IssueAccessToken(s.SigningKey, subject, namespace)
	if err != nil {
		return loginResponse{}, err
	}

	return loginResponse{
		AccessToken:          accessToken,
		AccessTokenExpiresAt: time.Now().Add(AccessTokenTTL).Format(time.RFC3339),
		SessionToken:         session.ID + "." + secret,
		SessionExpiresAt:     expiresAt.Format(time.RFC3339),
	}, nil
}

// sessionTokenRequest is POST /auth/refresh and POST /auth/logout's shared
// body shape — both operate on a Session row identified by SessionToken,
// not the Authorization header (an access token payload carries no session
// identifier — see tokenPayload — so there'd be nothing to look up from it
// alone).
type sessionTokenRequest struct {
	SessionToken string `json:"sessionToken"`
}

// splitSessionToken parses "<Session id>.<raw secret>" — safe to split on
// the first '.' since a Session's id (a UUIDv4, see orgdb's newID) and
// GenerateSessionSecret's base64url output never contain one.
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
	id, secret, ok := splitSessionToken(req.SessionToken)
	if !ok {
		writeError(w, http.StatusBadRequest, "malformed session token")
		return
	}

	session, err := s.OrgStore.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}
	if time.Now().After(session.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}
	if !constantTimeEqual(HashSessionSecret(secret), session.TokenHash) {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}

	accessToken, err := IssueAccessToken(s.SigningKey, session.Subject, session.TenantNamespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue access token")
		return
	}
	writeJSON(w, http.StatusOK, refreshResponse{
		AccessToken:          accessToken,
		AccessTokenExpiresAt: time.Now().Add(AccessTokenTTL).Format(time.RFC3339),
	})
}

// handleLogout deletes the Session row named by the presented session
// token — real, immediate revocation, unlike the old fully-stateless
// design this replaces. Best-effort and always reports success: a missing/
// malformed session token or an already-gone session isn't an error from
// the caller's perspective, since the end state ("this session no longer
// works") is identical either way (see orgdb.Store.DeleteSession's own
// "missing row is not an error" stance). Any access token already cached
// from this session keeps working until its own short AccessTokenTTL
// lapses — there's no cheaper way to invalidate an already-issued
// stateless token, see IssueAccessToken's doc comment.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req sessionTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
		if id, _, ok := splitSessionToken(req.SessionToken); ok {
			if err := s.OrgStore.DeleteSession(r.Context(), id); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to revoke session")
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}
