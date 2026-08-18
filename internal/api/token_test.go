package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueAndVerifyToken_RoundTrip(t *testing.T) {
	key := []byte("test-signing-key")
	token, err := IssueToken(key, "cedric")
	require.NoError(t, err)

	subject, err := VerifyToken(key, token)
	require.NoError(t, err)
	assert.Equal(t, "cedric", subject)
}

func TestVerifyToken_WrongKeyRejected(t *testing.T) {
	token, err := IssueToken([]byte("key-a"), "cedric")
	require.NoError(t, err)

	_, err = VerifyToken([]byte("key-b"), token)
	assert.Error(t, err)
}

func TestVerifyToken_TamperedPayloadRejected(t *testing.T) {
	key := []byte("test-signing-key")
	token, err := IssueToken(key, "cedric")
	require.NoError(t, err)

	// Flip the token's subject by swapping in another valid-looking
	// payload segment (still base64-decodable, just re-signed under a
	// *different* key so it fails signature verification against key).
	forged, err := IssueToken([]byte("attacker-key"), "admin")
	require.NoError(t, err)
	parts := splitToken(t, forged)
	original := splitToken(t, token)
	tampered := parts[0] + "." + original[1] // forged payload, original signature

	_, err = VerifyToken(key, tampered)
	assert.Error(t, err)
}

func TestVerifyToken_MalformedRejected(t *testing.T) {
	key := []byte("test-signing-key")
	for _, bad := range []string{"", "not-a-token", "onlyonepart", "a.b.c", "!!!.!!!"} {
		_, err := VerifyToken(key, bad)
		assert.Error(t, err, "expected error for malformed token %q", bad)
	}
}

func TestVerifyToken_ExpiredRejected(t *testing.T) {
	key := []byte("test-signing-key")
	payload := tokenPayload{Subject: "cedric", Expires: time.Now().Add(-time.Hour).Unix()}
	token, err := signPayload(key, payload)
	require.NoError(t, err)

	_, err = VerifyToken(key, token)
	assert.ErrorContains(t, err, "expired")
}

func splitToken(t *testing.T, token string) []string {
	t.Helper()
	parts := make([]string, 0, 2)
	idx := -1
	for i, c := range token {
		if c == '.' {
			idx = i
			break
		}
	}
	require.NotEqual(t, -1, idx, "token has no '.' separator")
	parts = append(parts, token[:idx], token[idx+1:])
	return parts
}
