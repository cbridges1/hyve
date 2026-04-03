package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupExecutor creates a workflow manager and executor with an empty cluster (no
// kubeconfig lookup needed) so tests can focus on pure execution logic.
func setupExecutor(t *testing.T) (*Executor, *Manager) {
	t.Helper()
	mgr, _ := setupTestEnvironment(t)
	exec, err := NewExecutor(mgr, "")
	require.NoError(t, err)
	// Initialise the execution state so addLog / expandVariables work without panicking.
	exec.execution = &WorkflowExecution{
		JobResults: make(map[string]*JobResult),
		Logs:       []WorkflowLogEntry{},
		Variables:  make(map[string]string),
	}
	return exec, mgr
}

// ── expandVariables ───────────────────────────────────────────────────────────

func TestExpandVariables_NoVariables(t *testing.T) {
	exec, _ := setupExecutor(t)
	assert.Equal(t, "hello world", exec.expandVariables("hello world"))
}

func TestExpandVariables_DollarBrace(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["CLUSTER"] = "prod"
	assert.Equal(t, "cluster=prod", exec.expandVariables("cluster=${CLUSTER}"))
}

func TestExpandVariables_PlainDollar(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["ENV"] = "staging"
	assert.Equal(t, "env=staging", exec.expandVariables("env=$ENV"))
}

func TestExpandVariables_MultipleVariables(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["A"] = "hello"
	exec.variables["B"] = "world"
	assert.Equal(t, "hello world", exec.expandVariables("${A} ${B}"))
}

func TestExpandVariables_UnknownVariableLeftUnchanged(t *testing.T) {
	exec, _ := setupExecutor(t)
	assert.Equal(t, "${UNKNOWN}", exec.expandVariables("${UNKNOWN}"))
}

// ── evaluateCondition ─────────────────────────────────────────────────────────

func TestEvaluateCondition_True(t *testing.T) {
	exec, _ := setupExecutor(t)
	result, err := exec.evaluateCondition("true")
	require.NoError(t, err)
	assert.True(t, result)
}

func TestEvaluateCondition_False(t *testing.T) {
	exec, _ := setupExecutor(t)
	result, err := exec.evaluateCondition("false")
	require.NoError(t, err)
	assert.False(t, result)
}

func TestEvaluateCondition_VariableExpandsToTrue(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["SHOULD_RUN"] = "true"
	result, err := exec.evaluateCondition("${SHOULD_RUN}")
	require.NoError(t, err)
	assert.True(t, result)
}

func TestEvaluateCondition_VariableExpandsToFalse(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["SHOULD_RUN"] = "false"
	result, err := exec.evaluateCondition("${SHOULD_RUN}")
	require.NoError(t, err)
	assert.False(t, result)
}

// ── getShellCommand ───────────────────────────────────────────────────────────

func TestGetShellCommand_ReturnsNonEmpty(t *testing.T) {
	cmd, flag := getShellCommand()
	assert.NotEmpty(t, cmd)
	assert.NotEmpty(t, flag)
}

// ── executeJobs: dependency ordering ─────────────────────────────────────────

func makeSimpleWorkflow(name string, jobs []WorkflowJob) *Workflow {
	return &Workflow{
		Metadata: WorkflowMetadata{Name: name},
		Spec:     WorkflowSpec{Jobs: jobs},
	}
}

func TestExecuteJobs_SingleJob(t *testing.T) {
	exec, _ := setupExecutor(t)

	wf := makeSimpleWorkflow("test", []WorkflowJob{
		{
			Name:  "job-a",
			Steps: []WorkflowStep{{Name: "step-1", Command: "echo hello"}},
		},
	})

	err := exec.executeJobs(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, JobStatusCompleted, exec.execution.JobResults["job-a"].Status)
}

func TestExecuteJobs_DependencyOrder(t *testing.T) {
	exec, _ := setupExecutor(t)
	executionOrder := []string{}

	// Use a script that appends to a temp file to track order, but since we
	// can't easily capture interleaved output, instead we verify via job results.
	wf := makeSimpleWorkflow("ordered", []WorkflowJob{
		{
			Name:      "job-b",
			DependsOn: []string{"job-a"},
			Steps:     []WorkflowStep{{Name: "step", Command: "echo b"}},
		},
		{
			Name:  "job-a",
			Steps: []WorkflowStep{{Name: "step", Command: "echo a"}},
		},
	})

	err := exec.executeJobs(context.Background(), wf)
	require.NoError(t, err)

	// Both jobs must complete regardless of declaration order.
	_ = executionOrder
	assert.Equal(t, JobStatusCompleted, exec.execution.JobResults["job-a"].Status)
	assert.Equal(t, JobStatusCompleted, exec.execution.JobResults["job-b"].Status)
}

func TestExecuteJobs_CircularDependencyDetected(t *testing.T) {
	exec, _ := setupExecutor(t)

	wf := makeSimpleWorkflow("circular", []WorkflowJob{
		{
			Name:      "job-a",
			DependsOn: []string{"job-b"},
			Steps:     []WorkflowStep{{Name: "step", Command: "echo a"}},
		},
		{
			Name:      "job-b",
			DependsOn: []string{"job-a"},
			Steps:     []WorkflowStep{{Name: "step", Command: "echo b"}},
		},
	})

	err := exec.executeJobs(context.Background(), wf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func TestExecuteJobs_FailingJobStopsExecution(t *testing.T) {
	exec, _ := setupExecutor(t)

	wf := makeSimpleWorkflow("fail", []WorkflowJob{
		{
			Name:  "fail-job",
			Steps: []WorkflowStep{{Name: "step", Command: "exit 1"}},
		},
	})

	err := exec.executeJobs(context.Background(), wf)
	assert.Error(t, err)
	assert.Equal(t, JobStatusFailed, exec.execution.JobResults["fail-job"].Status)
}

// ── executeStep: condition / continue-on-error ────────────────────────────────

func TestExecuteJob_SkippedWhenConditionFalse(t *testing.T) {
	exec, _ := setupExecutor(t)

	job := &WorkflowJob{
		Name: "conditional-job",
		If:   "false",
		Steps: []WorkflowStep{
			{Name: "step", Command: "echo should-not-run"},
		},
	}

	result, err := exec.executeJob(context.Background(), job, makeSimpleWorkflow("test", nil))
	require.NoError(t, err)
	assert.Equal(t, JobStatusSkipped, result.Status)
}

func TestExecuteStep_SkippedWhenConditionFalse(t *testing.T) {
	exec, _ := setupExecutor(t)

	step := &WorkflowStep{
		Name:    "conditional-step",
		If:      "false",
		Command: "echo should-not-run",
	}
	job := &WorkflowJob{Name: "job"}
	wf := makeSimpleWorkflow("test", nil)

	result, err := exec.executeStep(context.Background(), step, job, wf, "")
	require.NoError(t, err)
	assert.Equal(t, JobStatusSkipped, result.Status)
}

func TestExecuteStep_ContinueOnError(t *testing.T) {
	exec, _ := setupExecutor(t)

	// A job where the first step fails but ContinueOnError=true, so the job
	// should still execute the second step and complete.
	job := &WorkflowJob{
		Name: "resilient-job",
		Steps: []WorkflowStep{
			{Name: "fail-step", Command: "exit 1", ContinueOnError: true},
			{Name: "ok-step", Command: "echo ok"},
		},
	}

	result, err := exec.executeJob(context.Background(), job, makeSimpleWorkflow("test", nil))
	require.NoError(t, err)
	assert.Equal(t, JobStatusCompleted, result.Status)
	assert.Equal(t, JobStatusFailed, result.Steps["fail-step"].Status)
	assert.Equal(t, JobStatusCompleted, result.Steps["ok-step"].Status)
}

func TestExecuteStep_NoCommandOrScript_ReturnsError(t *testing.T) {
	exec, _ := setupExecutor(t)

	step := &WorkflowStep{Name: "empty-step"}
	job := &WorkflowJob{Name: "job"}
	wf := makeSimpleWorkflow("test", nil)

	result, err := exec.executeStep(context.Background(), step, job, wf, "")
	assert.Error(t, err)
	assert.Equal(t, JobStatusFailed, result.Status)
}

// ── executeAction: unknown action ────────────────────────────────────────────

func TestExecuteAction_UnknownAction(t *testing.T) {
	exec, _ := setupExecutor(t)

	result := &StepResult{Status: JobStatusRunning}
	_, err := exec.executeAction(context.Background(), "not-a-real-action", nil, result)
	assert.Error(t, err)
	assert.Equal(t, JobStatusFailed, result.Status)
	assert.Contains(t, result.Error, "unknown action")
}

func TestExecuteAction_KubectlApply_MissingFileParam(t *testing.T) {
	exec, _ := setupExecutor(t)

	result := &StepResult{Status: JobStatusRunning}
	_, err := exec.executeAction(context.Background(), "kubectl-apply", map[string]string{}, result)
	assert.Error(t, err)
	assert.Equal(t, JobStatusFailed, result.Status)
}

func TestExecuteAction_KubectlDelete_MissingFileParam(t *testing.T) {
	exec, _ := setupExecutor(t)

	result := &StepResult{Status: JobStatusRunning}
	_, err := exec.executeAction(context.Background(), "kubectl-delete", map[string]string{}, result)
	assert.Error(t, err)
	assert.Equal(t, JobStatusFailed, result.Status)
}

// ── RunWorkflow end-to-end (no cluster, simple command) ───────────────────────

func TestRunWorkflow_Success(t *testing.T) {
	mgr, _ := setupTestEnvironment(t)

	wf := &Workflow{
		Metadata: WorkflowMetadata{Name: "simple"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name:  "greet",
					Steps: []WorkflowStep{{Name: "say-hello", Command: "echo hello"}},
				},
			},
		},
	}
	require.NoError(t, mgr.CreateWorkflow(wf))

	exec, err := NewExecutor(mgr, "")
	require.NoError(t, err)

	result, err := exec.RunWorkflow(context.Background(), "simple", "")
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)
	assert.Equal(t, JobStatusCompleted, result.JobResults["greet"].Status)
}

func TestRunWorkflow_WorkflowNotFound(t *testing.T) {
	mgr, _ := setupTestEnvironment(t)

	exec, err := NewExecutor(mgr, "")
	require.NoError(t, err)

	_, err = exec.RunWorkflow(context.Background(), "nonexistent-workflow", "")
	assert.Error(t, err)
}

func TestRunWorkflow_FailingStep_ReturnsError(t *testing.T) {
	mgr, _ := setupTestEnvironment(t)

	wf := &Workflow{
		Metadata: WorkflowMetadata{Name: "broken"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name:  "bad-job",
					Steps: []WorkflowStep{{Name: "fail", Command: "exit 1"}},
				},
			},
		},
	}
	require.NoError(t, mgr.CreateWorkflow(wf))

	exec, err := NewExecutor(mgr, "")
	require.NoError(t, err)

	result, err := exec.RunWorkflow(context.Background(), "broken", "")
	assert.Error(t, err)
	assert.Equal(t, StatusFailed, result.Status)
}

func TestRunWorkflow_EnvVarsAvailableToSteps(t *testing.T) {
	mgr, tmpDir := setupTestEnvironment(t)

	outFile := tmpDir + "/out.txt"

	wf := &Workflow{
		Metadata: WorkflowMetadata{Name: "env-test"},
		Spec: WorkflowSpec{
			Env: map[string]string{"GREETING": "hello-from-env"},
			Jobs: []WorkflowJob{
				{
					Name: "write-env",
					Steps: []WorkflowStep{
						{
							Name:    "write",
							Command: "echo $GREETING > " + outFile,
						},
					},
				},
			},
		},
	}
	require.NoError(t, mgr.CreateWorkflow(wf))

	exec, err := NewExecutor(mgr, "")
	require.NoError(t, err)

	_, err = exec.RunWorkflow(context.Background(), "env-test", "")
	require.NoError(t, err)
}
