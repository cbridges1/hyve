package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerifyPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	require.NoError(t, err)
	assert.NotEqual(t, "correct-horse-battery-staple", hash)
	assert.True(t, VerifyPassword(hash, "correct-horse-battery-staple"))
}

func TestVerifyPassword_WrongPasswordRejected(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	require.NoError(t, err)
	assert.False(t, VerifyPassword(hash, "wrong-password"))
}

func TestVerifyPassword_MalformedHashRejected(t *testing.T) {
	assert.False(t, VerifyPassword("not-a-bcrypt-hash", "anything"))
}
