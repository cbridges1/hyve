package signin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// Anyone may list, make and revoke their own API tokens. Someone else's
// need the Kubernetes right to that verb on the users/tokens subresource
// of that User (all users', for a list of everyone's), checked with a
// SubjectAccessReview as the caller: admins have it, and the chart's
// hyve-user-admin role gives it.

// CreateTokenRequest is the body of POST /v1/tokens.
type CreateTokenRequest struct {
	// User the token signs in as; empty is the caller.
	User        string `json:"user,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ExpiresIn is a duration: "90d", "720h"; empty is DefaultTokenTTL.
	ExpiresIn string `json:"expiresIn,omitempty"`
}

// CreateTokenResponse carries the token, shown this once.
type CreateTokenResponse struct {
	Token string `json:"token"`
	TokenInfo
}

// ParseTokenTTL reads "90d", "12h" or "1y" (365 days).
func ParseTokenTTL(s string) (time.Duration, error) {
	if s == "" {
		return DefaultTokenTTL, nil
	}
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "y": 365 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			v, err := strconv.Atoi(n)
			if err != nil || v <= 0 {
				return 0, errors.New("an expiry is a duration such as 90d, 1y or 720h")
			}
			return time.Duration(v) * unit, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("an expiry is a duration such as 90d, 1y or 720h")
	}
	return d, nil
}

// canTokens checks whether ident may verb (list, create, delete) user's
// tokens; user "" is everyone's.
func (s *Server) canTokens(ctx context.Context, ident Identity, verb, user string) (bool, error) {
	if user == ident.Username {
		return true, nil
	}
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   ident.KubeUser(),
			Groups: ident.KubeGroups(),
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:       hyvev1alpha1.GroupVersion.Group,
				Version:     hyvev1alpha1.GroupVersion.Version,
				Resource:    "users",
				Subresource: "tokens",
				Verb:        verb,
				Name:        user,
			},
		},
	}
	if err := s.Store.Client.Create(ctx, sar); err != nil {
		return false, err
	}
	return sar.Status.Allowed, nil
}

// allowTokens writes the error and returns false when ident may not.
func (s *Server) allowTokens(w http.ResponseWriter, r *http.Request, ident Identity, verb, user string) bool {
	ok, err := s.canTokens(r.Context(), ident, verb, user)
	if err != nil {
		log.Printf("signin: access review for %s's tokens (%s by %s): %v", user, verb, ident.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't check permissions")
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "Forbidden", "you can't manage this user's API tokens")
		return false
	}
	return true
}

// handleListTokens is GET /v1/tokens: the caller's, ?user=NAME's, or
// ?all=true everyone's.
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	user := r.URL.Query().Get("user")
	if user == "" {
		user = ident.Username
	}
	if r.URL.Query().Get("all") == "true" {
		user = ""
	}
	if !s.allowTokens(w, r, ident, "list", user) {
		return
	}
	list, err := s.Store.ListTokens(r.Context(), user)
	if err != nil {
		log.Printf("signin: listing tokens: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't list tokens")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if ident.Kind != KindSession {
		writeError(w, http.StatusForbidden, "Forbidden", "an API token or a CI job's session can't make API tokens; sign in")
		return
	}
	var req CreateTokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	if req.User == "" {
		req.User = ident.Username
	}
	ttl, err := ParseTokenTTL(req.ExpiresIn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "InvalidFormat", err.Error())
		return
	}
	if !s.allowTokens(w, r, ident, "create", req.User) {
		return
	}
	token, info, err := s.Store.CreateToken(r.Context(), req.User, req.Name, req.Description, ident.Username, ttl)
	switch {
	case apierrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NotFound", "no such user")
		return
	case errors.Is(err, ErrTokenExists):
		writeError(w, http.StatusConflict, "AlreadyExists", err.Error())
		return
	case errors.Is(err, ErrInvalidToken):
		writeError(w, http.StatusUnprocessableEntity, "InvalidFormat", strings.TrimPrefix(err.Error(), ErrInvalidToken.Error()+": "))
		return
	case err != nil:
		log.Printf("signin: token for %s: %v", req.User, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't make the token")
		return
	}
	log.Printf("signin: %s made API token %s/%s, until %s", ident.Username, info.User, info.Name, info.ExpiresAt.Format(time.RFC3339))
	writeJSON(w, http.StatusCreated, CreateTokenResponse{Token: token, TokenInfo: info})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	user, name := r.PathValue("user"), r.PathValue("name")
	if !s.allowTokens(w, r, ident, "delete", user) {
		return
	}
	switch err := s.Store.RevokeToken(r.Context(), user, name); {
	case errors.Is(err, ErrNoToken):
		writeError(w, http.StatusNotFound, "NotFound", err.Error())
		return
	case err != nil:
		log.Printf("signin: revoking token %s/%s: %v", user, name, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't revoke the token")
		return
	}
	log.Printf("signin: %s revoked API token %s/%s", ident.Username, user, name)
	w.WriteHeader(http.StatusNoContent)
}
