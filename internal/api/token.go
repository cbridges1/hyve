package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AccessTokenTTL is how long an access token (the "Authorization: Bearer"
// value sent on every /api/* request) is valid for. Short and stateless by
// design — requireAuth verifies it locally (HMAC signature + expiry, no
// Kubernetes round trip), so every request stays cheap. A client silently
// exchanges an expiring access token for a fresh one via POST /auth/refresh
// as long as its underlying HyveSession (see SessionTTL) is still valid —
// see cmd/shared's UseClusterMode for the CLI side of that.
const AccessTokenTTL = 30 * time.Minute

// SessionTTL is how long a HyveSession (created by POST /auth/login,
// re-validated by every POST /auth/refresh) stays valid before a real
// `hyve login` is required again. Long relative to AccessTokenTTL — this
// is the credential that makes unattended/automated use practical without
// storing a raw password: a cron job holds this instead, silently
// refreshing its short-lived access token for up to SessionTTL without
// human involvement.
const SessionTTL = 30 * 24 * time.Hour

// tokenPayload is the signed portion of an access token.
type tokenPayload struct {
	Subject string `json:"sub"`
	// Namespace is which tenant namespace this token was issued for (see
	// HYVE-MULTI-TENANCY-PLAN.md's "Phase 2" section) — empty means the
	// install's own control-plane namespace (a superadmin token). Baked
	// into the signed payload, unlike role: which namespace a session
	// belongs to is fixed at login time (a new login is required to
	// switch it), whereas role is deliberately re-resolved fresh on every
	// request instead (see this function's own doc comment on why role
	// itself is never carried here).
	Namespace string `json:"ns,omitempty"`
	Expires   int64  `json:"exp"`
}

// IssueAccessToken returns a signed access token for subject (a username)
// scoped to namespace (empty for the control-plane/superadmin namespace),
// valid for AccessTokenTTL from now. The token is intentionally not a
// standards-compliant JWT — just a minimal signed-payload scheme
// (base64url(payload) + "." + base64url(HMAC-SHA256(payload, key))) hyve
// issues and verifies itself, avoiding a JWT library dependency for a
// format nothing outside this codebase ever needs to parse. It carries no
// role — internal/api's authz middleware re-resolves the caller's role
// from HyveAccessBindings on every request (see internal/api/authz.go), so
// a role change on a binding takes effect immediately without needing a
// new token. Deliberately stateless: unlike the HyveSession it's issued
// from, an individual access token can't be revoked early — that's the
// trade for not needing a Kubernetes round trip on every request. Keeping
// AccessTokenTTL short is what bounds that window.
func IssueAccessToken(signingKey []byte, subject, namespace string) (string, error) {
	payload := tokenPayload{Subject: subject, Namespace: namespace, Expires: time.Now().Add(AccessTokenTTL).Unix()}
	return signPayload(signingKey, payload)
}

// VerifyToken checks an access token's signature and expiry and returns
// its subject (username) and namespace. A tampered, expired, or malformed
// token returns an error — callers must treat any error as
// "unauthenticated," never fall back to a default identity.
func VerifyToken(signingKey []byte, token string) (subject, namespace string, err error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed token")
	}
	payloadB64, sigB64 := parts[0], parts[1]

	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", "", fmt.Errorf("malformed token payload: %w", err)
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return "", "", fmt.Errorf("malformed token signature: %w", err)
	}

	wantSig := sign(signingKey, payloadJSON)
	if !hmac.Equal(gotSig, wantSig) {
		return "", "", fmt.Errorf("invalid token signature")
	}

	var payload tokenPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return "", "", fmt.Errorf("malformed token payload: %w", err)
	}
	if payload.Subject == "" {
		return "", "", fmt.Errorf("token has no subject")
	}
	if time.Now().Unix() > payload.Expires {
		return "", "", fmt.Errorf("token expired")
	}
	return payload.Subject, payload.Namespace, nil
}

func signPayload(signingKey []byte, payload tokenPayload) (string, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal token payload: %w", err)
	}
	sig := sign(signingKey, payloadJSON)
	return base64.RawURLEncoding.EncodeToString(payloadJSON) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func sign(signingKey, data []byte) []byte {
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(data)
	return mac.Sum(nil)
}

// GenerateSessionSecret returns a fresh, high-entropy random secret for a
// new HyveSession — the long-lived credential POST /auth/refresh consumes.
// Only its hash (see HashSessionSecret) is ever persisted; this raw value
// is returned to the client exactly once, at login, the same way a
// password is only ever known to its owner.
func GenerateSessionSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSessionSecret returns hex(SHA-256(raw)) — what's actually stored on a
// HyveSession's spec.tokenHash, and what POST /auth/refresh recomputes from
// a presented secret to compare against. A plain fast hash (not bcrypt) is
// appropriate here, unlike password.go's login-password hashing: this
// input is already a 32-byte random secret, not a human-memorable password
// an attacker could dictionary/brute-force offline — the thing protecting
// it is its own entropy, not the hash function's slowness.
func HashSessionSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
