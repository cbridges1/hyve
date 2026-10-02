package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowRunSpec identifies one ad hoc workflow execution request: run
// WorkflowRef against the already-provisioned cluster named ClusterRef,
// with Params overlaid on top of that cluster's own spec.params (Params
// wins on conflict). Unlike a lifecycle hook (ClusterDefinitionSpec.
// Workflows), this never touches cluster create/delete/scale/resource
// reconciliation — see WorkflowRun's own doc comment.
type WorkflowRunSpec struct {
	WorkflowRef WorkflowRef       `json:"workflowRef"`
	ClusterRef  string            `json:"clusterRef"`
	Params      map[string]string `json:"params,omitempty"`
}

// WorkflowRunStatus is reconciler-owned observed state — written only via
// the status subresource.
type WorkflowRunStatus struct {
	// Phase is one of "" (not yet picked up), Pending, Running, Succeeded,
	// Failed.
	Phase string `json:"phase,omitempty"`

	// Message holds a short human-readable outcome/failure summary — e.g.
	// "cluster \"foo\" not found". Cleared on a fresh Pending.
	Message string `json:"message,omitempty"`

	// Output is the workflow's own captured step output (the same text
	// `hyve workflow run --output` would print locally). Truncated past
	// maxWorkflowRunOutputBytes — this is an etcd-backed CRD field, not a
	// log stream, so it must stay bounded regardless of how verbose a
	// workflow's steps are.
	Output string `json:"output,omitempty"`

	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// WorkflowRunPhase* are the only valid WorkflowRunStatus.Phase values.
const (
	WorkflowRunPhasePending   = "Pending"
	WorkflowRunPhaseRunning   = "Running"
	WorkflowRunPhaseSucceeded = "Succeeded"
	WorkflowRunPhaseFailed    = "Failed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=wfr
// +kubebuilder:printcolumn:name="Workflow",type=string,JSONPath=`.spec.workflowRef.name`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkflowRun is the Schema for the workflowruns API — a one-shot, ad hoc
// "run this workflow against this cluster now" request, created by
// `hyve workflow run --cluster <name>` in cluster mode (see
// cmd/workflow/cmd.go) and picked up by WorkflowRunReconciler
// (internal/controller/workflowrun_reconciler.go), which reuses the exact
// same auth+workflow-execution primitive (reconcile.Reconciler.
// RunAdHocWorkflow) that lifecycle hooks (onCreate/afterCreate/etc.)
// already run through — "same engine," not a second execution path. Not a
// trigger for anything else, and never garbage-collected by hyve itself —
// a completed WorkflowRun just sits there until `kubectl delete` removes
// it, the same way a completed batch/v1 Job would.
type WorkflowRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowRunSpec   `json:"spec,omitempty"`
	Status WorkflowRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkflowRunList contains a list of WorkflowRun.
type WorkflowRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkflowRun{}, &WorkflowRunList{})
}
