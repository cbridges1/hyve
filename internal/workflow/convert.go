package workflow

import (
	"fmt"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/secretsfrom"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// toWorkflow converts a Workflow CR (the on-disk/on-cluster shape) into the
// in-memory Workflow shape the execution engine (executor.go, job_runner.go)
// operates on — unchanged in this migration, only how it's persisted
// changes. Metadata.Updated has no CRD equivalent (see WorkflowSpec's own
// doc comment) and is always zero after this conversion.
func toWorkflow(cr *hyvev1alpha1.Workflow) *Workflow {
	return &Workflow{
		APIVersion: cr.APIVersion,
		Kind:       cr.Kind,
		Metadata: WorkflowMetadata{
			Name:        cr.Name,
			Description: cr.Spec.Description,
			Labels:      cr.Labels,
			Created:     cr.CreationTimestamp.Time,
		},
		Spec: WorkflowSpec{
			Inputs:       toWorkflowInputs(cr.Spec.Inputs),
			Requirements: toWorkflowRequirements(cr.Spec.Requirements),
			PreFlight:    WorkflowPreFlight{Cluster: cr.Spec.PreFlight.Cluster},
			Triggers:     toWorkflowTriggers(cr.Spec.Triggers),
			Jobs:         toWorkflowJobs(cr.Spec.Jobs),
			Env:          cr.Spec.Env,
			Runtime:      cr.Spec.Runtime,
			SecretsFrom:  toSecretSources(cr.Spec.SecretsFrom),
		},
	}
}

// fromWorkflow converts the in-memory Workflow shape into a Workflow CR for
// persisting to disk — the inverse of toWorkflow. name is required
// separately since ObjectMeta.Name (not wf.Metadata.Name) is authoritative
// once persisted as a real object; callers pass wf.Metadata.Name.
func fromWorkflow(wf *Workflow) *hyvev1alpha1.Workflow {
	cr := &hyvev1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{APIVersion: WorkflowAPIVersion, Kind: WorkflowKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:   wf.Metadata.Name,
			Labels: wf.Metadata.Labels,
		},
		Spec: hyvev1alpha1.WorkflowSpec{
			Description:  wf.Metadata.Description,
			Inputs:       fromWorkflowInputs(wf.Spec.Inputs),
			Requirements: fromWorkflowRequirements(wf.Spec.Requirements),
			PreFlight:    hyvev1alpha1.WorkflowPreFlight{Cluster: wf.Spec.PreFlight.Cluster},
			Triggers:     fromWorkflowTriggers(wf.Spec.Triggers),
			Jobs:         fromWorkflowJobs(wf.Spec.Jobs),
			Env:          wf.Spec.Env,
			Runtime:      wf.Spec.Runtime,
			SecretsFrom:  fromSecretSources(wf.Spec.SecretsFrom),
		},
	}
	if !wf.Metadata.Created.IsZero() {
		cr.CreationTimestamp = metav1.NewTime(wf.Metadata.Created)
	}
	return cr
}

func toWorkflowInputs(in []hyvev1alpha1.WorkflowInput) []WorkflowInput {
	if in == nil {
		return nil
	}
	out := make([]WorkflowInput, len(in))
	for i, v := range in {
		out[i] = WorkflowInput{Name: v.Name, Description: v.Description, Default: v.Default}
	}
	return out
}

func fromWorkflowInputs(in []WorkflowInput) []hyvev1alpha1.WorkflowInput {
	if in == nil {
		return nil
	}
	out := make([]hyvev1alpha1.WorkflowInput, len(in))
	for i, v := range in {
		out[i] = hyvev1alpha1.WorkflowInput{Name: v.Name, Description: v.Description, Default: v.Default}
	}
	return out
}

func toWorkflowRequirements(r *hyvev1alpha1.WorkflowRequirements) *WorkflowRequirements {
	if r == nil {
		return nil
	}
	tools := make([]ToolRequirement, len(r.Tools))
	for i, t := range r.Tools {
		tools[i] = ToolRequirement{Name: t.Name, Version: t.Version, Description: t.Description}
	}
	secrets := make([]SecretRequirement, len(r.Secrets))
	for i, s := range r.Secrets {
		secrets[i] = SecretRequirement{Name: s.Name, Provider: s.Provider, Required: s.Required, Description: s.Description}
	}
	return &WorkflowRequirements{Tools: tools, Secrets: secrets}
}

func fromWorkflowRequirements(r *WorkflowRequirements) *hyvev1alpha1.WorkflowRequirements {
	if r == nil {
		return nil
	}
	tools := make([]hyvev1alpha1.ToolRequirement, len(r.Tools))
	for i, t := range r.Tools {
		tools[i] = hyvev1alpha1.ToolRequirement{Name: t.Name, Version: t.Version, Description: t.Description}
	}
	secrets := make([]hyvev1alpha1.SecretRequirement, len(r.Secrets))
	for i, s := range r.Secrets {
		secrets[i] = hyvev1alpha1.SecretRequirement{Name: s.Name, Provider: s.Provider, Required: s.Required, Description: s.Description}
	}
	return &hyvev1alpha1.WorkflowRequirements{Tools: tools, Secrets: secrets}
}

// toWorkflowTriggers/fromWorkflowTriggers convert Config between
// map[string]interface{} (internal/workflow, unconstrained) and
// map[string]string (the CRD, which needs a concrete schema type — see
// hyvev1alpha1.WorkflowTrigger's doc comment). Values are stringified with
// fmt.Sprint on the way to the CRD; nothing in the execution engine reads
// Config today, so this is a safe best-effort conversion, not a lossy one
// in practice.
func toWorkflowTriggers(in []hyvev1alpha1.WorkflowTrigger) []WorkflowTrigger {
	if in == nil {
		return nil
	}
	out := make([]WorkflowTrigger, len(in))
	for i, t := range in {
		var cfg map[string]interface{}
		if t.Config != nil {
			cfg = make(map[string]interface{}, len(t.Config))
			for k, v := range t.Config {
				cfg[k] = v
			}
		}
		out[i] = WorkflowTrigger{Type: t.Type, Config: cfg}
	}
	return out
}

func fromWorkflowTriggers(in []WorkflowTrigger) []hyvev1alpha1.WorkflowTrigger {
	if in == nil {
		return nil
	}
	out := make([]hyvev1alpha1.WorkflowTrigger, len(in))
	for i, t := range in {
		var cfg map[string]string
		if t.Config != nil {
			cfg = make(map[string]string, len(t.Config))
			for k, v := range t.Config {
				if s, ok := v.(string); ok {
					cfg[k] = s
				} else {
					cfg[k] = fmt.Sprint(v)
				}
			}
		}
		out[i] = hyvev1alpha1.WorkflowTrigger{Type: t.Type, Config: cfg}
	}
	return out
}

func toWorkflowJobs(in []hyvev1alpha1.WorkflowJob) []WorkflowJob {
	if in == nil {
		return nil
	}
	out := make([]WorkflowJob, len(in))
	for i, j := range in {
		out[i] = WorkflowJob{
			Name:        j.Name,
			Description: j.Description,
			If:          j.If,
			DependsOn:   j.DependsOn,
			Cluster:     j.Cluster,
			Env:         j.Env,
			Steps:       toWorkflowSteps(j.Steps),
			Timeout:     j.Timeout,
			Retry:       toWorkflowRetryPolicy(j.Retry),
			Container:   j.Container,
		}
	}
	return out
}

func fromWorkflowJobs(in []WorkflowJob) []hyvev1alpha1.WorkflowJob {
	if in == nil {
		return nil
	}
	out := make([]hyvev1alpha1.WorkflowJob, len(in))
	for i, j := range in {
		out[i] = hyvev1alpha1.WorkflowJob{
			Name:        j.Name,
			Description: j.Description,
			If:          j.If,
			DependsOn:   j.DependsOn,
			Cluster:     j.Cluster,
			Env:         j.Env,
			Steps:       fromWorkflowSteps(j.Steps),
			Timeout:     j.Timeout,
			Retry:       fromWorkflowRetryPolicy(j.Retry),
			Container:   j.Container,
		}
	}
	return out
}

func toWorkflowSteps(in []hyvev1alpha1.WorkflowStep) []WorkflowStep {
	if in == nil {
		return nil
	}
	out := make([]WorkflowStep, len(in))
	for i, s := range in {
		out[i] = WorkflowStep{
			Name: s.Name, Description: s.Description, If: s.If,
			Command: s.Command, Script: s.Script, Action: s.Action,
			With: s.With, Env: s.Env, WorkingDir: s.WorkingDir,
			Timeout: s.Timeout, ContinueOnError: s.ContinueOnError, Container: s.Container,
		}
	}
	return out
}

func fromWorkflowSteps(in []WorkflowStep) []hyvev1alpha1.WorkflowStep {
	if in == nil {
		return nil
	}
	out := make([]hyvev1alpha1.WorkflowStep, len(in))
	for i, s := range in {
		out[i] = hyvev1alpha1.WorkflowStep{
			Name: s.Name, Description: s.Description, If: s.If,
			Command: s.Command, Script: s.Script, Action: s.Action,
			With: s.With, Env: s.Env, WorkingDir: s.WorkingDir,
			Timeout: s.Timeout, ContinueOnError: s.ContinueOnError, Container: s.Container,
		}
	}
	return out
}

func toWorkflowRetryPolicy(r *hyvev1alpha1.WorkflowRetryPolicy) *WorkflowRetryPolicy {
	if r == nil {
		return nil
	}
	return &WorkflowRetryPolicy{MaxAttempts: r.MaxAttempts, Delay: r.Delay}
}

func fromWorkflowRetryPolicy(r *WorkflowRetryPolicy) *hyvev1alpha1.WorkflowRetryPolicy {
	if r == nil {
		return nil
	}
	return &hyvev1alpha1.WorkflowRetryPolicy{MaxAttempts: r.MaxAttempts, Delay: r.Delay}
}

func toSecretSources(in []hyvev1alpha1.WorkflowSecretSource) []secretsfrom.SecretSource {
	if in == nil {
		return nil
	}
	out := make([]secretsfrom.SecretSource, len(in))
	for i, s := range in {
		keys := make([]secretsfrom.SecretKeyMap, len(s.Keys))
		for j, k := range s.Keys {
			keys[j] = secretsfrom.SecretKeyMap{Key: k.Key, Env: k.Env}
		}
		out[i] = secretsfrom.SecretSource{Cluster: s.Cluster, Namespace: s.Namespace, SecretRef: s.SecretRef, Keys: keys}
	}
	return out
}

func fromSecretSources(in []secretsfrom.SecretSource) []hyvev1alpha1.WorkflowSecretSource {
	if in == nil {
		return nil
	}
	out := make([]hyvev1alpha1.WorkflowSecretSource, len(in))
	for i, s := range in {
		keys := make([]hyvev1alpha1.WorkflowSecretKeyMap, len(s.Keys))
		for j, k := range s.Keys {
			keys[j] = hyvev1alpha1.WorkflowSecretKeyMap{Key: k.Key, Env: k.Env}
		}
		out[i] = hyvev1alpha1.WorkflowSecretSource{Cluster: s.Cluster, Namespace: s.Namespace, SecretRef: s.SecretRef, Keys: keys}
	}
	return out
}
