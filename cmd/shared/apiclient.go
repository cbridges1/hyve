package shared

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/session"
)

// ErrClientSideAuthUnavailable is returned by GetAuthContext when the
// target cluster doesn't use the default client-side auth method (see
// internal/api's handleAuthContext) — it's opted into the server-side
// module-auth override or tunnel access instead. Callers should fall back
// to GetKubeconfig.
var ErrClientSideAuthUnavailable = errors.New("cluster does not use client-side auth")

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

// NewAPIClient builds a client from the current session — callers should
// already have gone through UseClusterMode/EnsureValidSession, which
// guarantee AccessToken is current.
func NewAPIClient(sess *session.Session) *APIClient {
	return &APIClient{BaseURL: strings.TrimRight(sess.APIURL, "/"), Token: sess.AccessToken}
}

// UseClusterMode reports whether the current command should talk to the API
// instead of local files: a usable session exists (see EnsureValidSession,
// which silently refreshes an expired access token before this ever has to
// decide anything). Session presence deliberately wins over any other
// signal — no separate flag needed, and `hyve logout` cleanly reverts to
// local mode. Returns the session as well so callers don't need to reload
// it.
//
// Never having logged in returns (nil, false) — local mode is correct
// there, no different from before. Having logged in but ending up with
// nothing usable (the session itself expired, or the server rejected a
// refresh — e.g. revoked by `hyve logout` elsewhere) is deliberately NOT
// treated the same way: every caller of this function is a "which backend
// do I talk to" dispatch point (cluster API vs. local clusters/*.yaml),
// and for a cluster-mode environment, its local clusters/ directory is not
// a second source of truth — it's frequently empty or stale, since the
// real state lives in the cluster's CRDs. Silently falling through to the
// local branch there would let a command like `hyve cluster delete`
// operate on stale local files and run reconciliation directly against a
// cloud provider from this machine, bypassing the controller entirely —
// with no indication to the user that anything unusual happened. So this
// hard-fails instead, forcing an explicit `hyve login` before any command
// proceeds down either branch. (`hyve whoami`/`hyve env list`/`hyve env
// current` do not call this — they read the session directly and report
// expiry as information, not a fatal error, since they're the tools meant
// for diagnosing exactly this situation.)
func UseClusterMode() (*session.Session, bool) {
	sess, err := EnsureValidSession()
	if sess == nil {
		return nil, false
	}
	if err != nil {
		log.Fatalf("❌ %v — this is a cluster-mode environment (API: %s), not a local one. Refusing to silently fall back to local file operations, which could target stale or missing state instead of the live cluster.\n\nRun 'hyve login --api-url %s' to re-authenticate.", err, sess.APIURL, sess.APIURL)
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

// ClusterResourcesDTO mirrors internal/api's clusterResourcesDTO — a
// separate endpoint from ClusterDTO so the base cluster list/get responses
// stay lean. Uses the real hyvev1alpha1 types directly rather than a hand
// duplicated mirror (unlike ClusterDTO/TemplateDTO/WorkflowDTO) — resources
// are a deeply nested shape (Helm/Secret variants, applied-object lists)
// where duplicating the whole tree just for display isn't worth it, and
// nothing here is sensitive enough to need narrowing.
type ClusterResourcesDTO struct {
	Resources        []hyvev1alpha1.ResourceRef               `json:"resources,omitempty"`
	AppliedResources map[string]*hyvev1alpha1.AppliedResource `json:"appliedResources,omitempty"`
}

func (c *APIClient) GetClusterResources(name string) (*ClusterResourcesDTO, error) {
	var out ClusterResourcesDTO
	if err := c.do(http.MethodGet, "/api/clusters/"+name+"/resources", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateClusterFromTemplate posts {name, template: {name, region, params}}
// to POST /api/clusters — the cluster-mode counterpart to local mode's
// --template flow, rendered server-side via the same
// hyvev1alpha1.RenderClusterDefinitionSpec function.
func (c *APIClient) CreateClusterFromTemplate(name, templateName, region string, params map[string]string) (*ClusterDTO, error) {
	body, err := json.Marshal(struct {
		Name     string `json:"name"`
		Template struct {
			Name   string            `json:"name"`
			Region string            `json:"region,omitempty"`
			Params map[string]string `json:"params,omitempty"`
		} `json:"template"`
	}{
		Name: name,
		Template: struct {
			Name   string            `json:"name"`
			Region string            `json:"region,omitempty"`
			Params map[string]string `json:"params,omitempty"`
		}{Name: templateName, Region: region, Params: params},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out ClusterDTO
	if err := c.do(http.MethodPost, "/api/clusters", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TemplateDTO mirrors internal/api's templateDTO — Spec is kept as raw JSON
// rather than a fully duplicated hyvev1alpha1.TemplateSpec mirror (unlike
// ClusterDTO, nothing in a Template is sensitive, so there's no narrowing
// to justify hand-typing every nested field here too).
type TemplateDTO struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

func (c *APIClient) ListTemplates() ([]TemplateDTO, error) {
	var out []TemplateDTO
	if err := c.do(http.MethodGet, "/api/templates", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *APIClient) GetTemplate(name string) (*TemplateDTO, error) {
	var out TemplateDTO
	if err := c.do(http.MethodGet, "/api/templates/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) CreateTemplate(name string, spec json.RawMessage) (*TemplateDTO, error) {
	body, err := json.Marshal(struct {
		Name string          `json:"name"`
		Spec json.RawMessage `json:"spec"`
	}{Name: name, Spec: spec})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out TemplateDTO
	if err := c.do(http.MethodPost, "/api/templates", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) DeleteTemplate(name string) error {
	return c.do(http.MethodDelete, "/api/templates/"+name, nil, nil)
}

// RenderTemplate calls POST /api/templates/<name>/render and returns the
// rendered ClusterDefinitionSpec as raw JSON — a preview, no cluster
// created.
func (c *APIClient) RenderTemplate(name, region string, params map[string]string) (json.RawMessage, error) {
	body, err := json.Marshal(struct {
		Region string            `json:"region,omitempty"`
		Params map[string]string `json:"params,omitempty"`
	}{Region: region, Params: params})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out json.RawMessage
	if err := c.do(http.MethodPost, "/api/templates/"+name+"/render", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WorkflowDTO mirrors internal/api's workflowDTO — same raw-Spec rationale
// as TemplateDTO.
type WorkflowDTO struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

func (c *APIClient) ListWorkflows() ([]WorkflowDTO, error) {
	var out []WorkflowDTO
	if err := c.do(http.MethodGet, "/api/workflows", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *APIClient) GetWorkflow(name string) (*WorkflowDTO, error) {
	var out WorkflowDTO
	if err := c.do(http.MethodGet, "/api/workflows/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) CreateWorkflow(name string, spec json.RawMessage) (*WorkflowDTO, error) {
	body, err := json.Marshal(struct {
		Name string          `json:"name"`
		Spec json.RawMessage `json:"spec"`
	}{Name: name, Spec: spec})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out WorkflowDTO
	if err := c.do(http.MethodPost, "/api/workflows", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) DeleteWorkflow(name string) error {
	return c.do(http.MethodDelete, "/api/workflows/"+name, nil, nil)
}

// ResourceDTO mirrors internal/api's resourceDTO — same raw-Spec rationale
// as WorkflowDTO.
type ResourceDTO struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

func (c *APIClient) ListResources() ([]ResourceDTO, error) {
	var out []ResourceDTO
	if err := c.do(http.MethodGet, "/api/resources", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *APIClient) GetResource(name string) (*ResourceDTO, error) {
	var out ResourceDTO
	if err := c.do(http.MethodGet, "/api/resources/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) CreateResource(name string, spec json.RawMessage) (*ResourceDTO, error) {
	body, err := json.Marshal(struct {
		Name string          `json:"name"`
		Spec json.RawMessage `json:"spec"`
	}{Name: name, Spec: spec})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out ResourceDTO
	if err := c.do(http.MethodPost, "/api/resources", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *APIClient) DeleteResource(name string) error {
	return c.do(http.MethodDelete, "/api/resources/"+name, nil, nil)
}

// ListSecretKeys returns every secret key name — readable by any
// authenticated role, since names alone aren't sensitive (see
// internal/api/secrets.go's handleListSecrets).
func (c *APIClient) ListSecretKeys() ([]string, error) {
	var out []string
	if err := c.do(http.MethodGet, "/api/secrets", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSecretValues returns every secret key/value pair in one round trip —
// requires RoleAdmin server-side.
func (c *APIClient) ListSecretValues() (map[string]string, error) {
	var out map[string]string
	if err := c.do(http.MethodGet, "/api/secrets?values=true", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSecret returns a single key's value — requires RoleAdmin server-side.
func (c *APIClient) GetSecret(key string) (string, error) {
	var out struct {
		Value string `json:"value"`
	}
	if err := c.do(http.MethodGet, "/api/secrets/"+key, nil, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// SetSecret adds or updates a single key — requires RoleAdmin server-side.
func (c *APIClient) SetSecret(key, value string) error {
	body, err := json.Marshal(struct {
		Value string `json:"value"`
	}{Value: value})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	return c.do(http.MethodPut, "/api/secrets/"+key, body, nil)
}

// UnsetSecret removes a single key — requires RoleAdmin server-side.
func (c *APIClient) UnsetSecret(key string) error {
	return c.do(http.MethodDelete, "/api/secrets/"+key, nil, nil)
}

// ModuleDTO mirrors internal/api's moduleDTO — read-only, since the API
// never writes Module CRs (only the controller does, auto-resolving as it
// reconciles a ClusterDefinition — see
// internal/controller/reconciler.go's resolveModuleIfNeeded).
type ModuleDTO struct {
	Name   string                    `json:"name"`
	Spec   hyvev1alpha1.ModuleSpec   `json:"spec"`
	Status hyvev1alpha1.ModuleStatus `json:"status"`
}

func (c *APIClient) ListModules() ([]ModuleDTO, error) {
	var out []ModuleDTO
	if err := c.do(http.MethodGet, "/api/modules", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *APIClient) GetModule(name string) (*ModuleDTO, error) {
	var out ModuleDTO
	if err := c.do(http.MethodGet, "/api/modules/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AuthContextDTO mirrors internal/api's authContextDTO — everything needed
// to run a module's auth operation entirely client-side, with no local
// hyve.lock/module resolution: AuthFileContent is the resolved auth
// operation file's raw bytes as the server-side module cache already has
// it, AuthFileName is the filename to write them under (e.g. "auth.yaml")
// so Executor's own extension-based dispatch (module.FindOperationFile)
// picks the right execution path once it exists on disk locally.
type AuthContextDTO struct {
	DriverSource    string                   `json:"driverSource"`
	DriverVersion   string                   `json:"driverVersion"`
	Region          string                   `json:"region,omitempty"`
	Params          map[string]string        `json:"params,omitempty"`
	DriverOutputs   map[string]string        `json:"driverOutputs,omitempty"`
	AuthFileName    string                   `json:"authFileName"`
	AuthFileContent string                   `json:"authFileContent"`
	Tools           []AuthToolRequirementDTO `json:"tools,omitempty"`
}

// AuthToolRequirementDTO mirrors internal/api's authToolRequirement.
type AuthToolRequirementDTO struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// GetAuthContext calls GET /api/clusters/<name>/auth-context. A 409
// response (the cluster doesn't use client-side auth) is reported as
// ErrClientSideAuthUnavailable rather than a generic error, so callers can
// distinguish "fall back to GetKubeconfig" from a real failure.
func (c *APIClient) GetAuthContext(clusterName string) (*AuthContextDTO, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/clusters/"+url.PathEscape(clusterName)+"/auth-context", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusConflict {
		return nil, ErrClientSideAuthUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("%s (%s)", apiErr.Error, resp.Status)
		}
		return nil, fmt.Errorf("unexpected response: %s", resp.Status)
	}
	var out AuthContextDTO
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// GetKubeconfig calls GET /api/kubeconfig?cluster=<name> and returns the raw
// kubeconfig YAML document. Unlike every other APIClient method, the
// response isn't JSON (see internal/api's handleKubeconfig — it writes
// application/yaml directly), so this bypasses do()'s JSON decoding rather
// than trying to force it through the same path.
func (c *APIClient) GetKubeconfig(clusterName string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api/kubeconfig?cluster="+url.QueryEscape(clusterName), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("%s (%s)", apiErr.Error, resp.Status)
		}
		return nil, fmt.Errorf("unexpected response: %s", resp.Status)
	}
	return body, nil
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
