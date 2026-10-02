package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cbridges1/hyve/cmd/shared"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunWorkflowClusterMode_LocalName_Success exercises the full happy
// path: POST /api/workflow-runs with a local workflow name -> GET
// /api/workflow-runs/<name> immediately reports Succeeded -> the function
// returns normally (no log.Fatal) with no further polling.
func TestRunWorkflowClusterMode_LocalName_Success(t *testing.T) {
	var createBody struct {
		Workflow string            `json:"workflow,omitempty"`
		Source   string            `json:"source,omitempty"`
		Cluster  string            `json:"cluster"`
		Params   map[string]string `json:"params,omitempty"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/workflow-runs":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createBody))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "install-podinfo-abc123"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/workflow-runs/install-podinfo-abc123":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"phase":  "Succeeded",
				"output": "✅ podinfo installed",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &shared.APIClient{BaseURL: srv.URL}
	runWorkflowClusterMode(client, "install-podinfo", "", "demo", true, map[string]string{"FOO": "bar"})

	assert.Equal(t, "install-podinfo", createBody.Workflow)
	assert.Empty(t, createBody.Source, "a bare name must never be sent as source")
	assert.Equal(t, "demo", createBody.Cluster)
	assert.Equal(t, map[string]string{"FOO": "bar"}, createBody.Params)
}

// TestRunWorkflowClusterMode_RemoteSource_SplitsCorrectly confirms an
// argument containing "/" is sent as source, not workflow — matching local
// mode's own looksLikeRemoteSource resolution.
func TestRunWorkflowClusterMode_RemoteSource_SplitsCorrectly(t *testing.T) {
	var createBody struct {
		Workflow string `json:"workflow,omitempty"`
		Source   string `json:"source,omitempty"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/workflow-runs":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createBody))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "run-xyz"})
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"phase": "Succeeded"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &shared.APIClient{BaseURL: srv.URL}
	runWorkflowClusterMode(client, "github.com/org/repo//workflows/x.yaml", "", "demo", false, nil)

	assert.Empty(t, createBody.Workflow)
	assert.Equal(t, "github.com/org/repo//workflows/x.yaml", createBody.Source)
}
