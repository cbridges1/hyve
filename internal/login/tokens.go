package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"time"
)

// TokenInfo is an API token as the host lists it.
type TokenInfo struct {
	User        string     `json:"user"`
	Name        string     `json:"name"`
	ID          string     `json:"id"`
	Description string     `json:"description,omitempty"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	LastUsed    *time.Time `json:"lastUsed,omitempty"`
}

// hostError turns an error response into an error.
func hostError(what string, resp *http.Response, data []byte) error {
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		if code, msg := apiError(data); code != "" && msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("this hyve host is too old for %s; upgrade it", what)
	}
	if _, msg := apiError(data); msg != "" {
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %s", what, resp.Status)
}

// CreateToken makes an API token for user ("" is yourself), lasting
// expiresIn ("90d", "720h"; "" is the host's default). The token is in
// the result, this once.
func CreateToken(ctx context.Context, c *http.Client, s *Server, user, name, description, expiresIn string) (string, TokenInfo, error) {
	body := map[string]string{"user": user, "name": name, "description": description, "expiresIn": expiresIn}
	resp, data, err := do(ctx, c, http.MethodPost, s.URL+"/v1/tokens", body, s.Token)
	if err != nil {
		return "", TokenInfo{}, err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", TokenInfo{}, hostError("API tokens", resp, data)
	}
	var out struct {
		Token string `json:"token"`
		TokenInfo
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", TokenInfo{}, err
	}
	return out.Token, out.TokenInfo, nil
}

// ListTokens lists user's API tokens ("" is yours), or everyone's.
func ListTokens(ctx context.Context, c *http.Client, s *Server, user string, all bool) ([]TokenInfo, error) {
	q := neturl.Values{}
	if user != "" {
		q.Set("user", user)
	}
	if all {
		q.Set("all", "true")
	}
	resp, data, err := do(ctx, c, http.MethodGet, s.URL+"/v1/tokens?"+q.Encode(), nil, s.Token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, hostError("API tokens", resp, data)
	}
	var out []TokenInfo
	return out, json.Unmarshal(data, &out)
}

// RevokeToken deletes user's token name.
func RevokeToken(ctx context.Context, c *http.Client, s *Server, user, name string) error {
	resp, data, err := do(ctx, c, http.MethodDelete, s.URL+"/v1/tokens/"+neturl.PathEscape(user)+"/"+neturl.PathEscape(name), nil, s.Token)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return hostError("API tokens", resp, data)
	}
	return nil
}

// Me is who a session is, as the host says.
type Me struct {
	Username string `json:"username"`
	// Kind is "" (a sign-in), "token" or "federated" (a CI job's).
	Kind      string `json:"kind,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// WhoAmI asks the host who token is.
func WhoAmI(ctx context.Context, c *http.Client, base, token string) (Me, error) {
	resp, data, err := do(ctx, c, http.MethodGet, base+"/v1/me", nil, token)
	if err != nil {
		return Me{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return Me{}, errors.New("the host doesn't accept this token: it's wrong, expired or revoked")
	}
	if resp.StatusCode != http.StatusOK {
		return Me{}, hostError("checking the token", resp, data)
	}
	var me Me
	return me, json.Unmarshal(data, &me)
}

// CILogin trades a CI job's ID token for a session as username, which
// must trust the job's CI system (spec.trustedCI).
func CILogin(ctx context.Context, c *http.Client, base, username, idToken string) (Session, error) {
	resp, data, err := do(ctx, c, http.MethodPost, base+"/v1-public/ci-login", map[string]string{"username": username, "token": idToken}, "")
	if err != nil {
		return Session{}, err
	}
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return parseSession(data)
	case http.StatusUnauthorized:
		return Session{}, fmt.Errorf("%s doesn't trust this job: check its trusted CI entries ('hyve user trust list %s') against the token's claims", username, username)
	case http.StatusTooManyRequests:
		return Session{}, ErrTooManyAttempts
	}
	return Session{}, hostError("signing in from CI", resp, data)
}

// GitHubActionsIDToken asks GitHub Actions for the job's ID token, for
// audience. The workflow needs 'permissions: id-token: write'.
func GitHubActionsIDToken(ctx context.Context, c *http.Client, audience string) (string, error) {
	reqURL, reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqToken == "" {
		return "", errors.New("no GitHub Actions ID token: run in a GitHub Actions job with 'permissions: id-token: write'")
	}
	u, err := neturl.Parse(reqURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()
	resp, data, err := do(ctx, c, http.MethodGet, u.String(), nil, reqToken)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getting the GitHub Actions ID token: %s", resp.Status)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Value == "" {
		return "", errors.New("getting the GitHub Actions ID token: no token in the response")
	}
	return out.Value, nil
}
