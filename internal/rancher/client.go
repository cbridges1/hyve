// Package rancher is a minimal, native Go client for the two Rancher v3
// REST API calls hyve's AccessMethod "rancher" provider needs — login and
// kubeconfig generation. Deliberately not a full Rancher SDK, and
// deliberately not a wrapper around the `rancher` CLI: the whole point of
// this package, per HYVE-ACCESS-METHOD-DESIGN.md, is that a user connecting
// through a Rancher-backed AccessMethod needs nothing installed beyond the
// `hyve` binary itself.
//
// IMPORTANT — unverified against a live Rancher server: the two request/
// response shapes below (Login, GenerateKubeconfig) are implemented against
// Rancher's documented v3 API conventions, not confirmed live (no Rancher
// deployment was available to test against in this pass) — the same "spike
// before trusting it in CI" caveat this project already carries for the
// Teleport tunnel pattern. Treat the exact field names here as the best
// current understanding, not a verified contract, until run against a real
// Rancher server at least once.
package rancher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client talks to one Rancher server's v3 REST API.
type Client struct {
	// ServerURL is the Rancher server's base URL (e.g.
	// "https://rancher.example.com") — normally an AccessMethod's
	// spec.serverURL, trailing slash trimmed.
	ServerURL string

	// HTTPClient is used for every request — defaults to http.DefaultClient
	// when nil. Overridable for tests (see client_test.go) and for a
	// caller that needs custom TLS config (a private CA, etc.).
	HTTPClient *http.Client
}

// New returns a Client for serverURL, trimming any trailing slash so path
// concatenation below never produces a doubled "//".
func New(serverURL string) *Client {
	return &Client{ServerURL: strings.TrimRight(serverURL, "/")}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// loginRequest/loginResponse model Rancher's local-auth-provider login
// action: POST /v3-public/localProviders/local?action=login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string `json:"token"`
}

// Login exchanges a Rancher username/password for an API token — the
// user's own credentials, per the design's "pass the user's own
// credentials through, never a shared hyve service credential" decision.
// Never logs or persists password/token beyond the returned string; the
// caller is responsible for not persisting it either, beyond whatever a
// local Rancher CLI/token cache convention already does.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	body, err := json.Marshal(loginRequest{Username: username, Password: password})
	if err != nil {
		return "", fmt.Errorf("rancher: marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.ServerURL+"/v3-public/localProviders/local?action=login", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("rancher: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("rancher: login request to %s: %w", c.ServerURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("rancher: read login response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rancher: login failed (%s): %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var out loginResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("rancher: parse login response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("rancher: login response had no token")
	}
	return out.Token, nil
}

// generateKubeconfigResponse models Rancher's generateKubeconfig action's
// response — the kubeconfig YAML comes back as a JSON string field, not a
// raw YAML body.
type generateKubeconfigResponse struct {
	Config string `json:"config"`
}

// GenerateKubeconfig calls Rancher's per-cluster kubeconfig-generation
// action for clusterID (Rancher's own internal cluster ID — the value an
// AccessMethod-referencing ClusterDefinition needs to record somewhere,
// see the design doc's open item on this), authenticated as token (from
// Login, or a token the caller already had — see the design's "reuse an
// existing token" note). Returns the kubeconfig YAML bytes directly, the
// same shape every other AccessProvider in this project returns.
func (c *Client) GenerateKubeconfig(ctx context.Context, token, clusterID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/v3/clusters/%s?action=generateKubeconfig", c.ServerURL, clusterID), nil)
	if err != nil {
		return nil, fmt.Errorf("rancher: build generateKubeconfig request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("rancher: generateKubeconfig request for cluster %q: %w", clusterID, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rancher: read generateKubeconfig response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rancher: generateKubeconfig for cluster %q failed (%s): %s", clusterID, resp.Status, strings.TrimSpace(string(data)))
	}

	var out generateKubeconfigResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("rancher: parse generateKubeconfig response: %w", err)
	}
	if out.Config == "" {
		return nil, fmt.Errorf("rancher: generateKubeconfig response for cluster %q had no config", clusterID)
	}
	return []byte(out.Config), nil
}
