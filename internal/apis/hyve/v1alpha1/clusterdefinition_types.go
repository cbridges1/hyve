package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterDefinitionFinalizer is set on every ClusterDefinition by the
// controller so a `kubectl delete` first goes through the module's own
// delete op (and onDelete/afterDelete workflows) before the object is
// actually removed from etcd — mirrors file mode's spec.delete:true flow,
// where the reconciler runs the same steps before removing the YAML.
const ClusterDefinitionFinalizer = "hyve.io/cluster-cleanup"

// ClusterDefinitionSpec is the desired state — the CRD equivalent of a
// human-authored clusters/<name>.yaml in file mode. Region lives here
// (rather than on ObjectMeta, which is reserved for Kubernetes' own
// bookkeeping) as the CRD-mode replacement for
// internal/types.ClusterMetadata.Region; the object's own metadata.name
// replaces internal/types.ClusterMetadata.Name.
type ClusterDefinitionSpec struct {
	Region string `json:"region,omitempty"`

	// Driver identifies the module that manages this cluster.
	Driver DriverRef `json:"driver,omitempty"`

	// Runner configures the image cluster mode's Job dispatch uses to run
	// this cluster's module create/status/delete/auth operations — see
	// RunnerSpec's own doc comment for why this lives here (or on the
	// Template a cluster was rendered from — see
	// RenderClusterDefinitionSpec) rather than on the module itself.
	// Ignored entirely in local/CLI mode.
	Runner RunnerSpec `json:"runner,omitempty"`

	// Params are arbitrary key/value pairs passed to the driver as
	// HYVE_PARAM_<KEY> environment variables.
	Params map[string]string `json:"params,omitempty"`

	Workflows WorkflowsSpec `json:"workflows,omitempty"`

	// Resources declares Kubernetes manifests hyve should own, drift-check,
	// and re-apply on every reconcile cycle for an ACTIVE cluster.
	Resources []ResourceRef `json:"resources,omitempty"`

	// Delete marks this cluster for deletion — same semantics as file
	// mode's spec.delete:true. In CRD mode a `kubectl delete
	// clusterdefinition <name>` is the more natural trigger (see
	// ClusterDefinitionFinalizer); this field exists for parity and for
	// automated set-then-apply flows that mirror the file-mode pattern.
	Delete bool `json:"delete,omitempty"`

	// Pause skips reconciliation for this cluster while keeping its
	// definition in the cluster.
	Pause bool `json:"pause,omitempty"`

	// ExpiresAt is an optional RFC 3339 timestamp. When the current time is
	// past this value the reconciler treats the cluster as if delete:true
	// is set.
	ExpiresAt string `json:"expiresAt,omitempty"`

	// DependsOn names other ClusterDefinitions this one depends on — see
	// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's "Optional dependsOn ordering"
	// section. ReconcileOne skips (does not fail) a reconcile cycle for
	// this cluster while any named dependency isn't ACTIVE.
	DependsOn []string `json:"dependsOn,omitempty"`

	// Access controls how a kubeconfig is minted for this cluster — see
	// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5. Left unset (the
	// default), GET /api/kubeconfig doesn't serve this cluster at all: a
	// module's auth.yaml is written assuming it runs with the caller's own
	// local credentials/tools (cloud CLI configs, SSH keys, etc.), so the
	// API instead exposes driver info via
	// GET /api/clusters/<name>/auth-context and the CLI runs the module
	// client-side (see cmd/cluster/auth.go) — the same as local mode,
	// just sourcing the ClusterDefinition from the API instead of git.
	// Set Method: module-auth to override that and run the module
	// server-side instead, inside the API pod, on every /api/kubeconfig
	// request — only appropriate for a module whose auth.yaml is written
	// to run there and itself enforces proper authorization using the
	// caller identity hyve injects (HYVE_CALLER_USERNAME/HYVE_CALLER_ROLE
	// — see moduleEnvForClusterDefinition), since the pod's own ambient
	// credentials mint the result, not the caller's.
	Access AccessSpec `json:"access,omitempty"`
}

// AccessMethod values for AccessSpec.Method.
const (
	// AccessMethodModuleAuth is an explicit opt-in override: runs the
	// cluster's own driver module's auth.yaml live, server-side inside the
	// API pod, on every /api/kubeconfig request. The default (Method
	// unset) instead runs the module client-side — see
	// ClusterDefinitionSpec.Access's doc comment for why, and for what
	// this override assumes the module's auth.yaml itself takes care of.
	AccessMethodModuleAuth = "module-auth"

	// AccessMethodTunnel reads a pre-minted kubeconfig from a stored
	// Secret instead of a live fetch — for clusters with no cloud-native
	// reachable endpoint (self-hosted/on-prem/home-NAT'd). See TunnelSpec.
	AccessMethodTunnel = "tunnel"

	// AccessMethodPrimary marks a ClusterDefinition as representing the
	// cluster hyve-controller/hyve-api themselves run on (the "host"
	// cluster — see HYVE-MULTI-TENANCY-PLAN.md's "Host cluster access"
	// section). No driver/create/delete lifecycle applies — it already
	// exists by definition. Routes to PrimaryClusterProvider, which mints
	// a cluster-admin-bound token via TokenRequest and points server: at
	// this API's own /proxy path rather than any external address (works
	// identically whether the host cluster has a public IP, a LAN-only
	// address, or sits behind NAT with no inbound path at all — the proxy
	// runs from inside the cluster, so it never needs one). Gated to
	// RoleSuperadmin only; every other role is refused outright, since
	// this credential reaches the cluster every tenant's workload actually
	// runs on. Should live in the install's control-plane namespace
	// (hyve-system by convention), never a tenant namespace.
	AccessMethodPrimary = "primary"
)

// TunnelProvider values for TunnelSpec.Provider.
const (
	TunnelProviderRancher  = "rancher"
	TunnelProviderTeleport = "teleport"
)

// AccessSpec.
type AccessSpec struct {
	Method string      `json:"method,omitempty"`
	Tunnel *TunnelSpec `json:"tunnel,omitempty"` // only meaningful when Method is AccessMethodTunnel

	// AccessMethodRef names an AccessMethod object (see accessmethod_types.go)
	// providing a client-side, per-user access path independent of Method/
	// Tunnel above — set by `hyve cluster auth` itself, resolved entirely
	// client-side (or via a small read-only API lookup in cluster mode),
	// never through GET /api/kubeconfig or ModuleAuthProvider/TunnelProvider
	// at all. Orthogonal to Method: a cluster can leave Method unset (the
	// client-side module-auth default) and still set AccessMethodRef, since
	// they're two independent ways a caller might obtain a kubeconfig for
	// the same cluster. See HYVE-ACCESS-METHOD-DESIGN.md for the full
	// design and why this is additive rather than replacing Tunnel/
	// TunnelProvider (that path stays for the case Rancher/Teleport is only
	// reachable from the API pod, not the caller's own machine — deferred,
	// not removed).
	AccessMethodRef string `json:"accessMethodRef,omitempty"`

	// AccessMethodClusterID is this cluster's own identifier within the
	// referenced AccessMethod's provider (e.g. Rancher's internal cluster
	// ID) — required when AccessMethodRef is set. See AccessMethodRef's own
	// doc comment and types.ClusterSpec.AccessMethodClusterID (the local-
	// mode equivalent).
	AccessMethodClusterID string `json:"accessMethodClusterID,omitempty"`
}

// TunnelSpec names which appendix pattern workflows/mint-tunnel-access.yaml
// dispatches to for this cluster.
type TunnelSpec struct {
	Provider string `json:"provider,omitempty"`
}

// ClusterDefinitionStatus is reconciler-owned observed state — the CRD
// equivalent of file mode's cluster-state/<name>.state.yaml sidecar, plus
// the two fields standard for any controller-runtime-managed resource
// (Conditions, ObservedGeneration). Written only via the status
// subresource: the reconciler never modifies .spec, treating it as
// external, user-owned input exactly like the Kubernetes convention for
// any other controller.
type ClusterDefinitionStatus struct {
	// DriverOutputs captures HYVE_* outputs produced by the driver's
	// create/status operations.
	DriverOutputs map[string]string `json:"driverOutputs,omitempty"`

	// AppliedResources is reconciler-owned state tracking what Resources
	// entries are currently applied, keyed by ResourceRef.Name.
	AppliedResources map[string]*AppliedResource `json:"appliedResources,omitempty"`

	// Conditions follow the standard Kubernetes Condition[] convention.
	// hyve sets "Ready" (True once the module reports ACTIVE),
	// "Reconciling" (True while a reconcile is in progress for this
	// generation), and "Error" (True with Message set when the last
	// reconcile attempt failed).
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the .metadata.generation last processed by the
	// controller — the standard controller-runtime pattern for a caller to
	// tell whether status reflects the most recent spec change.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Access records which access method is currently active for this
	// cluster — useful for GET /api/clusters to give an honest signal
	// about how a caller's kubeconfig request will be served.
	Access AccessStatus `json:"access,omitempty"`

	// LastCreateOutput/LastDeleteOutput hold the most recent create/delete
	// operation's full captured stdout (module.OperationResult.RawOutput,
	// capped at 256KiB) — the only place this output survives at all:
	// k8sjob.Run always deletes its dispatched Job immediately after
	// fetching its logs, so without this, a create/delete script's actual
	// output (as opposed to just its parsed HYVE_KEY=value outputs or a bare
	// exit code) was never visible anywhere, in the CLI or the UI, once the
	// Job that ran it was gone. Each is overwritten only when that
	// operation actually runs again — see internal/controller/reconciler.go's
	// hooks wiring, which only touches whichever of the two fields fired
	// this cycle.
	LastCreateOutput string `json:"lastCreateOutput,omitempty"`
	LastDeleteOutput string `json:"lastDeleteOutput,omitempty"`
}

// AccessStatus records which access method is currently active for a
// cluster and, for tunnel-mode clusters, when the credential was last
// minted — see workflows/mint-tunnel-access.yaml.
type AccessStatus struct {
	Method string `json:"method,omitempty"`
	// LastMinted is an RFC 3339 timestamp, set only for tunnel-mode
	// clusters (module-auth mints fresh on every request, so there's
	// nothing meaningful to record here for it).
	LastMinted string `json:"lastMinted,omitempty"`
}

// Condition type strings used in ClusterDefinitionStatus.Conditions.
const (
	ConditionTypeReady       = "Ready"
	ConditionTypeReconciling = "Reconciling"
	ConditionTypeError       = "Error"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cd
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver.source`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterDefinition is the Schema for the clusterdefinitions API — the
// controller-mode source of truth for one cluster, equivalent to a
// clusters/<name>.yaml + cluster-state/<name>.state.yaml pair in file mode.
type ClusterDefinition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterDefinitionSpec   `json:"spec,omitempty"`
	Status ClusterDefinitionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterDefinitionList contains a list of ClusterDefinition.
type ClusterDefinitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterDefinition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterDefinition{}, &ClusterDefinitionList{})
}
