package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMintKubeconfig = "apiVersion: v1\nkind: Config\nclusters:\n- name: prod\n  cluster:\n    server: https://rancher-managed.example.com\nusers:\n- name: prod\n  user:\n    token: tok\ncontexts:\n- name: prod\n  context:\n    cluster: prod\n    user: prod\ncurrent-context: prod\n"

func TestRunAccessMethodAuthCluster_NoRefReturnsFalse(t *testing.T) {
	client := &shared.APIClient{BaseURL: "http://unused.invalid"}
	handled := runAccessMethodAuthCluster("my-cluster", "", "", client, nil)
	assert.False(t, handled)
}

// TestRunAccessMethodAuthCluster_Success_MergesKubeconfig exercises the
// full path end to end: GET .../<ref> (learns RequiredEnv) -> reads those
// exact names from the process's own environment -> POST .../<ref>/mint
// with only those as credentialEnv -> merges the returned kubeconfig into
// ~/.kube/config. Confirms the driver module's auth operation is never
// run by this process at all — only two plain HTTP calls.
func TestRunAccessMethodAuthCluster_Success_MergesKubeconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RANCHER_USERNAME", "alice")
	t.Setenv("RANCHER_PASSWORD", "s3cret")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/access-methods/corp-rancher":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "corp-rancher",
				"spec":        map[string]any{"serverURL": "https://rancher.example.com", "driver": map[string]any{"source": "x", "version": "v1"}},
				"requiredEnv": []string{"RANCHER_USERNAME", "RANCHER_PASSWORD"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/access-methods/corp-rancher/mint":
			var body struct {
				ClusterName           string            `json:"clusterName"`
				AccessMethodClusterID string            `json:"accessMethodClusterID"`
				CredentialEnv         map[string]string `json:"credentialEnv"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "my-cluster", body.ClusterName)
			assert.Equal(t, "c-abc123", body.AccessMethodClusterID)
			assert.Equal(t, map[string]string{"RANCHER_USERNAME": "alice", "RANCHER_PASSWORD": "s3cret"}, body.CredentialEnv, "must forward exactly RequiredEnv, never the caller's whole environment")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"kubeconfig": testMintKubeconfig})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &shared.APIClient{BaseURL: srv.URL}
	handled := runAccessMethodAuthCluster("my-cluster", "corp-rancher", "c-abc123", client, nil)
	assert.True(t, handled)

	defaultPath := filepath.Join(home, ".kube", "config")
	names, err := kubeconfig.ContextNames(readFile(t, defaultPath))
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, names, "the merged entry must be named after the hyve cluster, not the module's own context name")

	_, statErr := os.Stat(filepath.Join(home, ".hyve", "kubeconfigs", "my-cluster.yaml"))
	assert.True(t, os.IsNotExist(statErr), "the mint path must merge directly, never write a ~/.hyve/kubeconfigs staging file")
}

// TestRunAccessMethodAuthCluster_CredentialParams_TakePrecedenceOverEnv
// confirms --set KEY=VALUE (credentialParams) satisfies a required
// credential without needing it in the environment at all, and wins over
// the environment when both are set.
func TestRunAccessMethodAuthCluster_CredentialParams_TakePrecedenceOverEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RANCHER_USERNAME", "env-user")
	// RANCHER_PASSWORD deliberately never set in the environment at all —
	// must come entirely from credentialParams.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/access-methods/corp-rancher":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "corp-rancher",
				"spec":        map[string]any{"serverURL": "https://rancher.example.com", "driver": map[string]any{"source": "x", "version": "v1"}},
				"requiredEnv": []string{"RANCHER_USERNAME", "RANCHER_PASSWORD"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/access-methods/corp-rancher/mint":
			var body struct {
				CredentialEnv map[string]string `json:"credentialEnv"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]string{"RANCHER_USERNAME": "param-user", "RANCHER_PASSWORD": "param-pass"}, body.CredentialEnv,
				"--set must win over the same name already in the environment, and must satisfy a name absent from the environment entirely")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"kubeconfig": testMintKubeconfig})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &shared.APIClient{BaseURL: srv.URL}
	credentialParams := map[string]string{"RANCHER_USERNAME": "param-user", "RANCHER_PASSWORD": "param-pass"}
	handled := runAccessMethodAuthCluster("my-cluster", "corp-rancher", "c-abc123", client, credentialParams)
	assert.True(t, handled)
}
