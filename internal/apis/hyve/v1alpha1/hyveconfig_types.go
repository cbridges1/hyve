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
	// order is per-step container: -> per-job container: -> this field ->
	// k8sjob's own hardcoded fallback image (see k8sjob.Run's
	// defaultFallbackImage doc comment — a deliberate, narrow exception to
	// this field's own "no hyve-built or -maintained image" stance below,
	// added so a completely unconfigured cluster-mode install still works
	// rather than hard-failing). A real, actively-maintained image (e.g.
	// alpine/k8s:<version>, which bundles kubectl/helm) set here is still
	// only a documented suggestion for operators to configure, never a
	// code-level default — that stance is unchanged; only the very last,
	// nothing-configured-anywhere case now has a fallback instead of a
	// hard error. Pair with ImageInstalls (below) to bootstrap any image
	// (including the fallback) with whatever tools it's missing.
	DefaultWorkflowImage string `json:"defaultWorkflowImage,omitempty"`

	// DefaultModuleImage is the container image module.JobRunner falls back
	// to for a driver module's create/status/delete/auth operation whose
	// ClusterDefinition doesn't set spec.runner.image (see RunnerSpec —
	// set directly on a ClusterDefinition, or inherited from the Template
	// it was created from). Resolution order is
	// ClusterDefinition.spec.runner.image -> this field -> k8sjob's own
	// hardcoded fallback image (see DefaultWorkflowImage's doc comment for
	// why that last tier exists), one tier shorter than
	// DefaultWorkflowImage's chain since modules have no per-operation
	// image, only a per-cluster one. Deliberately does NOT consult the
	// module's own module.yaml — a module can recommend a suitable image
	// (its requirements.tools entries' description field) but doesn't
	// choose one, since the same module may need different images across
	// different deployments; that choice belongs to whoever is
	// instantiating it (a Template author or the ClusterDefinition
	// itself), not the module. Same non-default stance as
	// DefaultWorkflowImage otherwise: no hyve-built or -maintained image is
	// implied, and this field is only consulted at all in cluster mode —
	// local/CLI mode always runs modules inline, never via a Job.
	DefaultModuleImage string `json:"defaultModuleImage,omitempty"`

	// ImageInstalls declares, per exact image reference, a shell script to
	// run once at the start of every Job dispatched with that image —
	// module and workflow Jobs alike, matched against whichever image a
	// Job actually ends up using after every resolution tier above
	// (including k8sjob's own fallback image when nothing else is
	// configured). Declared here, centrally, rather than on a module's or
	// workflow's own tool requirements: only whoever chose an image
	// actually knows what OS/package manager it has, and a module/workflow
	// author has no such guarantee — the same image may be shared across
	// many different modules/workflows, each of which would otherwise need
	// its own (potentially inconsistent) guess at how to bootstrap it.
	// Centralizing per image means exactly one install declaration per
	// image, run once per Job, regardless of how many different
	// modules/workflows happen to share it. No entry matching a Job's
	// resolved image means no install step runs — an already-complete
	// image works exactly as it does today.
	ImageInstalls []ImageInstall `json:"imageInstalls,omitempty"`

	// ImagePullSecrets names existing kubernetes.io/dockerconfigjson
	// Secrets (in the controller's own namespace) attached to every
	// module/workflow Job's PodSpec so kubelet can authenticate pulling a
	// private runner/container: image — see k8sjob.RunRequest's own doc
	// comment. A public image needs nothing here. Cluster-wide, not
	// per-module/per-step: the registry credentials a Job needs depend on
	// which registry its image lives in, not which module or workflow
	// dispatched it. This chart never creates the Secret itself — create
	// it once yourself (e.g. `kubectl create secret docker-registry`) and
	// name it here. Only consulted in cluster mode — local/CLI mode always
	// runs everything inline, never via a Job, so pulling a container image
	// never comes up at all.
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`
}

// ImageInstall is one entry in HyveConfigSpec.ImageInstalls.
type ImageInstall struct {
	// Image is matched by exact string equality against the fully-resolved
	// image a Job ends up using — not a pattern or prefix.
	Image string `json:"image"`
	// Install is the shell script run once, before the Job's own
	// operation script, inside that same container.
	Install string `json:"install"`
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
