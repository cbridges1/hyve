package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowRefStatusSpec identifies which remote WorkflowRef this mirrors.
// Name is the declared ref's short name (WorkflowRef.Name) — the
// human-facing display identity; metadata.name is a derived, collision-safe
// slug (see module.CRName), not this, since two different sources can
// legitimately share the same short Name (see workflowref.NameCollision).
type WorkflowRefStatusSpec struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// WorkflowRefStatusStatus is reconciler-owned observed state — written only
// via the status subresource. Mirrors ModuleStatus exactly: a write-only
// record of an auto-resolve outcome, not something any reconcile loop
// watches or acts on.
type WorkflowRefStatusStatus struct {
	// Resolved is true once this ref has been successfully fetched and
	// locked (see internal/workflowref.Install).
	Resolved bool `json:"resolved,omitempty"`

	// RawVersion is the version as written in the ref ("" means "latest").
	RawVersion string `json:"rawVersion,omitempty"`

	// ResolvedVersion is the concrete ref hyve resolved to (tag/branch/
	// HEAD). Blank on a cache-hit resolve pass — cosmetic, not
	// correctness-bearing; SHA256 is the real integrity marker.
	ResolvedVersion string `json:"resolvedVersion,omitempty"`

	// SHA256 is the resolved file's content hash, set once Resolved.
	SHA256 string `json:"sha256,omitempty"`

	// ResolvedAt is when Resolved last became true.
	ResolvedAt metav1.Time `json:"resolvedAt,omitempty"`

	// Error holds the most recent resolution failure, if any — cleared on
	// the next successful resolve.
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=wfrs
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source`
// +kubebuilder:printcolumn:name="Resolved",type=boolean,JSONPath=`.status.resolved`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkflowRefStatus is a visibility record of a remote (git-referenced)
// WorkflowRef the controller has (attempted to) resolve automatically while
// reconciling a ClusterDefinition that referenced it — cluster mode's
// equivalent of Module for driver modules. The ClusterDefinition never
// references this CR; it's a byproduct the controller keeps in sync every
// reconcile (see internal/controller/reconciler.go's resolveWorkflowIfNeeded
// and upsertWorkflowRefStatusCR), purely so `hyve workflow list`/`kubectl
// get workflowrefstatuses.hyve.io` can show what a plain `kubectl get
// workflows.hyve.io` can't — a git-referenced workflow was never, and still
// isn't, represented by a real Workflow CR (see that kind's own doc
// comment). Not a trigger: creating or editing one by hand does not cause
// the controller to resolve anything.
type WorkflowRefStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowRefStatusSpec   `json:"spec,omitempty"`
	Status WorkflowRefStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkflowRefStatusList contains a list of WorkflowRefStatus.
type WorkflowRefStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowRefStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkflowRefStatus{}, &WorkflowRefStatusList{})
}
