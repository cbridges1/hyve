package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunnerSpec configures how a Template's driver module should be executed
// when a cluster is created from it. Mirrors internal/template.TemplateRunnerConfig.
type RunnerSpec struct {
	Image string `json:"image,omitempty"`
}

// TemplateSpec is what a ClusterDefinition looks like before its
// name/params/region are filled in — see RenderClusterDefinitionSpec, which
// both local mode (`hyve cluster create --template`) and cluster mode
// (POST /api/templates/{name}/render, POST /api/clusters with a template
// field) call to turn one of these into a real ClusterDefinitionSpec.
// Mirrors internal/template.TemplateSpec; Description moved off ObjectMeta
// onto Spec, the same pattern ClusterDefinitionSpec.Region already uses —
// a real Kubernetes object's metadata can only hold standard ObjectMeta
// fields, not arbitrary custom ones.
type TemplateSpec struct {
	Description string            `json:"description,omitempty"`
	Driver      DriverRef         `json:"driver,omitempty"`
	Runner      RunnerSpec        `json:"runner,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
	Region      string            `json:"region,omitempty"`
	Workflows   WorkflowsSpec     `json:"workflows,omitempty"`
	Resources   []ResourceRef     `json:"resources,omitempty"`

	// Schedule is a 5-field cron expression (e.g. "0 20 * * 5"). When a
	// cluster is created from this template, the next occurrence is
	// calculated and written to the generated cluster's spec.expiresAt.
	Schedule string `json:"schedule,omitempty"`

	// LockParams prevents overriding default param values when creating a
	// cluster from this template — the --set flag (local mode) or a
	// render request's params (cluster mode) are rejected rather than
	// silently ignored. Intended for admins enforcing standard
	// configurations.
	LockParams bool `json:"lockParams,omitempty"`

	// Access mirrors ClusterDefinitionSpec.Access — copied verbatim by
	// RenderClusterDefinitionSpec into every cluster created from this
	// template. Cluster-mode only, like the rest of AccessSpec (a
	// local-mode-rendered cluster has no live API for Agent to mean
	// anything against); internal/template.TemplateSpec
	// (local mode's own template type, converted into this one before
	// rendering) has no equivalent field, so a local-mode template
	// always renders a zero-value Access here. Found missing live,
	// confirmed by a real Template CRD silently dropping a hand-written
	// spec.access.agent block at the API server's own schema-validation
	// layer — the CRD simply had no such property to preserve, regardless
	// of how the Template was created (--set, -f a file, or a direct
	// kubectl apply all hit the same gap).
	Access AccessSpec `json:"access,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=tpl
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver.source`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Template is the Schema for the templates API — a reusable
// ClusterDefinition scaffold. No status: purely declarative config, read
// on demand when rendering a cluster, never itself reconciled.
type Template struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec TemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// TemplateList contains a list of Template.
type TemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Template `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Template{}, &TemplateList{})
}
