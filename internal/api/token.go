package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TokenTTL is how long a session token is valid for after login. No refresh
// endpoint exists yet (v1, matching Phase 6's own "no refresh endpoint yet"
// note for the primary-cluster TokenRequest path) — a caller re-runs
// `hyve login` once it expires.
const TokenTTL = 24 * time.Hour

// tokenPayload is the signed portion of a session token.
type tokenPayload struct {
	Subject string `json:"sub"`
	Expires int64  `json:"exp"`
}

// IssueToken returns a signed session token for subject (a username),
// valid for TokenTTL from now. The token is intentionally not a
// standards-compliant JWT — just a minimal signed-payload scheme
// (base64url(payload) + "." + base64url(HMAC-SHA256(payload, key))) hyve
// issues and verifies itself, avoiding a JWT library dependency for a
// format nothing outside this codebase ever needs to parse. It carries no
// role — internal/api's authz middleware re-resolves the caller's role
// from HyveAccessBindings on every request (see internal/api/authz.go), so
// a role change on a binding takes effect immediately without needing a
// new token.
func IssueToken(signingKey []byte, subject string) (string, error) {
	payload := tokenPayload{Subject: subject, Expires: time.Now().Add(TokenTTL).Unix()}
	return signPayload(signingKey, payload)
}

// VerifyToken checks a session token's signature and expiry and returns
// its subject (username). A tampered, expired, or malformed token returns
// an error — callers must treat any error as "unauthenticated," never fall
// back to a default identity.
func VerifyToken(signingKey []byte, token string) (subject string, err error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed token")
	}
	payloadB64, sigB64 := parts[0], parts[1]

	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", fmt.Errorf("malformed token payload: %w", err)
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return "", fmt.Errorf("malformed token signature: %w", err)
	}

	wantSig := sign(signingKey, payloadJSON)
	if !hmac.Equal(gotSig, wantSig) {
		return "", fmt.Errorf("invalid token signature")
	}

	var payload tokenPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return "", fmt.Errorf("malformed token payload: %w", err)
	}
	if payload.Subject == "" {
		return "", fmt.Errorf("token has no subject")
	}
	if time.Now().Unix() > payload.Expires {
		return "", fmt.Errorf("token expired")
	}
	return payload.Subject, nil
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
