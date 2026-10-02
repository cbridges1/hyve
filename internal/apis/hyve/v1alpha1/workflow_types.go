package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Runtime values for WorkflowSpec.Runtime — see its doc comment. Mirrors
// internal/workflow.RuntimeClient/RuntimeCluster.
const (
	WorkflowRuntimeClient  = "client"
	WorkflowRuntimeCluster = "cluster" // also the default when Runtime is unset
)

// WorkflowInput declares a variable that must be present before the
// workflow runs. Mirrors internal/workflow.WorkflowInput.
type WorkflowInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

// WorkflowPreFlight controls the cluster pre-flight check run before the
// workflow. Mirrors internal/workflow.WorkflowPreFlight.
type WorkflowPreFlight struct {
	Cluster string `json:"cluster,omitempty"`
}

// WorkflowRequirements defines prerequisites for workflow execution.
// Mirrors internal/workflow.WorkflowRequirements.
type WorkflowRequirements struct {
	Tools   []ToolRequirement   `json:"tools,omitempty"`
	Secrets []SecretRequirement `json:"secrets,omitempty"`
}

// ToolRequirement specifies a required CLI tool. Mirrors
// internal/workflow.ToolRequirement.
type ToolRequirement struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// SecretRequirement specifies a required secret or credential. Mirrors
// internal/workflow.SecretRequirement.
type SecretRequirement struct {
	Name        string `json:"name"`
	Provider    string `json:"provider,omitempty"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// WorkflowTrigger defines when the workflow should run. Config is
// map[string]string here, narrower than internal/workflow.WorkflowTrigger's
// map[string]interface{} — a real CRD schema needs a concrete value type,
// and nothing in internal/workflow's executor actually reads Config today
// (confirmed: no code dereferences WorkflowTrigger.Config anywhere), so
// this is a safe, deliberate simplification rather than a functional loss.
type WorkflowTrigger struct {
	Type   string            `json:"type"`
	Config map[string]string `json:"config,omitempty"`
}

// WorkflowRetryPolicy defines retry behavior for jobs. Mirrors
// internal/workflow.WorkflowRetryPolicy.
type WorkflowRetryPolicy struct {
	MaxAttempts int    `json:"maxAttempts"`
	Delay       string `json:"delay,omitempty"`
}

// WorkflowStep represents a single step in a job. Mirrors
// internal/workflow.WorkflowStep.
type WorkflowStep struct {
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	If              string            `json:"if,omitempty"`
	Command         string            `json:"command,omitempty"`
	Script          string            `json:"script,omitempty"`
	Action          string            `json:"action,omitempty"`
	With            map[string]string `json:"with,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	WorkingDir      string            `json:"workingDir,omitempty"`
	Timeout         string            `json:"timeout,omitempty"`
	ContinueOnError bool              `json:"continueOnError,omitempty"`

	// Container overrides WorkflowJob.Container for this step only.
	Container string `json:"container,omitempty"`
}

// WorkflowJob represents a job within a workflow. Mirrors
// internal/workflow.WorkflowJob.
type WorkflowJob struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	If          string               `json:"if,omitempty"`
	DependsOn   []string             `json:"dependsOn,omitempty"`
	Cluster     string               `json:"cluster,omitempty"`
	Env         map[string]string    `json:"env,omitempty"`
	Steps       []WorkflowStep       `json:"steps"`
	Timeout     string               `json:"timeout,omitempty"`
	Retry       *WorkflowRetryPolicy `json:"retry,omitempty"`

	// Container is the image every step in this job runs in under
	// KubernetesJobStepRunner (controller mode) — meaningless, and
	// ignored rather than validated, under LocalStepRunner. A step's own
	// Container overrides this; HyveConfig.spec.defaultWorkflowImage is
	// the last fallback before a hard pre-flight failure.
	Container string `json:"container,omitempty"`
}

// WorkflowSecretKeyMap is one key mapping within WorkflowSecretSource.
// Mirrors internal/secretsfrom.SecretKeyMap — duplicated rather than
// imported so controller-gen's generated DeepCopy for Workflow doesn't
// need a hand-written DeepCopyInto for a type outside this package (the
// same reasoning as every other "Mirrors internal/X" type in this file).
type WorkflowSecretKeyMap struct {
	Key string `json:"key"`
	Env string `json:"env,omitempty"`
}

// WorkflowSecretSource declares one Kubernetes Secret to resolve into env
// vars before a workflow's steps run. Mirrors internal/secretsfrom.SecretSource.
type WorkflowSecretSource struct {
	Cluster   string                 `json:"cluster"`
	Namespace string                 `json:"namespace"`
	SecretRef string                 `json:"secretRef"`
	Keys      []WorkflowSecretKeyMap `json:"keys"`
}

// WorkflowSpec defines the workflow specification. Mirrors
// internal/workflow.WorkflowSpec. Description moved off ObjectMeta onto
// Spec (same pattern as TemplateSpec.Description); Labels maps to real
// ObjectMeta.Labels instead; Created/Updated are dropped — Created is
// subsumed by ObjectMeta.CreationTimestamp (server-set), and Updated has no
// clean CRD equivalent (closest is resourceVersion/generation, different
// semantics) — a deliberate, small behavior change, not an oversight.
type WorkflowSpec struct {
	Description  string                 `json:"description,omitempty"`
	Inputs       []WorkflowInput        `json:"inputs,omitempty"`
	Requirements *WorkflowRequirements  `json:"requirements,omitempty"`
	PreFlight    WorkflowPreFlight      `json:"preFlight,omitempty"`
	Triggers     []WorkflowTrigger      `json:"triggers,omitempty"`
	Jobs         []WorkflowJob          `json:"jobs"`
	Env          map[string]string      `json:"env,omitempty"`
	Runtime      string                 `json:"runtime,omitempty"`
	SecretsFrom  []WorkflowSecretSource `json:"secretsFrom,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=wf
// +kubebuilder:printcolumn:name="Description",type=string,JSONPath=`.spec.description`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Workflow is the Schema for the workflows API — a named sequence of jobs
// hyve can run standalone (`hyve workflow run`) or as a ClusterDefinition
// lifecycle hook (WorkflowRef.Name). No status: purely declarative config,
// read on demand at execution time, never itself reconciled.
type Workflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkflowSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// WorkflowList contains a list of Workflow.
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workflow{}, &WorkflowList{})
}
