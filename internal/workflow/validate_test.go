package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func validWorkflow() *Workflow {
	return &Workflow{
		APIVersion: WorkflowAPIVersion,
		Kind:       "Workflow",
		Metadata:   WorkflowMetadata{Name: "test"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{Name: "job1", Steps: []WorkflowStep{{Name: "step1", Command: "echo hi"}}},
			},
		},
	}
}

func TestValidate_ValidWorkflowHasNoErrorsOrWarnings(t *testing.T) {
	errors, warnings := Validate(validWorkflow())
	assert.Empty(t, errors)
	assert.Empty(t, warnings)
}

func TestValidate_RuntimeClientWithJobContainer_Warns(t *testing.T) {
	wf := validWorkflow()
	wf.Spec.Runtime = RuntimeClient
	wf.Spec.Jobs[0].Container = "alpine:3"

	_, warnings := Validate(wf)
	assert.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0], "job1")
	assert.Contains(t, warnings[0], "runtime: client")
}

func TestValidate_RuntimeClientWithStepContainer_Warns(t *testing.T) {
	wf := validWorkflow()
	wf.Spec.Runtime = RuntimeClient
	wf.Spec.Jobs[0].Steps[0].Container = "alpine:3"

	_, warnings := Validate(wf)
	assert.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0], "step1")
}

func TestValidate_RuntimeClientWithNoContainer_NoWarning(t *testing.T) {
	wf := validWorkflow()
	wf.Spec.Runtime = RuntimeClient

	_, warnings := Validate(wf)
	assert.Empty(t, warnings)
}

func TestValidate_ContainerOnNonClientRuntime_NoWarning(t *testing.T) {
	wf := validWorkflow()
	wf.Spec.Jobs[0].Container = "alpine:3" // Runtime left unset ("cluster")

	_, warnings := Validate(wf)
	assert.Empty(t, warnings, "container: is meaningful for cluster-runtime workflows — no lint warning expected")
}
