package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestAgentProxyRBAC_ImpersonationDecision is the plan's own required
// case: role→impersonation-group mapping verified against a real
// apiserver's own RBAC decision, not just that the right header string
// got built. buildAgentReverseProxy's Director (agent_proxy.go) sets raw
// Impersonate-User/Impersonate-Group HTTP headers; rest.Config.Impersonate
// produces the exact same wire-level headers via client-go's own
// supported mechanism, so exercising it here validates the real claim —
// "does agentpki.AgentProxyAdminGroup/AgentProxyReadOnlyGroup, presented
// as Impersonate-Group against a target cluster carrying the same two
// ClusterRoleBindings internal/reconcile/agent.go provisions, actually
// produce the intended authorization decision" — without needing to stand
// up this package's own HTTP handler, a fake SSH tunnel, and a fake agent
// connection just to reach the same two header values.
//
// Uses sigs.k8s.io/controller-runtime/pkg/envtest (a real, local
// kube-apiserver + etcd, RBAC authorization mode by default) rather than
// any fake/mocked client — cross-tenant/plumbing tests in
// agent_proxy_test.go cover this package's own routing logic; this test
// is deliberately narrow, covering only the RBAC decision itself. Skips
// (does not fail) when envtest's binary assets aren't available locally,
// so `go test ./...` stays runnable in an environment that never
// provisioned them (this project has no other envtest-based test —
// e.g. via `setup-envtest use` or KUBEBUILDER_ASSETS).
// findEnvtestAssetsDir locates a directory actually containing
// etcd/kube-apiserver binaries — not merely the parent "store" directory
// SetupEnvtestDefaultBinaryAssetsDirectory returns (which setup-envtest
// nests one more level under, by k8s version+platform, e.g.
// ".../k8s/1.36.2-darwin-arm64/"; confirmed live: passing the un-nested
// parent directly to envtest.Environment fails with a literal "no such
// file or directory" trying to exec ".../k8s/etcd"). Returns "" if
// nothing usable is found, letting the caller skip rather than fail.
func findEnvtestAssetsDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" && hasEnvtestBinaries(dir) {
		return dir
	}
	root, err := envtest.SetupEnvtestDefaultBinaryAssetsDirectory()
	if err != nil {
		return ""
	}
	if hasEnvtestBinaries(root) {
		return root
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(root, e.Name())
		if hasEnvtestBinaries(candidate) {
			return candidate
		}
	}
	return ""
}

func hasEnvtestBinaries(dir string) bool {
	for _, bin := range []string{"etcd", "kube-apiserver"} {
		if _, err := os.Stat(filepath.Join(dir, bin)); err != nil {
			return false
		}
	}
	return true
}

func TestAgentProxyRBAC_ImpersonationDecision(t *testing.T) {
	assetsDir := findEnvtestAssetsDir(t)
	if assetsDir == "" {
		t.Skip("envtest binary assets not found — skipping (see `setup-envtest use`)")
	}

	env := &envtest.Environment{BinaryAssetsDirectory: assetsDir}
	adminCfg, err := env.Start()
	if err != nil {
		t.Skipf("failed to start envtest environment: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	adminClientset, err := kubernetes.NewForConfig(adminCfg)
	require.NoError(t, err)

	ctx := context.Background()
	const namespace = "default"
	const serviceAccountName = "test-hyve-agent"

	// Mirrors internal/reconcile/agent.go's own hyve-agent-impersonator
	// ClusterRole exactly: impersonate only, nothing else — the whole
	// point being verified below is that this identity alone can do
	// nothing besides impersonate.
	_, err = adminClientset.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: serviceAccountName, Namespace: namespace},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = adminClientset.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hyve-agent-impersonator"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"users", "groups", "serviceaccounts"},
			Verbs:     []string{"impersonate"},
		}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = adminClientset.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hyve-agent-impersonator"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: serviceAccountName, Namespace: namespace}},
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "test-hyve-agent-impersonator", APIGroup: "rbac.authorization.k8s.io"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// admin -> the real, built-in "cluster-admin" ClusterRole — exactly
	// what internal/reconcile/agent.go's own renderAgentProxyManifest
	// creates when spec.access.agent.proxy is true. cluster-admin's own
	// rules are static (a literal */* wildcard, not computed), so it's
	// meaningfully present even in envtest's own minimal environment.
	_, err = adminClientset.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hyve-agent-proxy-admin"},
		Subjects:   []rbacv1.Subject{{Kind: "Group", Name: agentpki.AgentProxyAdminGroup, APIGroup: "rbac.authorization.k8s.io"}},
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin", APIGroup: "rbac.authorization.k8s.io"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// read-only -> a synthetic stand-in for the real built-in "view"
	// ClusterRole, not "view" itself — confirmed live: envtest runs only
	// kube-apiserver + etcd, no kube-controller-manager, and "view"'s
	// rules are populated entirely by that controller's
	// ClusterRoleAggregation reconciliation (aggregated from other
	// ClusterRoles carrying the "rbac.authorization.k8s.io/aggregate-to-view"
	// label); with no controller ever running, the real "view" object
	// exists but its .rules stay permanently empty, making every request
	// through it 403 regardless of what's actually being tested. That
	// internal/reconcile/agent.go's own rendered manifest names "view" by
	// name (not this synthetic role) is already covered separately, by
	// internal/reconcile/agent_test.go's own fake-kubectl assertion on
	// the literal rendered YAML — this test's job is the impersonation +
	// binding *mechanism* around whatever role ends up referenced, which
	// this equivalent-rules substitute exercises identically to the real
	// "view" would under a full control plane.
	_, err = adminClientset.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hyve-view-equivalent"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"pods", "services", "configmaps"},
			Verbs:     []string{"get", "list", "watch"},
		}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = adminClientset.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hyve-agent-proxy-readonly"},
		Subjects:   []rbacv1.Subject{{Kind: "Group", Name: agentpki.AgentProxyReadOnlyGroup, APIGroup: "rbac.authorization.k8s.io"}},
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "test-hyve-view-equivalent", APIGroup: "rbac.authorization.k8s.io"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	expSeconds := int64((10 * time.Minute).Seconds())
	tr, err := adminClientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, serviceAccountName, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expSeconds},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// agentClientFor builds a clientset authenticating as the agent's own
	// restricted ServiceAccount token, impersonating (group, "" for none)
	// exactly the way buildAgentReverseProxy's Director sets
	// Impersonate-User/Impersonate-Group — rest.Config.Impersonate is
	// client-go's own supported mechanism for the identical headers.
	agentClientFor := func(t *testing.T, group string) *kubernetes.Clientset {
		t.Helper()
		// Only CAData carries over from adminCfg — deliberately not the
		// whole TLSClientConfig struct, which also holds the admin
		// user's own CertData/KeyData (envtest's default admin
		// authenticates via client cert, in the system:masters group —
		// see plane.go's AddUser call). Copying those too would let a
		// present client certificate win authentication at the TLS
		// handshake layer itself, before the API server ever looks at
		// this request's Bearer token — confirmed live: the very first
		// version of this test did exactly that and the "unimpersonated"
		// case unexpectedly passed as cluster-admin.
		cfg := &rest.Config{
			Host:            adminCfg.Host,
			TLSClientConfig: rest.TLSClientConfig{CAData: adminCfg.TLSClientConfig.CAData},
			BearerToken:     tr.Status.Token,
		}
		if group != "" {
			cfg.Impersonate = rest.ImpersonationConfig{UserName: "hyve:test-caller", Groups: []string{group}}
		}
		cs, err := kubernetes.NewForConfig(cfg)
		require.NoError(t, err)
		return cs
	}

	t.Run("admin group can do cluster-admin things (create a namespace)", func(t *testing.T) {
		cs := agentClientFor(t, agentpki.AgentProxyAdminGroup)
		_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "proxy-rbac-test-admin"},
		}, metav1.CreateOptions{})
		assert.NoError(t, err, "hyve:admin group, bound to cluster-admin, must be able to create a namespace")
	})

	t.Run("read-only group can read but not write", func(t *testing.T) {
		cs := agentClientFor(t, agentpki.AgentProxyReadOnlyGroup)

		_, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		assert.NoError(t, err, "hyve:read-only group, bound to view, must be able to list pods")

		_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "proxy-rbac-test-readonly"},
		}, metav1.CreateOptions{})
		require.Error(t, err, "hyve:read-only group, bound to view, must NOT be able to create a namespace")
		assert.True(t, apierrors.IsForbidden(err), "expected a Forbidden error, got: %v", err)
	})

	t.Run("the agent's own identity, unimpersonated, can do nothing but impersonate", func(t *testing.T) {
		cs := agentClientFor(t, "") // no Impersonate config at all — acting directly as the agent SA
		_, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		require.Error(t, err, "the agent's own ServiceAccount (impersonate verb only) must not be able to list pods directly")
		assert.True(t, apierrors.IsForbidden(err), "expected a Forbidden error, got: %v", err)
	})
}
