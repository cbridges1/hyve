package login

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"time"
)

var (
	// ErrInvalidCredentials is a rejected username or password.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrMustChangePassword means the server wants a new password before it
	// issues a session.
	ErrMustChangePassword = errors.New("the password must be changed")
	// ErrTooManyAttempts is the server's sign-in rate limit.
	ErrTooManyAttempts = errors.New("too many failed sign-ins; try again later")
)

// Session is what a successful sign-in returns.
type Session struct {
	Token     string
	ExpiresAt time.Time // zero: no expiry
	UserID    string
}

// HTTPClient returns a client trusting the server's CA settings.
func HTTPClient(caCertFile string, insecure bool) (*http.Client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec // only when asked for
	if caCertFile != "" {
		pem, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s has no PEM certificates", caCertFile)
		}
		tlsCfg.RootCAs = pool
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}, nil
}

// Detect checks that base is a hyve host: only hyve's host answers
// /v1-public/hyve.
func Detect(ctx context.Context, c *http.Client, base string) error {
	resp, body, err := do(ctx, c, http.MethodGet, base+"/v1-public/hyve", nil, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		var info struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(body, &info) == nil && info.Kind == "hyve-host" {
			return nil
		}
	}
	return fmt.Errorf("%s isn't a hyve host (GET /v1-public/hyve: %s)", base, resp.Status)
}

// do sends a request and reads the whole response.
func do(ctx context.Context, c *http.Client, method, url string, body any, token string) (*http.Response, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, data, err
}

// apiError extracts the host's error code from a body.
func apiError(body []byte) (code, message string) {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Code, e.Message
}

// parseSession reads a login response: {token, expiresAt, userId}.
func parseSession(body []byte) (Session, error) {
	var t struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
		UserID    string `json:"userId"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return Session{}, fmt.Errorf("reading the sign-in response: %w", err)
	}
	s := Session{Token: t.Token, UserID: t.UserID}
	if s.Token == "" {
		return Session{}, errors.New("the sign-in response had no token")
	}
	if t.ExpiresAt != "" {
		ts, err := time.Parse(time.RFC3339, t.ExpiresAt)
		if err != nil {
			return Session{}, fmt.Errorf("reading the session expiry %q: %w", t.ExpiresAt, err)
		}
		s.ExpiresAt = ts
	}
	return s, nil
}

// PasswordLogin signs in with a username and password. newPassword is sent
// only when the server said the password must change.
func PasswordLogin(ctx context.Context, c *http.Client, base, username, password, newPassword string) (Session, error) {
	body := map[string]any{
		"type":     "localProvider",
		"username": username,
		"password": password,
	}
	if newPassword != "" {
		body["newPassword"] = newPassword
	}
	resp, data, err := do(ctx, c, http.MethodPost, base+"/v1-public/login", body, "")
	if err != nil {
		return Session{}, err
	}
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return parseSession(data)
	case http.StatusUnauthorized:
		return Session{}, ErrInvalidCredentials
	case http.StatusTooManyRequests:
		return Session{}, ErrTooManyAttempts
	case http.StatusForbidden:
		if code, _ := apiError(data); code == "MustChangePassword" {
			return Session{}, ErrMustChangePassword
		}
		return Session{}, ErrInvalidCredentials
	default:
		if _, msg := apiError(data); msg != "" {
			return Session{}, fmt.Errorf("sign-in failed: %s", msg)
		}
		return Session{}, fmt.Errorf("sign-in failed: %s", resp.Status)
	}
}

// Logout ends the session on the server.
func Logout(ctx context.Context, c *http.Client, s *Server) error {
	resp, _, err := do(ctx, c, http.MethodPost, s.URL+"/v1/logout", map[string]any{}, s.Token)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("sign-out: %s", resp.Status)
	}
	return nil
}

// SetPassword changes a hyve User's password through the host. current is
// needed only when changing your own.
func SetPassword(ctx context.Context, c *http.Client, s *Server, username, current, newPassword string) error {
	resp, data, err := do(ctx, c, http.MethodPut, s.URL+"/v1/users/"+neturl.PathEscape(username)+"/password",
		map[string]string{"currentPassword": current, "newPassword": newPassword}, s.Token)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if _, msg := apiError(data); msg != "" {
		return errors.New(msg)
	}
	return fmt.Errorf("setting the password: %s", resp.Status)
}

// UISession asks a hyve host for a new session for the browser, as the
// same user (`hyve ui`).
func UISession(ctx context.Context, c *http.Client, s *Server) (Session, error) {
	resp, data, err := do(ctx, c, http.MethodPost, s.URL+"/v1/ui-sessions", map[string]any{}, s.Token)
	if err != nil {
		return Session{}, err
	}
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return parseSession(data)
	case http.StatusUnauthorized:
		return Session{}, fmt.Errorf("the session for %s has expired; run 'hyve login %s'", s.Name, s.URL)
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return Session{}, errors.New("this hyve host is too old to sign the browser in; upgrade it")
	default:
		if _, msg := apiError(data); msg != "" {
			return Session{}, errors.New(msg)
		}
		return Session{}, fmt.Errorf("starting a browser session: %s", resp.Status)
	}
}
