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

// Detect tells a hyve host from Rancher. Only hyve's host answers
// /v1-public/hyve.
func Detect(ctx context.Context, c *http.Client, base string) (string, error) {
	resp, body, err := do(ctx, c, http.MethodGet, base+"/v1-public/hyve", nil, "")
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusOK {
		var info struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(body, &info) == nil && info.Kind == "hyve-host" {
			return KindHyve, nil
		}
	}
	// Rancher answers /ping with "pong" too, so check its providers list.
	resp, _, err = do(ctx, c, http.MethodGet, base+"/v1-public/authproviders", nil, "")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s is neither a hyve host nor Rancher (GET /v1-public/authproviders: %s)", base, resp.Status)
	}
	return KindRancher, nil
}

// Provider is one auth provider a server offers.
type Provider struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	ClientID      string   `json:"clientId,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	AuthURL       string   `json:"authUrl,omitempty"`
	DeviceAuthURL string   `json:"deviceAuthUrl,omitempty"`
	TokenURL      string   `json:"tokenUrl,omitempty"`
}

// Flow is how the CLI signs in with a provider.
type Flow int

const (
	FlowPassword Flow = iota // username and password
	FlowSAML                 // Rancher's request-ID handoff
	FlowOAuth                // device code or auth code, then the ID token
	FlowBrowser              // the hyve extension's CLI sign-in page
)

// FlowFor returns the flow for a provider type, as Rancher's CLI chooses it;
// FlowBrowser is for the providers Rancher's CLI doesn't support.
func FlowFor(providerType string) Flow {
	switch providerType {
	case "localProvider", "openLdapProvider", "freeIpaProvider", "activeDirectoryProvider":
		return FlowPassword
	case "pingProvider", "adfsProvider", "keyCloakProvider", "oktaProvider", "shibbolethProvider":
		return FlowSAML
	case "azureADProvider":
		return FlowOAuth
	default:
		return FlowBrowser
	}
}

// ListProviders returns the server's enabled auth providers, local first.
func ListProviders(ctx context.Context, c *http.Client, base string) ([]Provider, error) {
	resp, body, err := do(ctx, c, http.MethodGet, base+"/v1-public/authproviders", nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing auth providers: %s", resp.Status)
	}
	var list struct {
		Data []Provider `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("listing auth providers: %w", err)
	}
	out := make([]Provider, 0, len(list.Data))
	for _, p := range list.Data {
		if p.Type == "" {
			continue
		}
		if p.Type == "localProvider" {
			out = append([]Provider{p}, out...)
		} else {
			out = append(out, p)
		}
	}
	return out, nil
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

// apiError extracts Rancher's (and hyve's) error code from a body.
func apiError(body []byte) (code, message string) {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Code, e.Message
}

// parseSession reads a login response: Rancher's v3-style
// {token, expiresAt, userId}, or its ext.cattle.io/v1 Token.
func parseSession(body []byte) (Session, error) {
	var t struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Token      string `json:"token"`
		ExpiresAt  string `json:"expiresAt"`
		UserID     string `json:"userId"`
		Spec       struct {
			UserID string `json:"userID"`
		} `json:"spec"`
		Status struct {
			BearerToken string `json:"bearerToken"`
			ExpiresAt   string `json:"expiresAt"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return Session{}, fmt.Errorf("reading the sign-in response: %w", err)
	}
	s := Session{Token: t.Token, UserID: t.UserID}
	expires := t.ExpiresAt
	if t.APIVersion == "ext.cattle.io/v1" && t.Kind == "Token" {
		s = Session{Token: t.Status.BearerToken, UserID: t.Spec.UserID}
		expires = t.Status.ExpiresAt
	}
	if s.Token == "" {
		return Session{}, errors.New("the sign-in response had no token")
	}
	if expires != "" {
		ts, err := time.Parse(time.RFC3339, expires)
		if err != nil {
			return Session{}, fmt.Errorf("reading the session expiry %q: %w", expires, err)
		}
		s.ExpiresAt = ts
	}
	return s, nil
}

// responseType is what the CLI asks Rancher for: a kubeconfig token, as
// Rancher's own CLI does. hyve's host ignores it.
const responseType = "kubeconfig"

// PasswordLogin signs in with a username and password. newPassword is sent
// only when the server said the password must change.
func PasswordLogin(ctx context.Context, c *http.Client, base, providerType, username, password, newPassword string) (Session, error) {
	body := map[string]any{
		"type":         providerType,
		"responseType": responseType,
		"username":     username,
		"password":     password,
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

// Logout ends the session on the server. Rancher's equivalent is the
// tokens logout action its UI uses.
func Logout(ctx context.Context, c *http.Client, s *Server) error {
	url := s.URL + "/v1/logout"
	if s.Kind == KindRancher {
		url = s.URL + "/v3/tokens?action=logout"
	}
	resp, _, err := do(ctx, c, http.MethodPost, url, map[string]any{}, s.Token)
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
	if s.Kind != KindHyve {
		return errors.New("passwords for Rancher users are managed in Rancher")
	}
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
