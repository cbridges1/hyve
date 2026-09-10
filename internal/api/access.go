package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/hostauth"
	"github.com/cbridges1/hyve/internal/module"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AccessProvider mints a kubeconfig for a ClusterDefinition — see
// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5. ctx carries whatever
// per-request context the caller's requireRole middleware attached — every
// implementation shares this one signature so handleKubeconfig can dispatch
// without a type switch on the provider itself.
type AccessProvider interface {
	Kubeconfig(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition) ([]byte, error)
}

// buildKubeconfig assembles a minimal, valid kubeconfig YAML via
// client-go's own clientcmd types rather than hand-formatting YAML —
// avoids subtle serialization bugs (quoting, base64 wrapping) a
// string-templated kubeconfig would risk.
func buildKubeconfig(server string, caData []byte, token string) ([]byte, error) {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["hyve"] = &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: caData}
	cfg.AuthInfos["hyve"] = &clientcmdapi.AuthInfo{Token: token}
	cfg.Contexts["hyve"] = &clientcmdapi.Context{Cluster: "hyve", AuthInfo: "hyve"}
	cfg.CurrentContext = "hyve"
	return clientcmd.Write(*cfg)
}

// AgentProvider mints a kubeconfig whose server: points at this API's own
// /api/agent-proxy/<name> path (see agent_proxy.go), carrying the
// caller's own hyve session token — /proxy's own kubeconfigs (see
// proxy.go) instead carry a real Kubernetes ServiceAccount token, since
// /proxy forwards it straight through, unmodified, to the one real
// apiserver it already trusts (the cluster it runs on itself).
// /api/agent-proxy has no such standing trust in any one target cluster
// (there could be many, each a different real cluster this pod has never
// talked to directly) — so the credential embedded here is hyve's own
// session token instead: agent_proxy.go's own requireAuth+requireRole
// re-verifies it exactly like any other /api/* call, then that handler
// itself supplies the real Kubernetes credential (the connected agent's
// own heartbeat-reported ServiceAccount token) plus Impersonate-User/
// -Group headers derived from the caller's resolved role.
type AgentProvider struct {
	// PublicBaseURL is this API's own public address.
	PublicBaseURL string

	// PublicCA, if set, is embedded as this kubeconfig's
	// certificate-authority-data — the CA that signed whatever terminates
	// TLS in front of PublicBaseURL (an Ingress, a LoadBalancer, wherever).
	// Left nil (the default), the minted kubeconfig carries no CA data at
	// all and kubectl falls back to the OS/system trust store — fine for a
	// publicly-trusted certificate (a real ACME/Let's Encrypt cert), but
	// it means a self-signed or locally-generated cert fails validation
	// for every caller except a machine that separately imported that CA
	// into its own system trust store (e.g. via mkcert). Setting PublicCA
	// avoids needing that per-machine step at all: any caller's kubectl
	// trusts this specific cluster entry via the embedded CA, the same
	// mechanism HostProvider already uses for its own CA, and the same
	// mechanism every real kubeconfig for every real cluster already
	// relies on — there is nothing agent-proxy-specific this needs beyond
	// what kubeconfig already supports. See
	// docs/HYVE-CLOUD-EXPOSURE-PROPOSAL.md.
	PublicCA []byte
}

func (p *AgentProvider) Kubeconfig(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition) ([]byte, error) {
	token, ok := tokenFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no session token available for this caller")
	}
	server := strings.TrimRight(p.PublicBaseURL, "/") + "/api/agent-proxy/" + cd.Name
	return buildKubeconfig(server, p.PublicCA, token)
}

// HostProvider mints a kubeconfig for the ClusterDefinition marked
// access.method: primary (hyvev1alpha1.AccessMethodPrimary) — the cluster
// hyve-controller/hyve-api themselves run on. Unlike every other
// AccessProvider, this needs no driver module at all: an earlier design
// for this required an admin to hand-write a driver module's auth.yaml
// just to get a kubeconfig for the cluster hyve is already running on,
// which turned out to be pure friction with no real benefit — reverted as
// an acknowledged oversight (see docs/HYVE-AGENT-MIGRATION-GUIDE.md's
// "Host cluster access" section for the full history). Mints a token
// against the dedicated HostServiceAccountRef (e.g. hyve-host-admin,
// bound to the built-in cluster-admin ClusterRole — see
// deploy/helm/hyve/templates/api-access-roles.yaml) via
// internal/hostauth.MintKubeconfig, gated to RoleSuperadmin only, since
// this credential reaches the cluster every tenant's workload actually
// runs on.
type HostProvider struct {
	Clientset kubernetes.Interface

	// CA is this API pod's own in-cluster CA — normally read once at
	// startup from /var/run/secrets/kubernetes.io/serviceaccount/ca.crt.
	CA []byte

	// PublicBaseURL is this API's own public address, e.g.
	// "https://hyve-api.example.com" — clusters[].cluster.server in the
	// returned kubeconfig is PublicBaseURL + "/proxy".
	PublicBaseURL string

	// HostServiceAccountRef is the dedicated, standing ServiceAccount this
	// mints a token against — deliberately separate from any
	// tenant-scoped role's own ServiceAccount, so host-cluster privilege
	// is its own narrowly-granted, auditable binding, never incidentally
	// inherited from a tenant role. Left zero-value, Kubeconfig 500s with
	// a clear message rather than falling back to anything.
	HostServiceAccountRef hyvev1alpha1.ServiceAccountRef

	// TokenTTL defaults to 1h when zero — deliberately short, given what
	// this kubeconfig grants; no refresh endpoint yet, a caller
	// re-requests a fresh one once this expires.
	TokenTTL time.Duration
}

func (p *HostProvider) Kubeconfig(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition) ([]byte, error) {
	role, _ := RoleFromContext(ctx)
	if role != hyvev1alpha1.RoleSuperadmin {
		name := "<unknown>"
		if cd != nil {
			name = cd.Name
		}
		return nil, fmt.Errorf("cluster %q is the host cluster (access.method: primary) — only a superadmin may access it", name)
	}
	if p.HostServiceAccountRef.Name == "" {
		return nil, fmt.Errorf("no host ServiceAccount configured for the host access path")
	}
	ttl := p.TokenTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	server := strings.TrimRight(p.PublicBaseURL, "/") + "/proxy"
	return hostauth.MintKubeconfig(ctx, p.Clientset, p.HostServiceAccountRef.Namespace, p.HostServiceAccountRef.Name, server, p.CA, ttl)
}

// ModuleAuthProvider backs the explicit AccessMethodModuleAuth override
// (see ClusterDefinitionSpec.Access's doc comment — the default instead
// runs the same auth.yaml client-side, via GET /api/clusters/<name>/auth-context)
// — runs the target ClusterDefinition's driver module's existing auth.yaml
// live, inside this API pod, and returns the resulting kubeconfig as-is. No
// cloud-provider SDK code lives here or anywhere in internal/api — all
// cloud-specific logic stays inside the module's own auth.yaml, per "no
// cloud SDKs embedded in hyve". Because this runs with the pod's own
// ambient credentials rather than the caller's, the caller's resolved
// identity is injected as HYVE_CALLER_USERNAME/HYVE_CALLER_ROLE (see
// moduleEnvForClusterDefinition) so the module's auth.yaml can itself
// enforce whatever authorization it needs — this provider does not check
// authorization on the module's behalf.
type ModuleAuthProvider struct {
	// ModulesDir is the same baked-in modules root
	// internal/controller.CRDStateProvider.ModulesDirPath uses.
	ModulesDir string
}

func (p *ModuleAuthProvider) Kubeconfig(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition) ([]byte, error) {
	lf, err := module.LoadLockFile(p.ModulesDir)
	if err != nil {
		return nil, fmt.Errorf("load hyve.lock: %w", err)
	}
	locked := lf.GetLocked(cd.Spec.Driver.Source, cd.Spec.Driver.Version)
	resolved, err := module.Resolve(cd.Spec.Driver.Source, cd.Spec.Driver.Version, locked, p.ModulesDir)
	if err != nil {
		return nil, fmt.Errorf("resolve driver module for cluster %q: %w", cd.Name, err)
	}

	exec := &module.Executor{
		ModuleDir:   resolved.Dir,
		Env:         moduleEnvForClusterDefinition(ctx, cd),
		WorkDir:     p.ModulesDir,
		ClusterName: cd.Name,
	}
	result, err := exec.Execute(ctx, module.OperationAuth)
	if err != nil {
		return nil, fmt.Errorf("run auth op for cluster %q: %w", cd.Name, err)
	}
	kcPath := result.Outputs["KUBECONFIG"]
	if kcPath == "" {
		return nil, fmt.Errorf("auth op for cluster %q produced no kubeconfig — its auth.yaml must set exports: KUBECONFIG", cd.Name)
	}
	data, err := os.ReadFile(kcPath)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig for cluster %q: %w", cd.Name, err)
	}
	return data, nil
}

// moduleEnvForClusterDefinition builds the HYVE_* env module.Executor needs
// — a CRD-flavored equivalent of internal/reconcile's unexported
// buildModuleEnv (that one operates on internal/types.ClusterDefinition,
// which this package has no reason to depend on internal/reconcile just to
// reuse; duplicated deliberately, same precedent as
// internal/apis/hyve/v1alpha1's own duplication of internal/types shapes).
// Also injects the caller's resolved identity (HYVE_CALLER_USERNAME/
// HYVE_CALLER_ROLE, from ctx — see requireAuth/requireRole) since this path
// runs the module server-side, with this pod's own ambient credentials
// rather than the caller's: the module's auth.yaml is the only place left
// that can enforce authorization for what it's about to mint, and it needs
// to know who's actually asking.
func moduleEnvForClusterDefinition(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition) []string {
	env := []string{
		"HYVE_CLUSTER_NAME=" + cd.Name,
		"HYVE_CLUSTER_REGION=" + cd.Spec.Region,
	}
	if username, ok := UsernameFromContext(ctx); ok {
		env = append(env, "HYVE_CALLER_USERNAME="+username)
	}
	if role, ok := RoleFromContext(ctx); ok {
		env = append(env, "HYVE_CALLER_ROLE="+role)
	}
	for k, v := range cd.Spec.Params {
		env = append(env, "HYVE_PARAM_"+strings.ToUpper(k)+"="+v)
	}
	for k, v := range cd.Status.DriverOutputs {
		env = append(env, k+"="+v)
	}
	return env
}

// TunnelProvider reads a pre-minted kubeconfig from a stored Secret
// instead of a live fetch — for clusters with no cloud-native reachable
// endpoint. The Secret is populated by workflows/mint-tunnel-access.yaml
// (see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5b appendix
// patterns), which this pass does not implement — it requires a real
// Rancher or Teleport deployment to build and verify against. This
// provider's read path is independent of that and fully implemented: once
// the Secret exists (however it got there — the workflow, or applied by
// hand), /api/kubeconfig serves it correctly.
type TunnelProvider struct {
	Client    client.Client
	Namespace string // hyve-system by convention
}

func (p *TunnelProvider) Kubeconfig(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition) ([]byte, error) {
	secretName := cd.Name + "-access-kubeconfig"
	var secret corev1.Secret
	if err := p.Client.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: secretName}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("cluster %q is set to access.method: tunnel but %s/%s doesn't exist yet — run workflows/mint-tunnel-access.yaml for it first", cd.Name, p.Namespace, secretName)
		}
		return nil, fmt.Errorf("get tunnel kubeconfig secret: %w", err)
	}
	data, ok := secret.Data["kubeconfig"]
	if !ok || len(data) == 0 {
		return nil, fmt.Errorf("secret %s/%s has no %q key", p.Namespace, secretName, "kubeconfig")
	}
	return data, nil
}
