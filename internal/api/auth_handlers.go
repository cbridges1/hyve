package api

import (
	"encoding/json"
	"net/http"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// handleLogin authenticates a local (username/password) identity and
// issues a session token. OIDC login (a browser redirect flow) is not
// implemented — see HyveAccessBindingSubject.Type's doc comment, SubjectTypeOIDC
// is reserved for later — local auth is the only login path today.
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

	binding, err := FindBindingBySubject(r.Context(), s.Client, hyvev1alpha1.SubjectTypeLocal, req.Username)
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

	token, err := IssueToken(s.SigningKey, req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session token")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(TokenTTL).Format(time.RFC3339),
	})
}

// handleLogout is a stateless no-op success response — session tokens are
// signed and self-verifying with no server-side session store, so there is
// nothing to invalidate here. The caller is responsible for discarding its
// stored token; `hyve login`'s companion logout command clears it from the
// active environment's registry entry (see internal/repository).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}
