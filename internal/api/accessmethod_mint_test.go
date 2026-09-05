package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestModulesDirWithAccessMethodAuth creates a temp ModulesDir
// containing an auth-only module at modules/rancher-access — matching an
// AccessMethod whose Driver.Source is "./modules/rancher-access" (a
// local, no-hyve.lock-needed source, see module.resolveLocal) — with a
// declared tool requirement, so handleAccessMethodMint has something real
// to resolve and read.
func newTestModulesDirWithAccessMethodAuth(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "modules", "rancher-access")
	require.NoError(t, os.MkdirAll(moduleDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "module.yaml"), []byte(`apiVersion: v1
kind: Module
metadata:
  name: rancher-access
  version: 1.0.0
  type: authOnly
spec:
  requirements:
    tools:
      - name: curl
        description: Used to call Rancher's REST API
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "auth.yaml"), []byte(`apiVersion: v1
kind: ClusterAuth
metadata:
  name: auth
spec:
  methods:
    - name: default
      auth:
        script: "echo mint-via $HYVE_ACCESS_METHOD_SERVER_URL > \"$KUBECONFIG\""
      exports: KUBECONFIG
`), 0644))
	return dir
}

func newAccessMethodDefWithDriver(name, source string) *hyvev1alpha1.AccessMethod {
	return &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: hyvev1alpha1.AccessMethodSpec{
			Driver:    hyvev1alpha1.DriverRef{Source: source, Version: "v1.0.0"},
			ServerURL: "https://rancher.example.com",
		},
	}
}

const testMintKubeconfig = "apiVersion: v1\nkind: Config\nclusters:\n- name: prod\n  cluster:\n    server: https://rancher-managed.example.com\n"

func newMintMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAccessMethodMintRoutes(mux)
	return mux
}

func newRelayMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAccessMethodMintRelayRoutes(mux)
	return mux
}

// findPendingMint polls s.mintPending until exactly one entry appears —
// handleAccessMethodMint registers it before creating the Job, so this
// reliably observes the request a concurrently-running test goroutine
// dispatched, without an arbitrary sleep.
func findPendingMint(t *testing.T, s *Server) (requestID string, entry *mintPendingEntry) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		found := false
		s.mintPending.Range(func(k, v interface{}) bool {
			requestID = k.(string)
			entry = v.(*mintPendingEntry)
			found = true
			return false
		})
		if found {
			return requestID, entry
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending mint request appeared")
	return "", nil
}

func TestHandleAccessMethodMint_Success(t *testing.T) {
	am := newAccessMethodDefWithDriver("corp-rancher", "./modules/rancher-access")
	s := &Server{
		Client:       newFakeClient(t, am),
		Namespace:    testNamespace,
		ModulesDir:   newTestModulesDirWithAccessMethodAuth(t),
		Clientset:    fake.NewClientset(),
		RelayBaseURL: "http://relay.internal",
		MintTimeout:  2 * time.Second,
	}

	relaySrv := httptest.NewServer(newRelayMux(s))
	defer relaySrv.Close()

	reqBody, err := json.Marshal(map[string]any{
		"clusterName":           "my-cluster",
		"accessMethodClusterID": "c-abc123",
		"credentialEnv":         map[string]string{"RANCHER_TOKEN": "tok"},
	})
	require.NoError(t, err)

	type mintResult struct {
		code int
		body []byte
	}
	resultCh := make(chan mintResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/access-methods/corp-rancher/mint", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()
		newMintMux(s).ServeHTTP(rec, req)
		resultCh <- mintResult{code: rec.Code, body: rec.Body.Bytes()}
	}()

	requestID, entry := findPendingMint(t, s)

	// Confirm the Job was actually created (with the credentials Secret
	// referenced via envFrom, never a literal pod-spec env var) before
	// simulating its own push — this is the one place that invariant is
	// checkable at all, since a real Job never actually runs in this test.
	jobs, err := s.Clientset.BatchV1().Jobs(testNamespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)
	container := jobs.Items[0].Spec.Template.Spec.Containers[0]
	require.Len(t, container.EnvFrom, 1)
	secretName := container.EnvFrom[0].SecretRef.Name
	for _, e := range container.Env {
		assert.NotEqual(t, "RANCHER_TOKEN", e.Name, "the credential must never be a literal pod-spec env var")
	}

	secret, err := s.Clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "tok", secret.StringData["RANCHER_TOKEN"])
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, jobs.Items[0].Name, secret.OwnerReferences[0].Name)
	assert.Equal(t, jobs.Items[0].UID, secret.OwnerReferences[0].UID)

	relayReq, err := http.NewRequest(http.MethodPost, relaySrv.URL+"/relay/"+requestID, bytes.NewReader([]byte(testMintKubeconfig)))
	require.NoError(t, err)
	relayReq.Header.Set("Authorization", "Bearer "+entry.token)
	relayReq.Header.Set("X-Hyve-Status", "ok")
	relayResp, err := http.DefaultClient.Do(relayReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, relayResp.StatusCode)

	res := <-resultCh
	require.Equal(t, http.StatusOK, res.code, string(res.body))
	var out mintAccessMethodResponse
	require.NoError(t, json.Unmarshal(res.body, &out))
	assert.Equal(t, testMintKubeconfig, out.Kubeconfig)

	// The Job must be cleaned up once the result is delivered — nothing
	// left behind for an operator to separately notice and remove.
	jobs, err = s.Clientset.BatchV1().Jobs(testNamespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items)
}

// TestHandleAccessMethodMint_InlineAuth_Success mirrors
// TestHandleAccessMethodMint_Success but with an AccessMethod using
// InlineAuth instead of Driver — confirming the whole mint flow works
// with zero ModulesDir/module-resolution involvement at all (no
// hyve.lock, no local-path or git source, nothing to go stale across an
// API pod restart).
func TestHandleAccessMethodMint_InlineAuth_Success(t *testing.T) {
	am := &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: "corp-rancher", Namespace: testNamespace},
		Spec: hyvev1alpha1.AccessMethodSpec{
			ServerURL:   "https://rancher.example.com",
			InlineAuth:  "echo mint-via $HYVE_ACCESS_METHOD_SERVER_URL > \"$KUBECONFIG\"",
			RequiredEnv: []string{"RANCHER_TOKEN"},
		},
	}
	s := &Server{
		Client:       newFakeClient(t, am),
		Namespace:    testNamespace,
		Clientset:    fake.NewClientset(),
		RelayBaseURL: "http://relay.internal",
		MintTimeout:  2 * time.Second,
	}

	relaySrv := httptest.NewServer(newRelayMux(s))
	defer relaySrv.Close()

	reqBody, err := json.Marshal(map[string]any{
		"clusterName":           "my-cluster",
		"accessMethodClusterID": "c-abc123",
		"credentialEnv":         map[string]string{"RANCHER_TOKEN": "tok"},
	})
	require.NoError(t, err)

	type mintResult struct {
		code int
		body []byte
	}
	resultCh := make(chan mintResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/access-methods/corp-rancher/mint", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()
		newMintMux(s).ServeHTTP(rec, req)
		resultCh <- mintResult{code: rec.Code, body: rec.Body.Bytes()}
	}()

	requestID, entry := findPendingMint(t, s)

	// Confirm the dispatched Job's script is the InlineAuth text directly
	// (wrapped for the push), never anything resolved from a module.
	jobs, err := s.Clientset.BatchV1().Jobs(testNamespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)
	script := jobs.Items[0].Spec.Template.Spec.Containers[0].Command[2]
	assert.Contains(t, script, "echo mint-via $HYVE_ACCESS_METHOD_SERVER_URL")

	relayReq, err := http.NewRequest(http.MethodPost, relaySrv.URL+"/relay/"+requestID, bytes.NewReader([]byte(testMintKubeconfig)))
	require.NoError(t, err)
	relayReq.Header.Set("Authorization", "Bearer "+entry.token)
	relayReq.Header.Set("X-Hyve-Status", "ok")
	relayResp, err := http.DefaultClient.Do(relayReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, relayResp.StatusCode)

	res := <-resultCh
	require.Equal(t, http.StatusOK, res.code, string(res.body))
	var out mintAccessMethodResponse
	require.NoError(t, json.Unmarshal(res.body, &out))
	assert.Equal(t, testMintKubeconfig, out.Kubeconfig)
}

func TestResolveAccessMethodAuthScript_InlineAuth(t *testing.T) {
	s := &Server{}
	am := &hyvev1alpha1.AccessMethod{Spec: hyvev1alpha1.AccessMethodSpec{InlineAuth: "echo hi"}}
	script, err := s.resolveAccessMethodAuthScript(context.Background(), "corp-rancher", am)
	require.NoError(t, err)
	assert.Equal(t, "echo hi", script)
}

func TestResolveAccessMethodAuthScript_BothSet_Error(t *testing.T) {
	s := &Server{}
	am := &hyvev1alpha1.AccessMethod{Spec: hyvev1alpha1.AccessMethodSpec{
		InlineAuth: "echo hi",
		Driver:     hyvev1alpha1.DriverRef{Source: "github.com/example/mod"},
	}}
	_, err := s.resolveAccessMethodAuthScript(context.Background(), "corp-rancher", am)
	assert.Error(t, err)
}

func TestResolveAccessMethodAuthScript_NeitherSet_Error(t *testing.T) {
	s := &Server{}
	am := &hyvev1alpha1.AccessMethod{}
	_, err := s.resolveAccessMethodAuthScript(context.Background(), "corp-rancher", am)
	assert.Error(t, err)
}

// TestHandleAccessMethodMintRelay_WrongTokenRejected confirms a POST with
// an incorrect token never resolves the pending request — the real
// caller's own later, correctly-authenticated push must still be able to
// complete it.
func TestHandleAccessMethodMintRelay_WrongTokenRejected(t *testing.T) {
	am := newAccessMethodDefWithDriver("corp-rancher", "./modules/rancher-access")
	s := &Server{
		Client:       newFakeClient(t, am),
		Namespace:    testNamespace,
		ModulesDir:   newTestModulesDirWithAccessMethodAuth(t),
		Clientset:    fake.NewClientset(),
		RelayBaseURL: "http://relay.internal",
		MintTimeout:  2 * time.Second,
	}

	s.mintPending.Store("req-1", &mintPendingEntry{token: "real-token", result: make(chan mintPushResult, 1)})

	req := httptest.NewRequest(http.MethodPost, "/relay/req-1", bytes.NewReader([]byte("data")))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	newRelayMux(s).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	_, stillPending := s.mintPending.Load("req-1")
	assert.True(t, stillPending, "a wrong-token POST must not consume the pending request")
}

func TestHandleAccessMethodMintRelay_UnknownRequestID_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	req := httptest.NewRequest(http.MethodPost, "/relay/does-not-exist", bytes.NewReader([]byte("data")))
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()
	newRelayMux(s).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestExtractAuthScript_YAMLClusterAuth(t *testing.T) {
	content := []byte(`apiVersion: v1
kind: ClusterAuth
metadata:
  name: test
spec:
  methods:
    - name: default
      auth:
        script: "echo hi"
      exports: KUBECONFIG
`)
	script, err := extractAuthScript("auth.yaml", content)
	require.NoError(t, err)
	assert.Equal(t, "echo hi", script)
}

func TestExtractAuthScript_PlainShellScript(t *testing.T) {
	script, err := extractAuthScript("auth.sh", []byte("#!/bin/sh\necho hi\n"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho hi\n", script)
}

func TestBuildMintWrapperScript_ContainsPushLogic(t *testing.T) {
	script := buildMintWrapperScript("echo hi > \"$KUBECONFIG\"")
	assert.Contains(t, script, "echo hi")
	assert.Contains(t, script, "HYVE_RELAY_URL")
	assert.Contains(t, script, "HYVE_RELAY_TOKEN")
	assert.Contains(t, script, "X-Hyve-Status: ok")
}
