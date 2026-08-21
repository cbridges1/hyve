package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/module"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AccessProvider mints a kubeconfig for a ClusterDefinition — see
// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5. ctx carries whatever
// per-request context the caller's requireRole middleware attached (e.g.
// ServiceAccountRefFromContext for PrimaryClusterProvider) — every
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

// PrimaryClusterProvider mints a scoped ServiceAccount token via
// TokenRequest and assembles a kubeconfig whose server: points back at
// this API's own /proxy path (see proxy.go) — Phase 6.5/6.6. Hardcoded to
// the one cluster this API runs on; not configurable per-request.
type PrimaryClusterProvider struct {
	Clientset kubernetes.Interface

	// CA is this API pod's own in-cluster CA — normally read once at
	// startup from /var/run/secrets/kubernetes.io/serviceaccount/ca.crt.
	CA []byte

	// PublicBaseURL is this API's own public address, e.g.
	// "https://hyve-api.example.com" — clusters[].cluster.server in the
	// returned kubeconfig is PublicBaseURL + "/proxy".
	PublicBaseURL string

	// TokenTTL defaults to 24h when zero — no refresh endpoint yet (v1); a
	// caller re-requests a fresh kubeconfig once this expires.
	TokenTTL time.Duration
}

// Kubeconfig ignores cd (the primary cluster isn't represented by a driver
// module) and instead mints a token for the caller's own resolved
// ServiceAccountRef — see ServiceAccountRefFromContext, set by
// requireRole from the caller's matched HyveAccessBinding.
func (p *PrimaryClusterProvider) Kubeconfig(ctx context.Context, _ *hyvev1alpha1.ClusterDefinition) ([]byte, error) {
	saRef, ok := ServiceAccountRefFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no service account resolved for this caller")
	}

	ttl := p.TokenTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	expSeconds := int64(ttl.Seconds())

	tr, err := p.Clientset.CoreV1().ServiceAccounts(saRef.Namespace).CreateToken(ctx, saRef.Name, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expSeconds},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("mint token for %s/%s: %w", saRef.Namespace, saRef.Name, err)
	}

	return buildKubeconfig(strings.TrimRight(p.PublicBaseURL, "/")+"/proxy", p.CA, tr.Status.Token)
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
		return nil, fmt.Errorf("auth op for cluster %q produced no kubeconfig — its auth.yaml's script must print HYVE_KUBECONFIG_B64=<base64 kubeconfig content> to stdout", cd.Name)
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
