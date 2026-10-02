package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cbridges1/hyve/internal/email"
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

// passwordResetTokenTTL matches Pangolin's own 2-hour window exactly (see
// HYVE-EMAIL-IMPLEMENTATION-PLAN.md's "Baseline" section) — long enough
// to actually check an inbox, short enough that a forgotten, unused
// reset link/code stops being a live credential well before it's likely
// to be found by anyone else.
const passwordResetTokenTTL = 2 * time.Hour

// passwordResetTokenAlphabet/Length mirror Pangolin's own
// generateRandomString(8, alphabet("0-9","A-Z","a-z")) call exactly — 8
// characters from a 62-symbol alphabet is ~47.6 bits of entropy, hashed
// before storage (see generatePasswordResetToken's own call site) the
// same way a real password is, not just base64'd.
const passwordResetTokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const passwordResetTokenLength = 8

// generatePasswordResetToken returns a fresh random token — crypto/rand,
// never math/rand, for the same reason GenerateSessionSecret already
// uses it: this is a bearer credential good for a live password reset,
// not cosmetic randomness.
func generatePasswordResetToken() (string, error) {
	b := make([]byte, passwordResetTokenLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordResetTokenAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = passwordResetTokenAlphabet[n.Int64()]
	}
	return string(b), nil
}

// randomEnumerationDelay sleeps a random 0-2000ms — Pangolin's own
// mitigation, copied as-is (see requestPasswordReset.ts's own
// randomDelay), for the timing side-channel a fixed-time "no such
// account" response would otherwise open up: a real send involves an
// actual SMTP round trip and is naturally slower, so without this, an
// attacker could distinguish "no such account" from "email actually
// sent" purely by response latency, even though both return the
// identical 200 body.
func randomEnumerationDelay(ctx context.Context) {
	n, err := rand.Int(rand.Reader, big.NewInt(2000))
	if err != nil {
		return
	}
	select {
	case <-time.After(time.Duration(n.Int64()) * time.Millisecond):
	case <-ctx.Done():
	}
}

type requestPasswordResetRequest struct {
	// Identifier accepts either a binding's Identity (username) or its
	// Email, same dual lookup handleLogin already established.
	Identifier string `json:"identifier"`
	// Namespace selects which tenant to look the identifier up in — same
	// field, same resolution (resolveLoginNamespace), same "despite the
	// name" caveat as loginRequest.Namespace. Empty means the
	// control-plane namespace.
	Namespace string `json:"namespace,omitempty"`
}

type requestPasswordResetResponse struct {
	Sent bool `json:"sent"`
}

// handleRequestPasswordReset is the self-service "forgot my password"
// entry point — unauthenticated by design, mounted alongside
// POST /auth/login (see Routes). Always responds 200 {"sent": true}
// regardless of whether Identifier actually matched an account, whether
// that account has an email on file, or whether SMTP is even configured
// at all — a login endpoint's neighbor shouldn't reveal which
// usernames/emails exist any more than login itself does (see
// handleLogin's own identical stance). See
// HYVE-EMAIL-IMPLEMENTATION-PLAN.md's Milestone 4 for the full design
// this mirrors from Pangolin's requestPasswordReset.ts.
func (s *Server) handleRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req requestPasswordResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Identifier == "" {
		writeError(w, http.StatusBadRequest, "identifier is required")
		return
	}

	ctx := r.Context()
	resolvedNamespace := req.Namespace
	if resolvedNamespace != "" {
		resolvedNamespace = s.resolveLoginNamespace(ctx, resolvedNamespace)
	}
	ns := resolvedNamespace
	if ns == "" {
		ns = s.Namespace
	}

	binding, err := s.findBindingBySubject(ctx, ns, orgdb.SubjectTypeLocal, req.Identifier)
	if err != nil {
		binding, err = s.findBindingByEmail(ctx, ns, req.Identifier)
	}
	// No match, wrong subject type, or no email on file to send to —
	// every one of these gets the identical generic response. A binding
	// with no email isn't an error state (every account created before
	// this feature shipped has none), just nothing this endpoint can act
	// on.
	if err != nil || binding.SubjectType != orgdb.SubjectTypeLocal || binding.Email == nil {
		randomEnumerationDelay(ctx)
		writeJSON(w, http.StatusOK, requestPasswordResetResponse{Sent: true})
		return
	}

	token, err := generatePasswordResetToken()
	if err != nil {
		log.Printf("api: failed to generate password reset token: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to process password reset request")
		return
	}
	tokenHash, err := HashPassword(token)
	if err != nil {
		log.Printf("api: failed to hash password reset token: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to process password reset request")
		return
	}
	if _, err := s.OrgStore.CreatePasswordResetToken(ctx, orgdb.PasswordResetToken{
		BindingID: binding.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(passwordResetTokenTTL),
	}); err != nil {
		log.Printf("api: failed to store password reset token for %q: %v", binding.Identity, err)
		writeError(w, http.StatusInternalServerError, "failed to process password reset request")
		return
	}

	link := s.buildPasswordResetLink(*binding.Email, token, resolvedNamespace)
	sendErr := email.Send(ctx, s.OrgStore, *binding.Email, email.TemplatePasswordResetCode, email.PasswordResetCodeData{
		Username: binding.Identity,
		Code:     token,
		Link:     link,
	})
	if errors.Is(sendErr, email.ErrNotConfigured) {
		// The exact bootstrap safety net Pangolin's own fallback
		// provides (see requestPasswordReset.ts: "if
		// (!config.getRawConfig().email) logger.info(...)") — a fresh
		// self-hosted install with no SMTP set up yet still lets its
		// first admin recover a forgotten password: read the logs. This
		// case is expected to be common for hyve specifically (a
		// single-superadmin self-hosted install is the normal starting
		// point, not the exception), not a degraded/error state.
		log.Printf("ℹ️  password reset requested for %q (namespace %q) — no SMTP configured, token: %s", binding.Identity, ns, token)
	} else if sendErr != nil {
		log.Printf("api: failed to send password reset email to %q: %v", *binding.Email, sendErr)
	}

	writeJSON(w, http.StatusOK, requestPasswordResetResponse{Sent: true})
}

// buildPasswordResetLink builds the URL a password-reset email's button
// points at — the web console is served from the same origin as this API
// (see Routes' own doc comment), so PublicBaseURL + the console's own
// hash-router path is a real, clickable link. namespace is carried
// through as a query parameter and round-tripped back by
// handleResetPassword — bindings.email is only unique *within* a
// namespace (see migrations/*/0003_binding_email.sql), so the same email
// address could in principle belong to a different account in a
// different tenant; carrying the exact namespace this token was actually
// minted for avoids re-deriving (and potentially mismatching) it from
// email alone at consume time. email/token/namespace are query
// parameters, not path segments, so url.QueryEscape (not raw
// concatenation) is what keeps a "+"-containing email address or similar
// from corrupting the URL.
func (s *Server) buildPasswordResetLink(email, token, namespace string) string {
	base := strings.TrimRight(s.PublicBaseURL, "/")
	return base + "/#/reset-password?email=" + url.QueryEscape(email) + "&token=" + url.QueryEscape(token) + "&namespace=" + url.QueryEscape(namespace)
}

type resetPasswordRequest struct {
	Email       string `json:"email"`
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
	// Namespace is the exact (already-resolved) namespace
	// handleRequestPasswordReset minted this token against — round-
	// tripped from buildPasswordResetLink's own query parameter, not
	// re-resolved from Email (see that function's own doc comment for
	// why). Empty means the control-plane namespace, same convention as
	// loginRequest.Namespace/requestPasswordResetRequest.Namespace.
	Namespace string `json:"namespace,omitempty"`
}

type resetPasswordResponse struct {
	Reset bool `json:"reset"`
}

// handleResetPassword consumes a password-reset token minted by
// handleRequestPasswordReset — unauthenticated by design, same as that
// handler. Unlike the request side, this one DOES report specific
// failures (invalid/expired token, unknown email) rather than a generic
// response: by the time a caller has a real token value in hand, there's
// nothing left to protect by staying vague — the token itself (not the
// email address) is what proves the caller was the one who received the
// reset email in the first place.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Token == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "email, token, and newPassword are required")
		return
	}

	ctx := r.Context()
	ns := req.Namespace
	if ns == "" {
		ns = s.Namespace
	}
	binding, err := s.findBindingByEmail(ctx, ns, req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}

	token, err := s.OrgStore.GetPasswordResetTokenByBindingID(ctx, binding.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}
	if time.Now().After(token.ExpiresAt) {
		writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}
	if !VerifyPassword(token.TokenHash, req.Token) {
		writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		log.Printf("api: failed to hash new password for %q: %v", binding.Identity, err)
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}
	if err := s.OrgStore.SetBindingPassword(ctx, binding.ID, hash); err != nil {
		log.Printf("api: failed to set new password for %q: %v", binding.Identity, err)
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}
	// Single-use: the token is consumed the moment it's successfully
	// applied, whether or not everything below it (session revocation,
	// the notification email) also succeeds.
	if err := s.OrgStore.DeletePasswordResetTokensForBinding(ctx, binding.ID); err != nil {
		log.Printf("api: failed to delete used password reset token for %q: %v", binding.Identity, err)
	}
	// Kill every other still-active session — same security posture as
	// Pangolin's own resetPassword.ts (invalidateAllSessions). Logged,
	// not fatal to the request: the password itself already changed
	// successfully, which is what the caller actually asked for.
	if err := s.OrgStore.DeleteSessionsBySubject(ctx, binding.Identity, binding.Namespace); err != nil {
		log.Printf("api: failed to revoke sessions for %q after password reset: %v", binding.Identity, err)
	}

	if sendErr := email.Send(ctx, s.OrgStore, req.Email, email.TemplatePasswordChanged, email.PasswordChangedData{Username: binding.Identity}); sendErr != nil && !errors.Is(sendErr, email.ErrNotConfigured) {
		log.Printf("api: failed to send password-changed notification to %q: %v", req.Email, sendErr)
	}

	writeJSON(w, http.StatusOK, resetPasswordResponse{Reset: true})
}
