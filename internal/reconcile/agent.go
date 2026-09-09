package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"text/template"
	"time"

	"github.com/cbridges1/hyve/internal/types"
)

// AgentTokenIssuer mints a single-use hyve-agent bootstrap token scoped to
// one (targetNamespace, clusterName) — see internal/agentpki.
// GenerateBootstrapToken, which internal/agentpki.TokenIssuer (cmd/
// controller/run.go's own implementation) wraps directly. Declared here,
// not in internal/agentpki, so this package's only dependency on the
// concrete client-go-backed implementation is through this interface —
// same "interface lives with the consumer, concrete impl lives with
// whoever owns the real dependency" shape as workflow.StepRunner/
// module.JobRunner already establish for this same Reconciler. nil (the
// CLI's own default) disables hyve-agent installation entirely: local/
// file mode has no control-plane API process for an agent to dial into in
// the first place, so spec.access.agent.enabled simply has no effect
// there, logged once as a warning rather than failing the reconcile.
type AgentTokenIssuer interface {
	IssueBootstrapToken(ctx context.Context, targetNamespace, clusterName string) (string, error)
}

// agentNamespace is the fixed namespace hyve-agent's own manifests are
// installed into on every managed cluster — deliberately not configurable
// in this first pass (keeps object-identity bookkeeping simple; every
// object this reconcile step ever creates or deletes has one fixed,
// well-known name+namespace, regardless of which cluster it's installed
// on). Not deleted on uninstall (see removeAgentManifests) since other
// things may reasonably come to live in it later.
const agentNamespace = "hyve-system"

// Fixed object names hyve-agent's manifests use on every managed cluster.
const (
	agentServiceAccountName       = "hyve-agent"
	agentImpersonatorRoleName     = "hyve-agent-impersonator"
	agentSecretRoleName           = "hyve-agent"
	agentDeploymentName           = "hyve-agent"
	agentProxyAdminBindingName    = "hyve-agent-proxy-admin"
	agentProxyReadOnlyBindingName = "hyve-agent-proxy-readonly"
)

// agentProxyAdminGroup/agentProxyReadOnlyGroup are the Impersonate-Group
// values milestone 5's proxy path must send for hyve's admin/read-only
// roles, respectively — fixed here because this reconcile step is what
// actually provisions the ClusterRoleBindings that make those two exact
// group names meaningful on the target cluster (see
// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Proxy authorization model":
// "the exact same two RoleBindings... admin->cluster-admin, read-only->view").
// Milestone 5 must use these two literal strings, not invent its own.
const (
	agentProxyAdminGroup    = "hyve:admin"
	agentProxyReadOnlyGroup = "hyve:read-only"
)

// defaultAgentImage is hyve-agent's image when HyveConfig.spec.
// defaultAgentImage is unset — see that field's own doc comment
// (internal/apis/hyve/v1alpha1/hyveconfig_types.go) for why a built-in
// default exists at all. Placeholder: this repo has no release pipeline
// publishing a real, versioned hyve-agent image yet — replace this the
// moment one exists, rather than leaving it silently wrong only in a
// comment nobody would find.
const defaultAgentImage = "ghcr.io/cbridges1/hyve-agent:dev"

// resolveAgentImage applies the same two-tier resolution order
// moduleImage/DefaultModuleImage already use elsewhere on this Reconciler:
// r.DefaultAgentImage (from HyveConfig.spec.defaultAgentImage) if set,
// else the built-in placeholder above.
func (r *Reconciler) resolveAgentImage() string {
	if r.DefaultAgentImage != "" {
		return r.DefaultAgentImage
	}
	return defaultAgentImage
}

// agentCoreObjects/agentProxyObjects are removeAgentManifests' own fixed
// deletion lists — safe to keep as constants rather than deriving them
// from a rendered manifest (parseManifestObjects, as resources.go uses for
// arbitrary user-supplied manifests) since every object hyve-agent ever
// creates has one fixed, well-known identity. agentNamespace itself is
// deliberately excluded — see that constant's own doc comment.
func agentCoreObjects() []types.AppliedObject {
	return []types.AppliedObject{
		{APIVersion: "v1", Kind: "ServiceAccount", Namespace: agentNamespace, Name: agentServiceAccountName},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: agentImpersonatorRoleName},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: agentImpersonatorRoleName},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role", Namespace: agentNamespace, Name: agentSecretRoleName},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding", Namespace: agentNamespace, Name: agentSecretRoleName},
		{APIVersion: "apps/v1", Kind: "Deployment", Namespace: agentNamespace, Name: agentDeploymentName},
	}
}

func agentProxyObjects() []types.AppliedObject {
	return []types.AppliedObject{
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: agentProxyAdminBindingName},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: agentProxyReadOnlyBindingName},
	}
}

// agentConfigHash detects drift in exactly the inputs that change what
// gets applied — see types.AppliedAgent.ConfigHash's own doc comment for
// why Enabled and the bootstrap token are deliberately excluded.
// controlPlaneURL/tunnelAddress are included despite being controller-wide
// (not per-cluster) settings: they're embedded directly in every agent's
// rendered Deployment env, so a real change to either (e.g. hyve-api
// migrating to a new address) must re-apply every existing installation,
// not just new ones — confirmed live: leaving them out of the hash meant
// an already-applied agent kept its stale, now-wrong HYVE_CONTROL_PLANE_URL
// forever, since the "up to date, skip" check never noticed anything
// changed.
func agentConfigHash(proxy bool, image, controlPlaneURL, tunnelAddress string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("proxy=%v;image=%s;controlPlaneURL=%s;tunnelAddress=%s", proxy, image, controlPlaneURL, tunnelAddress)))
	return hex.EncodeToString(sum[:])
}

// reconcileAgent is milestone 4's own reconcile step: installs, updates,
// or removes hyve-agent on cluster's target Kubernetes cluster according
// to cluster.Spec.Agent — a first-class part of every ACTIVE-and-not-
// deleting reconcile pass (see reconcileCluster's own call site), never a
// Template afterCreate hook, per docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's
// "Per-cluster toggle" resolution. env must already carry KUBECONFIG for
// cluster (reconcileCluster's own auth step, run unconditionally ahead of
// this call) — kubectlApply/kubectlDeleteObjects below run against
// whatever that KUBECONFIG points at, exactly like reconcileResources.
func (r *Reconciler) reconcileAgent(ctx context.Context, cluster *types.ClusterDefinition, env []string) error {
	name := cluster.Metadata.Name
	spec := cluster.Spec.Agent
	repoRoot := r.stateMgr.LocalPath()

	if !spec.Enabled {
		if spec.Proxy {
			log.Printf("[%s] Warning: spec.access.agent.proxy is true but enabled is false — proxy requires enabled, ignoring", name)
		}
		if cluster.Spec.AppliedAgent == nil {
			return nil // never installed, nothing to do
		}
		log.Printf("[%s] hyve-agent: enabled flipped to false — removing", name)
		if err := removeAgentManifests(ctx, repoRoot, env, true); err != nil {
			return fmt.Errorf("remove hyve-agent: %w", err)
		}
		cluster.Spec.AppliedAgent = nil
		return r.stateMgr.SaveClusterDefinition(cluster)
	}

	if r.AgentTokenIssuer == nil || r.AgentControlPlaneURL == "" || r.AgentTunnelAddress == "" {
		log.Printf("[%s] Warning: spec.access.agent.enabled is true but hyve-agent installation isn't configured on this controller (missing token issuer / control-plane URL / tunnel address) — skipping", name)
		return nil
	}

	image := r.resolveAgentImage()
	configHash := agentConfigHash(spec.Proxy, image, r.AgentControlPlaneURL, r.AgentTunnelAddress)
	if cluster.Spec.AppliedAgent != nil && cluster.Spec.AppliedAgent.ConfigHash == configHash {
		log.Printf("[%s] hyve-agent: up to date", name)
		return nil
	}

	log.Printf("[%s] hyve-agent: applying (proxy=%v image=%s)", name, spec.Proxy, image)

	// A fresh bootstrap token is only ever minted here, on a real config
	// transition (first install, or a later proxy/image change) — never
	// on an unchanged reconcile cycle (the up-to-date check above already
	// returned). An already-installed agent still has its own persisted
	// identity Secret on the target cluster from its original bootstrap
	// (see internal/agent/bootstrap.go's LoadOrBootstrapIdentity, which
	// checks that Secret first and only ever falls back to a bootstrap
	// token when it's genuinely missing) — so a token minted here on a
	// drift-triggered re-apply typically goes unused, which is harmless:
	// single-use and short-lived (agentpki.BootstrapTokenTTL), exactly the
	// same as any other bootstrap token nothing ever redeemed.
	token, err := r.AgentTokenIssuer.IssueBootstrapToken(ctx, r.AgentControlPlaneNamespace, name)
	if err != nil {
		return fmt.Errorf("mint hyve-agent bootstrap token: %w", err)
	}

	coreManifest, err := renderAgentCoreManifest(agentManifestParams{
		Image:           image,
		ControlPlaneURL: r.AgentControlPlaneURL,
		TunnelAddress:   r.AgentTunnelAddress,
		ClusterName:     name,
		BootstrapToken:  token,
	})
	if err != nil {
		return fmt.Errorf("render hyve-agent manifest: %w", err)
	}
	if err := kubectlApply(ctx, repoRoot, env, coreManifest, ""); err != nil {
		return fmt.Errorf("apply hyve-agent manifest: %w", err)
	}

	if spec.Proxy {
		proxyManifest, err := renderAgentProxyManifest()
		if err != nil {
			return fmt.Errorf("render hyve-agent proxy manifest: %w", err)
		}
		if err := kubectlApply(ctx, repoRoot, env, proxyManifest, ""); err != nil {
			return fmt.Errorf("apply hyve-agent proxy manifest: %w", err)
		}
	} else {
		// Config changed and proxy is now off — a plain `kubectl apply`
		// of coreManifest above never removes objects that simply aren't
		// in it, so a true false (as opposed to "was already false")
		// needs an explicit delete. Safe to call unconditionally even
		// when proxy was already off (kubectlDeleteObjects's own
		// --ignore-not-found), so no extra state is needed to tell the
		// two cases apart.
		if err := kubectlDeleteObjects(ctx, repoRoot, env, agentProxyObjects()); err != nil {
			return fmt.Errorf("remove hyve-agent proxy bindings: %w", err)
		}
	}

	cluster.Spec.AppliedAgent = &types.AppliedAgent{ConfigHash: configHash, AppliedAt: time.Now().UTC().Format(time.RFC3339)}
	return r.stateMgr.SaveClusterDefinition(cluster)
}

// removeAgentManifests deletes every object hyve-agent's core install
// owns, and — when includeProxy is true — the two proxy ClusterRoleBindings
// too. Called both from the Enabled: true -> false path (includeProxy:
// true, unconditionally) and, indirectly via the Proxy: true -> false
// branch in reconcileAgent above, through agentProxyObjects() directly
// instead (Enabled stays true there, so the core objects must NOT be
// touched).
func removeAgentManifests(ctx context.Context, repoRoot string, env []string, includeProxy bool) error {
	objects := agentCoreObjects()
	if includeProxy {
		objects = append(objects, agentProxyObjects()...)
	}
	return kubectlDeleteObjects(ctx, repoRoot, env, objects)
}

// agentManifestParams bundles renderAgentCoreManifest's inputs.
type agentManifestParams struct {
	Image           string
	ControlPlaneURL string
	TunnelAddress   string
	ClusterName     string
	BootstrapToken  string
}

// yq YAML-quotes s as a double-quoted scalar — every templated value below
// is user/config-controlled (an image ref, a URL, a cluster name, a
// generated hex token) and gets interpolated directly into a YAML
// document, so this guards against any of them containing a character
// that would otherwise break the surrounding YAML (a colon, a leading
// special character, ...) rather than assuming they're always "obviously
// safe". Go's double-quoted string escaping is a superset of what a YAML
// double-quoted scalar needs for this ASCII-only value space.
func yq(s string) string {
	return strconv.Quote(s)
}

var agentCoreManifestTemplate = template.Must(template.New("agent-core").Funcs(template.FuncMap{"yq": yq}).Parse(`
apiVersion: v1
kind: Namespace
metadata:
  name: ` + agentNamespace + `
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ` + agentServiceAccountName + `
  namespace: ` + agentNamespace + `
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ` + agentImpersonatorRoleName + `
rules:
  - apiGroups: [""]
    resources: ["users", "groups", "serviceaccounts"]
    verbs: ["impersonate"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ` + agentImpersonatorRoleName + `
subjects:
  - kind: ServiceAccount
    name: ` + agentServiceAccountName + `
    namespace: ` + agentNamespace + `
roleRef:
  kind: ClusterRole
  name: ` + agentImpersonatorRoleName + `
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ` + agentSecretRoleName + `
  namespace: ` + agentNamespace + `
rules:
  # get/update scoped to hyve-agent's own persisted-identity Secret by
  # name; create can't be scoped by resourceNames at all (a Kubernetes
  # RBAC limitation — see api-rbac.yaml's own hyve-cli-secrets comment for
  # the same gotcha confirmed live there), hence the separate unscoped
  # rule below.
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["hyve-agent-cert"]
    verbs: ["get", "update"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ` + agentSecretRoleName + `
  namespace: ` + agentNamespace + `
subjects:
  - kind: ServiceAccount
    name: ` + agentServiceAccountName + `
    namespace: ` + agentNamespace + `
roleRef:
  kind: Role
  name: ` + agentSecretRoleName + `
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ` + agentDeploymentName + `
  namespace: ` + agentNamespace + `
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: hyve-agent
  template:
    metadata:
      labels:
        app.kubernetes.io/name: hyve-agent
    spec:
      serviceAccountName: ` + agentServiceAccountName + `
      containers:
        - name: agent
          image: {{yq .Image}}
          env:
            - name: HYVE_CONTROL_PLANE_URL
              value: {{yq .ControlPlaneURL}}
            - name: HYVE_TUNNEL_ADDRESS
              value: {{yq .TunnelAddress}}
            - name: HYVE_NAMESPACE
              value: ` + agentNamespace + `
            - name: HYVE_CLUSTER_NAME
              value: {{yq .ClusterName}}
            - name: HYVE_BOOTSTRAP_TOKEN
              value: {{yq .BootstrapToken}}
`))

func renderAgentCoreManifest(p agentManifestParams) ([]byte, error) {
	var buf bytes.Buffer
	if err := agentCoreManifestTemplate.Execute(&buf, p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderAgentProxyManifest needs no per-cluster values at all — both
// ClusterRoleBindings' subjects are the two fixed Impersonate-Group
// values milestone 5 will send, not anything specific to this cluster.
func renderAgentProxyManifest() ([]byte, error) {
	return []byte(fmt.Sprintf(`
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s
subjects:
  - kind: Group
    name: %s
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s
subjects:
  - kind: Group
    name: %s
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: view
  apiGroup: rbac.authorization.k8s.io
`, agentProxyAdminBindingName, agentProxyAdminGroup, agentProxyReadOnlyBindingName, agentProxyReadOnlyGroup)), nil
}
