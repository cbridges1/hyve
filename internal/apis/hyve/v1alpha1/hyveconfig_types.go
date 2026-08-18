package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HyveConfigSpec is the controller-mode equivalent of file mode's hyve.yaml
// — deliberately narrower than internal/state.RepoConfig, since several of
// that type's fields are file-mode-only concepts with no controller-mode
// analogue: Reconcile.Mode (local vs. cicd) doesn't apply — a controller
// always reconciles every ClusterDefinition it watches, there's no
// "defer to CI" branch — and Env.File doesn't apply either, since a
// long-running controller pod gets its environment from its own
// container/Secret mounts, not a dotenv file loaded at CLI startup. Only
// the fields ReconcileOne's call graph actually consumes are carried over.
type HyveConfigSpec struct {
	// StrictResourceDelete mirrors internal/state.ReconcileConfig.StrictResourceDelete
	// — when true, a spec.resources entry removed without delete:true is
	// auto-pruned instead of just logged as an orphan warning.
	StrictResourceDelete bool `json:"strictResourceDelete,omitempty"`

	// DefaultWorkflowImage is the container image KubernetesJobStepRunner
	// falls back to for a workflow job/step that doesn't set its own
	// container:. See internal/workflow's StepRunner design — resolution
	// order is per-step container: -> per-job container: ->
	// this field -> hard failure. Left unset, a workflow with no
	// container: anywhere fails pre-flight validation with a clear error
	// naming which of the three levels was missing, rather than silently
	// falling back to a hyve-hardcoded image. No hyve-built or -maintained
	// image is implied here; a real, actively-maintained image (e.g.
	// alpine/k8s:<version>, which bundles kubectl/helm) is only a
	// documented suggestion for operators to configure, never a
	// code-level default.
	DefaultWorkflowImage string `json:"defaultWorkflowImage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=hc

// HyveConfig is the Schema for the hyveconfigs API. Treated as a singleton:
// exactly one HyveConfig object (name and namespace chosen at controller
// deploy time, passed via --config-name/--config-namespace) is read by
// CRDStateProvider.LoadRepoConfig per reconcile — see internal/controller.
type HyveConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HyveConfigSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// HyveConfigList contains a list of HyveConfig.
type HyveConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyveConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyveConfig{}, &HyveConfigList{})
}
