package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModuleSpec identifies a driver module by source/version — mirrors
// DriverRef, kept as its own type rather than reusing DriverRef directly
// since a Module CR's identity (its name) is derived from these two fields
// together, unlike DriverRef which is always embedded inside something
// else.
type ModuleSpec struct {
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
}

// ModuleStatus is reconciler-owned observed state — written only via the
// status subresource. See internal/controller/reconciler.go's
// resolveModuleIfNeeded: this is a write-only record of an auto-resolve
// outcome, not something any reconcile loop watches or acts on.
type ModuleStatus struct {
	// Resolved is true once the module has been successfully fetched and
	// locked (see internal/module.EnsureResolved).
	Resolved bool `json:"resolved,omitempty"`

	// SHA256 is the resolved module's content hash, set once Resolved.
	SHA256 string `json:"sha256,omitempty"`

	// ResolvedAt is when Resolved last became true.
	ResolvedAt metav1.Time `json:"resolvedAt,omitempty"`

	// Error holds the most recent resolution failure, if any — cleared on
	// the next successful resolve.
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=mod
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Resolved",type=boolean,JSONPath=`.status.resolved`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Module is the Schema for the modules API — a visibility record of a
// driver module the controller has (attempted to) resolve automatically
// while reconciling a ClusterDefinition that referenced it, since cluster
// mode has no human-run `hyve module install` step to make that state
// discoverable otherwise. Not a trigger: creating one by hand does not
// cause the controller to resolve anything — see
// internal/controller/reconciler.go's resolveModuleIfNeeded, which is the
// only writer.
type Module struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModuleSpec   `json:"spec,omitempty"`
	Status ModuleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ModuleList contains a list of Module.
type ModuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Module `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Module{}, &ModuleList{})
}
