package signin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// LocalProviderType is the only auth provider hyve's host offers. The name
// is Rancher's, so the CLI handles hyve and Rancher's local users alike.
const LocalProviderType = "localProvider"

// Server serves the sign-in API and the /k8s/clusters/local proxy.
type Server struct {
	Store *Store
	// Proxy forwards /k8s/clusters/local to the management cluster's API.
	Proxy http.Handler
	// Version is reported by /v1-public/hyve.
	Version string
	// Routes, if set, adds more routes: the agent tunnel and the
	// child-cluster proxy.
	Routes func(*http.ServeMux)

	userLimit *limiter
	addrLimit *limiter
}

// NewServer returns a Server with Rancher-like limits on failed sign-ins:
// 5 per user, or 20 per client address, in 15 minutes locks that user or
// address out for 15 minutes.
func NewServer(store *Store, proxy http.Handler, version string) *Server {
	return &Server{
		Store:     store,
		Proxy:     proxy,
		Version:   version,
		userLimit: newLimiter(5, 15*time.Minute, 15*time.Minute),
		addrLimit: newLimiter(20, 15*time.Minute, 15*time.Minute),
	}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	mux.HandleFunc("GET /v1-public/hyve", s.handleInfo)
	mux.HandleFunc("GET /v1-public/authproviders", s.handleProviders)
	mux.HandleFunc("POST /v1-public/login", s.handleLogin)
	mux.HandleFunc("POST /v1/logout", s.handleLogout)
	mux.HandleFunc("PUT /v1/users/{name}/password", s.handleSetPassword)
	if s.Proxy != nil {
		mux.Handle("/k8s/clusters/local/", s.Proxy)
		mux.Handle("/k8s/clusters/local", s.Proxy)
	}
	if s.Routes != nil {
		s.Routes(mux)
	}
	return mux
}

type apiError struct {
	Type    string `json:"type"`
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Type: "error", Status: status, Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleInfo lets the CLI tell a hyve host from Rancher, which has no such
// path.
func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"kind": "hyve-host", "version": s.Version})
}

func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"type": "collection",
		"data": []map[string]string{{"id": "local", "type": LocalProviderType}},
	})
}

// LoginRequest is the body of POST /v1-public/login: Rancher's basic-login
// fields, plus NewPassword for a User that must change its password.
type LoginRequest struct {
	Type         string `json:"type"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	NewPassword  string `json:"newPassword,omitempty"`
	ResponseType string `json:"responseType,omitempty"`
}

// LoginResponse is Rancher's login response shape.
type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
	UserID    string `json:"userId"`
	ID        string `json:"id"`
	BaseType  string `json:"baseType"`
	Type      string `json:"type"`
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	if req.Type != "" && req.Type != LocalProviderType {
		writeError(w, http.StatusBadRequest, "InvalidFormat", "unsupported auth provider "+req.Type)
		return
	}
	userKey, addrKey := "user:"+req.Username, "addr:"+clientAddr(r)
	if s.userLimit.Blocked(userKey) || s.addrLimit.Blocked(addrKey) {
		writeError(w, http.StatusTooManyRequests, "TooManyRequests", "too many failed sign-ins; try again later")
		return
	}

	token, expiresAt, err := s.Store.Login(r.Context(), req.Username, req.Password, req.NewPassword)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		s.userLimit.Fail(userKey)
		s.addrLimit.Fail(addrKey)
		writeError(w, http.StatusUnauthorized, "Unauthorized", ErrInvalidCredentials.Error())
		return
	case errors.Is(err, ErrMustChangePassword):
		writeError(w, http.StatusForbidden, "MustChangePassword", "the password must be changed before signing in")
		return
	case errors.Is(err, ErrInvalidNewPassword):
		writeError(w, http.StatusUnprocessableEntity, "InvalidPassword", err.Error())
		return
	case err != nil:
		log.Printf("signin: login for %q: %v", req.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "sign-in failed")
		return
	}
	s.userLimit.Succeed(userKey)
	s.addrLimit.Succeed(addrKey)

	id, _, _ := parseSessionToken(token)
	writeJSON(w, http.StatusCreated, LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		UserID:    req.Username,
		ID:        SessionPrefix + id,
		BaseType:  "token",
		Type:      "token",
	})
}

// bearer returns the request's bearer token, or "".
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	ident, err := s.Store.Lookup(r.Context(), bearer(r))
	if err != nil {
		if !errors.Is(err, ErrInvalidSession) {
			log.Printf("signin: session lookup: %v", err)
		}
		writeError(w, http.StatusUnauthorized, "Unauthorized", ErrInvalidSession.Error())
		return Identity{}, false
	}
	return ident, true
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Logout(r.Context(), bearer(r)); err != nil && !errors.Is(err, ErrInvalidSession) {
		log.Printf("signin: logout: %v", err)
		writeError(w, http.StatusInternalServerError, "ServerError", "sign-out failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SetPasswordRequest is the body of PUT /v1/users/{name}/password.
type SetPasswordRequest struct {
	CurrentPassword string `json:"currentPassword,omitempty"`
	NewPassword     string `json:"newPassword"`
}

// handleSetPassword changes a password. Users change their own with their
// current password. Changing someone else's needs the Kubernetes right to
// update the users/password subresource of that User, checked with a
// SubjectAccessReview as the caller, and signs that user out everywhere.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var req SetPasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "InvalidPassword", err.Error())
		return
	}

	keep := ""
	if name == ident.Username {
		if _, err := s.Store.checkPassword(r.Context(), name, req.CurrentPassword); err != nil {
			writeError(w, http.StatusForbidden, "Forbidden", "the current password is wrong")
			return
		}
		keep = ident.SessionID
	} else {
		allowed, err := s.canSetPassword(r.Context(), ident, name)
		if err != nil {
			log.Printf("signin: access review for %s setting %s's password: %v", ident.Username, name, err)
			writeError(w, http.StatusInternalServerError, "ServerError", "couldn't check permissions")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "Forbidden", "you can't change this user's password")
			return
		}
	}

	if err := s.Store.SetPassword(r.Context(), name, req.NewPassword); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "NotFound", "no such user")
			return
		}
		log.Printf("signin: setting %s's password: %v", name, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't set the password")
		return
	}
	if err := s.Store.RevokeSessions(r.Context(), name, keep); err != nil {
		log.Printf("signin: revoking %s's sessions: %v", name, err)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) canSetPassword(ctx context.Context, ident Identity, name string) (bool, error) {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   ident.KubeUser(),
			Groups: ident.KubeGroups(),
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:       hyvev1alpha1.GroupVersion.Group,
				Version:     hyvev1alpha1.GroupVersion.Version,
				Resource:    "users",
				Subresource: "password",
				Verb:        "update",
				Name:        name,
			},
		},
	}
	if err := s.Store.Client.Create(ctx, sar); err != nil {
		return false, err
	}
	return sar.Status.Allowed, nil
}

// RunSessionCleanup deletes expired sessions every interval until ctx ends.
func (s *Server) RunSessionCleanup(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Store.DeleteExpiredSessions(ctx); err != nil {
				log.Printf("signin: deleting expired sessions: %v", err)
			}
		}
	}
}

// SessionIdentity resolves a request's hyve session, for other handlers on
// the host; ok is false when it has no valid one.
func (s *Server) SessionIdentity(r *http.Request) (Identity, bool) {
	token := bearer(r)
	if !IsSessionToken(token) {
		return Identity{}, false
	}
	ident, err := s.Store.Lookup(r.Context(), token)
	if err != nil {
		if !errors.Is(err, ErrInvalidSession) {
			log.Printf("signin: session lookup: %v", err)
		}
		return Identity{}, false
	}
	return ident, true
}
