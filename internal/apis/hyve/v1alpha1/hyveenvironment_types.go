package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HyveEnvironmentSpec is deliberately minimal — see HYVE-MULTI-TENANCY-PLAN.md's
// "New object: HyveEnvironment" section.
type HyveEnvironmentSpec struct {
	// Namespace this environment maps to. Usually equal to the object's
	// own name, kept as a separate field only because org-name and
	// namespace-name aren't guaranteed to always be the same value (see
	// cmd/shared.ResolveOrgToNamespace's own doc comment).
	Namespace string `json:"namespace"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=henv
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.spec.namespace`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HyveEnvironment is the Schema for the hyveenvironments API — one object
// per tenant, living centrally in the install's control-plane namespace
// (hyve-system by convention, alongside HyveConfig), not namespaced-per-
// tenant itself (which would recreate the original cross-tenant-listing
// problem HyveAccessBinding already had before it was made namespaced —
// see HYVE-MULTI-TENANCY-PLAN.md's Part B). Created by POST /environments
// (internal/api/environments.go) as the authoritative answer to "what
// tenants exist" — deliberately not derived from which namespaces happen
// to have a ClusterDefinition, since a freshly-created tenant with no
// cluster yet would otherwise be invisible to a full `migrate cluster
// --namespace hyve-system` run. No status subresource: metadata.creationTimestamp
// already answers "when was this created," and there's nothing else to
// report yet.
type HyveEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HyveEnvironmentSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// HyveEnvironmentList contains a list of HyveEnvironment.
type HyveEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyveEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyveEnvironment{}, &HyveEnvironmentList{})
}
