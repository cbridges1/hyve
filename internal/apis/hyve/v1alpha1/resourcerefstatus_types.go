package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceRefStatusSpec identifies which remote ResourceRef this mirrors.
// Mirrors WorkflowRefStatusSpec exactly — see its doc comment for why
// metadata.name is a derived slug rather than Name directly.
type ResourceRefStatusSpec struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// ResourceRefStatusStatus is reconciler-owned observed state — status
// subresource only. Mirrors WorkflowRefStatusStatus, minus ResolvedVersion:
// resourceref.ResolvedResource has no such field (unlike
// workflowref.ResolvedWorkflowFile), so there's nothing to carry here.
type ResourceRefStatusStatus struct {
	Resolved   bool        `json:"resolved,omitempty"`
	RawVersion string      `json:"rawVersion,omitempty"`
	SHA256     string      `json:"sha256,omitempty"`
	ResolvedAt metav1.Time `json:"resolvedAt,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=resrs
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source`
// +kubebuilder:printcolumn:name="Resolved",type=boolean,JSONPath=`.status.resolved`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ResourceRefStatus mirrors WorkflowRefStatus exactly, one tier below it —
// see that kind's doc comment. A visibility record of a remote
// (git-referenced) ResourceRef the controller has (attempted to) resolve
// automatically; the ClusterDefinition never references this CR. Only ever
// written by internal/controller/reconciler.go's resolveResourceIfNeeded /
// upsertResourceRefStatusCR.
type ResourceRefStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceRefStatusSpec   `json:"spec,omitempty"`
	Status ResourceRefStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ResourceRefStatusList contains a list of ResourceRefStatus.
type ResourceRefStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceRefStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ResourceRefStatus{}, &ResourceRefStatusList{})
}
