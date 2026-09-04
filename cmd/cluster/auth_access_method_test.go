package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/kubeconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunAccessMethodAuth_NoRefReturnsFalse confirms a cluster with no
// accessMethodRef set never even calls the resolver — the caller falls
// through to its own existing auth logic untouched.
func TestRunAccessMethodAuth_NoRefReturnsFalse(t *testing.T) {
	called := false
	resolver := func(ref string) (string, string, error) {
		called = true
		return "", "", nil
	}
	handled := runAccessMethodAuth("my-cluster", "", "", resolver)
	assert.False(t, handled)
	assert.False(t, called, "resolver must not be called when accessMethodRef is unset")
}

// TestRunAccessMethodAuth_RancherSuccess_MergesKubeconfig exercises the
// full success path end to end: resolver -> Login (RANCHER_TOKEN reused,
// login skipped) -> GenerateKubeconfig -> merge into ~/.kube/config —
// confirming this genuinely never needs a hyve API round trip for the
// credential exchange, only a resolver call and a direct Rancher round
// trip.
func TestRunAccessMethodAuth_RancherSuccess_MergesKubeconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RANCHER_TOKEN", "already-held-token")

	const kc = "apiVersion: v1\nkind: Config\nclusters:\n- name: prod\n  cluster:\n    server: https://rancher-managed.example.com\nusers:\n- name: prod\n  user:\n    token: tok\ncontexts:\n- name: prod\n  context:\n    cluster: prod\n    user: prod\ncurrent-context: prod\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer already-held-token", r.Header.Get("Authorization"), "must reuse RANCHER_TOKEN, never prompt when it's already set")
		assert.Equal(t, "/v3/clusters/c-abc123", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Config string `json:"config"`
		}{Config: kc})
	}))
	defer srv.Close()

	resolver := func(ref string) (string, string, error) {
		assert.Equal(t, "corp-rancher", ref)
		return hyvev1alpha1.AccessMethodProviderRancher, srv.URL, nil
	}

	handled := runAccessMethodAuth("my-cluster", "corp-rancher", "c-abc123", resolver)
	assert.True(t, handled)

	defaultPath := filepath.Join(home, ".kube", "config")
	names, err := kubeconfig.ContextNames(readFile(t, defaultPath))
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, names, "the merged entry must be named after the hyve cluster, not Rancher's own context name")
}
