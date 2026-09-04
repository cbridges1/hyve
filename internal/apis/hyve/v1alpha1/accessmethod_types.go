package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Provider values for AccessMethodSpec.Provider.
const (
	AccessMethodProviderRancher  = "rancher"
	AccessMethodProviderTeleport = "teleport"
)

// AccessMethodSpec holds connection config for an externally-addressable
// identity/access service (Rancher, Teleport, ...) that mints kubeconfigs
// for clusters that reference it — never a credential itself, only where
// to reach the service. The credential comes from whichever user requests
// access, at request time (see HYVE-ACCESS-METHOD-DESIGN.md), which is
// exactly what makes this object safe to read broadly and safe to commit
// to a GitOps repo. Deliberately not scoped to "tunnel" specifically —
// Provider is a plain string, not a closed set baked into this struct's
// shape, so a future non-tunnel provider (an OIDC-based mechanism, a
// Vault-issued cert, ...) needs no schema change here.
type AccessMethodSpec struct {
	// Provider selects which client-side implementation resolves this
	// AccessMethod — see AccessMethodProviderRancher/AccessMethodProviderTeleport.
	Provider string `json:"provider"`

	// ServerURL is the provider's own address — e.g. a Rancher server's
	// base URL. Meaning is provider-specific; hyve never validates its
	// shape beyond requiring it be set.
	ServerURL string `json:"serverURL"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=am
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="ServerURL",type=string,JSONPath=`.spec.serverURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AccessMethod is the Schema for the accessmethods API — an admin-managed,
// reusable, named resource referenced by name
// (ClusterDefinitionSpec.Access.AccessMethodRef) from any ClusterDefinition,
// decoupled from which module provisioned that cluster. Namespaced, for the
// same reason HyveAccessBinding is: a cluster-scoped object here would let
// one tenant's ClusterDefinition reference (and, depending on RBAC, list)
// another tenant's access-service config. Read-only from this API's own
// perspective — admins create/update these directly via kubectl, the same
// stance HyveConfig's controller.hyveConfig.create: false default already
// takes for a different singleton object; see
// HYVE-ACCESS-METHOD-DESIGN.md for the full design.
type AccessMethod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AccessMethodSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AccessMethodList contains a list of AccessMethod.
type AccessMethodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AccessMethod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AccessMethod{}, &AccessMethodList{})
}
