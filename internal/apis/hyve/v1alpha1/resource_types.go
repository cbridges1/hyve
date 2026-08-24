package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceSpec holds one resource's raw manifest content, possibly
// multi-document. Mirrors internal/resourceref's own ResolvedResource.Data
// shape — deliberately raw-only, no templating beyond what the reconciler
// already applies to any manifest resource, matching WorkflowSpec's own
// "no templating beyond ${VAR} expansion" stance rather than growing a
// superset.
type ResourceSpec struct {
	Manifest string `json:"manifest"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=res
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Resource is the Schema for the resources API — the cluster-native
// alternative to a ClusterDefinition.spec.resources[] entry's local-path or
// remote-git Source, referenced by ResourceRef.Name the same way a
// Workflow CR is referenced by WorkflowRef.Name (see
// internal/resourceref.ChainSource). No status: purely declarative
// content, read on demand at reconcile time, never itself reconciled.
type Resource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ResourceSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// ResourceList contains a list of Resource.
type ResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Resource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Resource{}, &ResourceList{})
}
