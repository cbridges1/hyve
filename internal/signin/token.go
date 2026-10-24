// Package signin is the server side of hyve's username-and-password
// sign-in (HYVE-PROPOSAL.md, "Signing in: one
// URL, a username and a password"). It runs inside `hyve host run`:
//
//   - Users are hyve.io User objects. Each password is a bcrypt hash in a
//     Secret owned by its User; there's no database.
//   - Signing in trades the password for a session token. Only the token's
//     SHA-256 is stored, in a Secret owned by the User, so deleting a User
//     removes its password and sessions.
//   - The HTTP API follows the shape of Rancher's /v1-public login API, the
//     subset the CLI uses.
//   - /k8s/clusters/local proxies the management cluster's API as the
//     signed-in user, impersonated as "hyve:<username>".
package signin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"
)

// SessionPrefix starts every hyve session token. The proxy uses it to tell
// a hyve session from any other bearer token (a service-account JWT, an
// admin's own kubeconfig token), which it passes through untouched.
const SessionPrefix = "hs-"

var (
	tokenPattern = regexp.MustCompile(`^hs-([a-z0-9]{10}):([a-z2-7]{52})$`)
	idAlphabet   = "abcdefghijklmnopqrstuvwxyz0123456789"
	secretCodec  = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// IsSessionToken reports whether bearer has the shape of a hyve session
// token. It says nothing about whether the session is valid.
func IsSessionToken(bearer string) bool {
	return strings.HasPrefix(bearer, SessionPrefix)
}

// newSessionToken returns a fresh token, its ID and the hash to store.
func newSessionToken() (token, id, hash string, err error) {
	idBytes := make([]byte, 10)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", "", err
	}
	for i, b := range idBytes {
		idBytes[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	secretBytes := make([]byte, 32)
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", "", err
	}
	id = string(idBytes)
	secret := strings.ToLower(secretCodec.EncodeToString(secretBytes))
	return SessionPrefix + id + ":" + secret, id, hashSecret(secret), nil
}

// parseSessionToken splits a token into its ID and secret.
func parseSessionToken(token string) (id, secret string, ok bool) {
	m := tokenPattern.FindStringSubmatch(token)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func hashesEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// randomPassword is used for the bootstrap admin when no bootstrap Secret
// exists yet.
func randomPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 24)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}
