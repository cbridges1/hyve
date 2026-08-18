package api

import "golang.org/x/crypto/bcrypt"

// HashPassword bcrypt-hashes a plaintext password for storage in a local
// user's paired Secret — see credentials.go's naming convention. Never
// store or log the plaintext itself.
func HashPassword(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether plaintext matches hash (as produced by
// HashPassword). A mismatch and a malformed hash both return false — the
// caller shouldn't need to distinguish "wrong password" from "corrupt
// stored hash," both mean "reject this login."
func VerifyPassword(hash, plaintext string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}
