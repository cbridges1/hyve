package signin

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

// The UI's session is a cookie, so its script never holds the token. Two
// rules keep another site from using it: the cookie is SameSite=Strict, and
// a request it authenticates must carry UIHeader unless it's a read. A
// cross-site page can't add that header without a CORS preflight, which
// the host never answers.
const (
	SessionCookie = "hyve_session"
	UIHeader      = "X-Hyve-UI"
)

// fromUI reports whether r carries UIHeader.
func fromUI(r *http.Request) bool { return r.Header.Get(UIHeader) != "" }

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sessionToken returns the request's session: its bearer token, else the
// UI's cookie. ok is false for a cookie on a write without UIHeader.
func sessionToken(r *http.Request) (token string, ok bool) {
	if t := bearer(r); t != "" {
		return t, true
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return "", true
	}
	if !safeMethod(r.Method) && !fromUI(r) {
		return "", false
	}
	return c.Value, true
}

// secure reports whether the browser reached the host over HTTPS, itself
// or through a TLS-terminating proxy.
func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(SessionCookie); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// MeResponse is GET /v1/me: who the session belongs to.
type MeResponse struct {
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	Version  string   `json:"version"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	groups := ident.Groups
	if groups == nil {
		groups = []string{}
	}
	writeJSON(w, http.StatusOK, MeResponse{Username: ident.Username, Groups: groups, Version: s.Version})
}

// UISessionResponse is POST /v1/ui-sessions: a new session for the
// browser.
type UISessionResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// handleNewUISession lets the CLI (`hyve ui`) sign the browser in as the
// same user. Only a bearer token can ask: the browser's own session can't
// mint more.
func (s *Server) handleNewUISession(w http.ResponseWriter, r *http.Request) {
	if bearer(r) == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized", ErrInvalidSession.Error())
		return
	}
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	token, expiresAt, err := s.Store.NewSession(r.Context(), ident)
	if errors.Is(err, ErrInvalidSession) {
		writeError(w, http.StatusUnauthorized, "Unauthorized", err.Error())
		return
	}
	if err != nil {
		log.Printf("signin: UI session for %s: %v", ident.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't start a session")
		return
	}
	writeJSON(w, http.StatusCreated, UISessionResponse{Token: token, ExpiresAt: expiresAt.Format(time.RFC3339)})
}

// handleUISession turns a session from `hyve ui` (passed to the page in
// the URL's fragment, which never reaches a server) into the UI's cookie.
func (s *Server) handleUISession(w http.ResponseWriter, r *http.Request) {
	if !fromUI(r) {
		writeError(w, http.StatusForbidden, "Forbidden", "missing the "+UIHeader+" header")
		return
	}
	addrKey := "addr:" + clientAddr(r)
	if s.addrLimit.Blocked(addrKey) {
		writeError(w, http.StatusTooManyRequests, "TooManyRequests", "too many failed sign-ins; try again later")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidBodyContent", "the request body isn't valid JSON")
		return
	}
	ident, err := s.Store.Lookup(r.Context(), req.Token)
	if err != nil {
		if !errors.Is(err, ErrInvalidSession) {
			log.Printf("signin: session lookup: %v", err)
		}
		s.addrLimit.Fail(addrKey)
		writeError(w, http.StatusUnauthorized, "Unauthorized", ErrInvalidSession.Error())
		return
	}
	expiresAt, err := s.Store.Expiry(r.Context(), req.Token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized", ErrInvalidSession.Error())
		return
	}
	setSessionCookie(w, r, req.Token, expiresAt)
	writeJSON(w, http.StatusOK, MeResponse{Username: ident.Username, Groups: ident.Groups, Version: s.Version})
}
