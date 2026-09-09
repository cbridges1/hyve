package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/types"
)

const invocationDelimiter = "===FAKE-KUBECTL-INVOCATION-END==="

// withFakeKubectl puts a fake "kubectl" script first on PATH for the
// duration of the test — kubectlApply/kubectlDeleteObjects (kubectl.go)
// always shell out to the real "kubectl" binary by name, exactly like
// every other reconcile-package function that touches a target cluster
// (there's no seam to mock at the Go level without changing that file),
// so this intercepts at the process-exec boundary instead. The fake
// script logs "<args joined by space>\n<stdin>\n---\n" per invocation to
// invocationsPath and exits 0, letting a test assert on exactly what
// hyve-agent's reconcile step tried to apply/delete without a real
// cluster.
func withFakeKubectl(t *testing.T) (invocationsPath string) {
	t.Helper()
	dir := t.TempDir()
	invocationsPath = filepath.Join(dir, "invocations.log")
	// A distinctive delimiter, not a bare "---": the rendered agent
	// manifests are themselves multi-document YAML full of ordinary "---"
	// separators, which would otherwise collide with this log's own
	// per-invocation boundary.
	script := "#!/bin/sh\n" +
		"{ echo \"$@\"; cat; echo '" + invocationDelimiter + "'; } >> " + invocationsPath + "\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return invocationsPath
}

func readInvocations(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(data)
}

// fakeAgentTokenIssuer counts calls and returns a fixed token — lets tests
// assert a token was (or wasn't) minted without a real Kubernetes clientset.
type fakeAgentTokenIssuer struct {
	calls int
	token string
	err   error
}

func (f *fakeAgentTokenIssuer) IssueBootstrapToken(_ context.Context, _, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

func newTestAgentReconciler(t *testing.T, issuer AgentTokenIssuer) *Reconciler {
	t.Helper()
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	r.AgentTokenIssuer = issuer
	r.AgentControlPlaneURL = "http://hyve-api.example.com"
	r.AgentTunnelAddress = "hyve-api.example.com:8092"
	r.AgentControlPlaneNamespace = "hyve-system"
	return r
}

func TestReconcileAgent_InstallsWhenEnabled(t *testing.T) {
	invocations := withFakeKubectl(t)
	issuer := &fakeAgentTokenIssuer{token: "test-bootstrap-token"}
	r := newTestAgentReconciler(t, issuer)

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec:     types.ClusterSpec{Agent: types.AgentSpec{Enabled: true}},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err)

	assert.Equal(t, 1, issuer.calls, "a fresh install must mint exactly one bootstrap token")
	require.NotNil(t, cluster.Spec.AppliedAgent)
	assert.NotEmpty(t, cluster.Spec.AppliedAgent.ConfigHash)
	assert.NotEmpty(t, cluster.Spec.AppliedAgent.AppliedAt)

	log := readInvocations(t, invocations)
	records := strings.Split(log, invocationDelimiter)
	require.NotEmpty(t, records)
	applyRecord := records[0]
	assert.Contains(t, applyRecord, "apply", "the core manifest must be applied")
	assert.Contains(t, applyRecord, "kind: Deployment")
	assert.Contains(t, applyRecord, "test-bootstrap-token", "the minted token must reach the rendered Deployment env")
	assert.NotContains(t, applyRecord, "hyve-agent-proxy", "the applied manifest must not include proxy bindings when Proxy is false")
	// Proxy off is still asserted deleted (idempotent no-op when it never
	// existed) — see reconcileAgent's own "safe to call unconditionally"
	// doc comment.
	assert.Contains(t, log, "delete ClusterRoleBinding "+agentProxyAdminBindingName)
}

func TestReconcileAgent_UpToDate_SkipsReapply(t *testing.T) {
	invocations := withFakeKubectl(t)
	issuer := &fakeAgentTokenIssuer{token: "unused"}
	r := newTestAgentReconciler(t, issuer)

	image := r.resolveAgentImage()
	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec: types.ClusterSpec{
			Agent:        types.AgentSpec{Enabled: true},
			AppliedAgent: &types.AppliedAgent{ConfigHash: agentConfigHash(false, image, r.AgentControlPlaneURL, r.AgentTunnelAddress), AppliedAt: "2026-01-01T00:00:00Z"},
		},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err)

	assert.Equal(t, 0, issuer.calls, "an unchanged config must not mint a new token")
	assert.Empty(t, readInvocations(t, invocations), "an unchanged config must not touch kubectl at all")
}

func TestReconcileAgent_RemovesWhenDisabledAfterInstall(t *testing.T) {
	invocations := withFakeKubectl(t)
	r := newTestAgentReconciler(t, &fakeAgentTokenIssuer{token: "unused"})

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec: types.ClusterSpec{
			Agent:        types.AgentSpec{Enabled: false},
			AppliedAgent: &types.AppliedAgent{ConfigHash: "stale", AppliedAt: "2026-01-01T00:00:00Z"},
		},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err)

	assert.Nil(t, cluster.Spec.AppliedAgent, "flipping enabled off must clear AppliedAgent")

	log := readInvocations(t, invocations)
	for _, obj := range append(agentCoreObjects(), agentProxyObjects()...) {
		assert.Contains(t, log, obj.Name, "removal must delete every core+proxy object by identity: missing %s", obj.Name)
	}
	assert.Contains(t, log, "delete")
}

func TestReconcileAgent_NeverInstalled_DisabledIsNoop(t *testing.T) {
	invocations := withFakeKubectl(t)
	r := newTestAgentReconciler(t, &fakeAgentTokenIssuer{token: "unused"})

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec:     types.ClusterSpec{Agent: types.AgentSpec{Enabled: false}},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err)
	assert.Nil(t, cluster.Spec.AppliedAgent)
	assert.Empty(t, readInvocations(t, invocations), "a cluster that never had an agent must not touch kubectl on a no-op disabled cycle")
}

// TestReconcileAgent_ProxyWithoutEnabled_Ignored is the plan's own required
// case: Agent.Proxy alone (Enabled: false) must be ignored, not rejected as
// a hard error — see AgentSpec.Enabled's own doc comment ("Proxy... requires
// this to also be true").
func TestReconcileAgent_ProxyWithoutEnabled_Ignored(t *testing.T) {
	invocations := withFakeKubectl(t)
	r := newTestAgentReconciler(t, &fakeAgentTokenIssuer{token: "unused"})

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec:     types.ClusterSpec{Agent: types.AgentSpec{Enabled: false, Proxy: true}},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err, "proxy-without-enabled must be ignored, not a hard error")
	assert.Nil(t, cluster.Spec.AppliedAgent)
	assert.Empty(t, readInvocations(t, invocations))
}

func TestReconcileAgent_ProxyEnabled_AppliesProxyBindings(t *testing.T) {
	invocations := withFakeKubectl(t)
	r := newTestAgentReconciler(t, &fakeAgentTokenIssuer{token: "test-token"})

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec:     types.ClusterSpec{Agent: types.AgentSpec{Enabled: true, Proxy: true}},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err)

	log := readInvocations(t, invocations)
	assert.Contains(t, log, agentProxyAdminBindingName)
	assert.Contains(t, log, agentProxyReadOnlyBindingName)
	assert.Contains(t, log, agentProxyAdminGroup)
	assert.Contains(t, log, agentProxyReadOnlyGroup)
}

func TestReconcileAgent_ProxyTurnedOff_RemovesProxyBindingsOnly(t *testing.T) {
	invocations := withFakeKubectl(t)
	r := newTestAgentReconciler(t, &fakeAgentTokenIssuer{token: "test-token"})

	image := r.resolveAgentImage()
	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec: types.ClusterSpec{
			Agent:        types.AgentSpec{Enabled: true, Proxy: false},
			AppliedAgent: &types.AppliedAgent{ConfigHash: agentConfigHash(true, image, r.AgentControlPlaneURL, r.AgentTunnelAddress), AppliedAt: "2026-01-01T00:00:00Z"},
		},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err)
	require.NotNil(t, cluster.Spec.AppliedAgent)
	assert.Equal(t, agentConfigHash(false, image, r.AgentControlPlaneURL, r.AgentTunnelAddress), cluster.Spec.AppliedAgent.ConfigHash)

	log := readInvocations(t, invocations)
	assert.Contains(t, log, agentProxyAdminBindingName)
	assert.Contains(t, log, "kind: Deployment", "core objects (Deployment included) must still be re-applied, not deleted, on a proxy-only change")
	// kubectlDeleteObjects issues one "delete <kind> <name> ..."
	// invocation per object — the Deployment must never be one of them:
	// Enabled is still true, only Proxy changed off, so the core install
	// (Deployment/ServiceAccount/etc.) is re-applied, never removed.
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "delete" {
			assert.NotEqual(t, "Deployment", fields[1], "the Deployment must not be a delete target on a proxy-only change: %q", line)
		}
	}
}

func TestReconcileAgent_SkipsWhenNotConfigured(t *testing.T) {
	invocations := withFakeKubectl(t)
	r := NewReconciler(&fakeStateProvider{localPath: t.TempDir()})
	// AgentTokenIssuer/AgentControlPlaneURL/AgentTunnelAddress all left
	// unset — matches the CLI's own default (no cluster-mode controller
	// wiring anything in).

	cluster := types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "acme-worker"},
		Spec:     types.ClusterSpec{Agent: types.AgentSpec{Enabled: true}},
	}

	err := r.reconcileAgent(context.Background(), &cluster, nil)
	require.NoError(t, err, "missing agent configuration must be a soft no-op, not an error")
	assert.Nil(t, cluster.Spec.AppliedAgent)
	assert.Empty(t, readInvocations(t, invocations))
}

func TestAgentConfigHash_ChangesWithProxyOrImageOrEndpoints(t *testing.T) {
	base := agentConfigHash(false, "image:v1", "http://cp.example.com", "cp.example.com:8092")
	assert.NotEqual(t, base, agentConfigHash(true, "image:v1", "http://cp.example.com", "cp.example.com:8092"), "proxy toggling must change the hash")
	assert.NotEqual(t, base, agentConfigHash(false, "image:v2", "http://cp.example.com", "cp.example.com:8092"), "image change must change the hash")
	assert.NotEqual(t, base, agentConfigHash(false, "image:v1", "http://cp2.example.com", "cp.example.com:8092"), "control-plane URL change must change the hash")
	assert.NotEqual(t, base, agentConfigHash(false, "image:v1", "http://cp.example.com", "cp2.example.com:8092"), "tunnel address change must change the hash")
	assert.Equal(t, base, agentConfigHash(false, "image:v1", "http://cp.example.com", "cp.example.com:8092"), "identical inputs must hash identically")
}
