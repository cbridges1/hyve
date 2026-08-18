package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIClient is a minimal HTTP client for hyve's cluster-mode API (see
// internal/api) — cmd/cluster's commands use this instead of constructing
// a Kubernetes client directly when a valid local Session exists (see
// UseClusterMode). Response shapes are duplicated here rather than
// imported from internal/api (a deliberately server-side package,
// depending on controller-runtime/client-go) — matches this codebase's
// established precedent for small cross-boundary type duplication (see
// internal/apis/hyve/v1alpha1's own doc comments on why it duplicates
// internal/types shapes instead of importing them).
type APIClient struct {
	BaseURL string
	Token   string
}

// NewAPIClient builds a client from the current local session — callers
// should already have confirmed sess.Valid() (see UseClusterMode).
func NewAPIClient(sess *Session) *APIClient {
	return &APIClient{BaseURL: strings.TrimRight(sess.APIURL, "/"), Token: sess.Token}
}

// UseClusterMode reports whether cmd/cluster's commands should talk to the
// API instead of local files: a valid (unexpired, per local record) Session
// exists. Session presence deliberately wins over any other signal — no
// separate flag needed, and `hyve logout` cleanly reverts to local mode.
// Returns the session as well so callers don't need to reload it.
func UseClusterMode() (*Session, bool) {
	sess, err := LoadSession()
	if err != nil || sess == nil || !sess.Valid() {
		return nil, false
	}
	return sess, true
}

// ClusterDTO mirrors internal/api's clusterDTO response shape.
type ClusterDTO struct {
	Name               string         `json:"name"`
	Driver             string         `json:"driver"`
	Conditions         []ConditionDTO `json:"conditions,omitempty"`
	ObservedGeneration int64          `json:"observedGeneration"`
	AccessMethod       string         `json:"accessMethod,omitempty"`
	AccessLastMinted   string         `json:"accessLastMinted,omitempty"`
}

// ConditionDTO mirrors metav1.Condition's JSON shape closely enough for
// display purposes — cmd/cluster only ever reads these fields, never
// constructs or round-trips a full Condition.
type ConditionDTO struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

func (c *APIClient) ListClusters() ([]ClusterDTO, error) {
	var out []ClusterDTO
	if err := c.do(http.MethodGet, "/api/clusters", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *APIClient) GetCluster(name string) (*ClusterDTO, error) {
	var out ClusterDTO
	if err := c.do(http.MethodGet, "/api/clusters/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateCluster posts {name, spec} to POST /api/clusters. spec is already-
// marshaled JSON matching internal/apis/hyve/v1alpha1.ClusterDefinitionSpec's
// json tags — the real Kubernetes API server's own CRD schema validation
// on the resulting Create call is the actual validation, same as the
// server side deliberately relies on (see internal/api/clusters.go).
func (c *APIClient) CreateCluster(name string, spec json.RawMessage) (*ClusterDTO, error) {
	body, err := json.Marshal(struct {
		Name string          `json:"name"`
		Spec json.RawMessage `json:"spec"`
	}{Name: name, Spec: spec})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out ClusterDTO
	if err := c.do(http.MethodPost, "/api/clusters", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) DeleteCluster(name string) error {
	return c.do(http.MethodDelete, "/api/clusters/"+name, nil, nil)
}

// do sends the request and, on a non-2xx response, returns an error
// including the server's own {"error": "..."} body — unlike
// internal/api's own handlers (which deliberately hide internal details
// from callers), the CLI *is* the end user here, so the real error should
// reach them directly rather than being swallowed into a generic message.
func (c *APIClient) do(method, path string, body []byte, out interface{}) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error != "" {
			return fmt.Errorf("%s (%s)", apiErr.Error, resp.Status)
		}
		return fmt.Errorf("unexpected response: %s", resp.Status)
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}
