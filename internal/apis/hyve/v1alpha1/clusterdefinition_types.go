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

	// Access controls how internal/api's AccessProvider mints a kubeconfig
	// for this cluster via GET /api/kubeconfig — see
	// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5. Left unset, this
	// cluster uses the "module-auth" default: no stored credential needed.
	Access AccessSpec `json:"access,omitempty"`
}

// AccessMethod values for AccessSpec.Method.
const (
	// AccessMethodModuleAuth runs the cluster's own driver module's
	// auth.yaml live on every /api/kubeconfig request — the default when
	// Method is unset. No stored Secret, no tunnel technology; works for
	// any cloud-managed cluster reachable via its provider's own API.
	AccessMethodModuleAuth = "module-auth"

	// AccessMethodTunnel reads a pre-minted kubeconfig from a stored
	// Secret instead of a live fetch — for clusters with no cloud-native
	// reachable endpoint (self-hosted/on-prem/home-NAT'd). See TunnelSpec.
	AccessMethodTunnel = "tunnel"
)

// TunnelProvider values for TunnelSpec.Provider.
const (
	TunnelProviderRancher  = "rancher"
	TunnelProviderTeleport = "teleport"
)

// AccessSpec is only meaningful when Method is AccessMethodTunnel.
type AccessSpec struct {
	Method string      `json:"method,omitempty"`
	Tunnel *TunnelSpec `json:"tunnel,omitempty"`
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
