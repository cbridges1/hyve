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

	// Namespace optionally pre-selects an organization for the session
	// (by name, or namespace — see resolveLoginNamespace) — never needed:
	// a login reaches every organization its user belongs to, and each
	// request can pick one (organizationHeader). Kept for older CLIs'
	// --org; it must name an organization the user can access.
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

// handleLogin authenticates a local user (username or email, and
// password), creates a Session row recording the login, and issues both
// halves of loginResponse. The session isn't tied to an organization — see
// resolveAccess. OIDC login (a browser redirect flow) is not implemented —
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

	// A username or an email (both unique across the install). Every
	// failure below gets the same response as a wrong password — a login
	// endpoint shouldn't reveal which usernames/emails exist.
	user, err := s.findUserForLogin(r.Context(), req.Username)
	if err != nil || user.PasswordHash == nil || !VerifyPassword(*user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	memberships, err := s.OrgStore.ListBindingsForIdentity(r.Context(), orgdb.SubjectTypeLocal, user.Username)
	if err != nil || len(memberships) == 0 {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// The real (immutable) namespace, never an organization's renamable
	// Name, so a later rename can't point an issued session elsewhere.
	// Empty — the normal case — selects nothing; see resolveAccess.
	resolvedNamespace := ""
	if req.Namespace != "" {
		resolvedNamespace = s.resolveLoginNamespace(r.Context(), req.Namespace)
	}
	resp, err := s.issueSession(r.Context(), user.Username, resolvedNamespace)
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
	// Identifier accepts either a username or an email, same dual lookup
	// handleLogin uses.
	Identifier string `json:"identifier"`
	// Namespace is ignored — users aren't per-organization any more. Kept
	// so older consoles' requests still decode.
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
	user, err := s.findUserForLogin(ctx, req.Identifier)
	// No match, or no email on file to send to — both get the identical
	// generic response. A user with no email isn't an error state, just
	// nothing this endpoint can act on.
	if err != nil || user.Email == nil {
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
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(passwordResetTokenTTL),
	}); err != nil {
		log.Printf("api: failed to store password reset token for %q: %v", user.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to process password reset request")
		return
	}

	link := s.buildPasswordResetLink(*user.Email, token)
	sendErr := email.Send(ctx, s.OrgStore, *user.Email, email.TemplatePasswordResetCode, email.PasswordResetCodeData{
		Username: user.Username,
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
		log.Printf("ℹ️  password reset requested for %q — no SMTP configured, token: %s", user.Username, token)
	} else if sendErr != nil {
		log.Printf("api: failed to send password reset email to %q: %v", *user.Email, sendErr)
	}

	writeJSON(w, http.StatusOK, requestPasswordResetResponse{Sent: true})
}

// buildPasswordResetLink builds the URL a password-reset email's button
// points at — the web console is served from the same origin as this API
// (see Routes' own doc comment), so PublicBaseURL + the console's own
// hash-router path is a real, clickable link. Query parameters, escaped,
// so a "+"-containing email address can't corrupt the URL.
func (s *Server) buildPasswordResetLink(email, token string) string {
	base := strings.TrimRight(s.PublicBaseURL, "/")
	return base + "/#/reset-password?email=" + url.QueryEscape(email) + "&token=" + url.QueryEscape(token)
}

type resetPasswordRequest struct {
	Email       string `json:"email"`
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
	// Namespace is ignored — emails are unique across the install now.
	// Kept so older reset links still decode.
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
	user, err := s.OrgStore.GetUserByEmail(ctx, req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}

	token, err := s.OrgStore.GetPasswordResetTokenByUserID(ctx, user.ID)
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
		log.Printf("api: failed to hash new password for %q: %v", user.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}
	if err := s.OrgStore.SetUserPassword(ctx, user.ID, hash); err != nil {
		log.Printf("api: failed to set new password for %q: %v", user.Username, err)
		writeError(w, http.StatusInternalServerError, "failed to reset password")
		return
	}
	// Single-use: the token is consumed the moment it's successfully
	// applied, whether or not everything below it (session revocation,
	// the notification email) also succeeds.
	if err := s.OrgStore.DeletePasswordResetTokensForUser(ctx, user.ID); err != nil {
		log.Printf("api: failed to delete used password reset token for %q: %v", user.Username, err)
	}
	// Kill every other still-active session, in every organization — same
	// security posture as Pangolin's own resetPassword.ts
	// (invalidateAllSessions). Logged, not fatal: the password itself
	// already changed, which is what the caller actually asked for.
	if err := s.OrgStore.DeleteSessionsBySubject(ctx, user.Username); err != nil {
		log.Printf("api: failed to revoke sessions for %q after password reset: %v", user.Username, err)
	}

	if sendErr := email.Send(ctx, s.OrgStore, req.Email, email.TemplatePasswordChanged, email.PasswordChangedData{Username: user.Username}); sendErr != nil && !errors.Is(sendErr, email.ErrNotConfigured) {
		log.Printf("api: failed to send password-changed notification to %q: %v", req.Email, sendErr)
	}

	writeJSON(w, http.StatusOK, resetPasswordResponse{Reset: true})
}
