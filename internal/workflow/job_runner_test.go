package workflow

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// envHas returns true if env (a "KEY=VALUE" slice, as built by buildStepEnv)
// contains key=value exactly.
func envHas(env []string, key, value string) bool {
	for _, kv := range env {
		if kv == key+"="+value {
			return true
		}
	}
	return false
}

// TestBuildStepEnv_UsesExecutorVariables_NotProcessEnv is the direct
// regression test for the MaxConcurrentReconciles fix: an Executor's own
// e.variables (definition/injected vars, KUBECONFIG, hook outputs) must
// reach a step's subprocess env without ever touching os.Setenv — otherwise
// two clusters reconciling concurrently could see each other's values.
func TestBuildStepEnv_UsesExecutorVariables_NotProcessEnv(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["KUBECONFIG"] = "/isolated/path/to/my-cluster.yaml"
	exec.variables["HYVE_CLUSTER_NAME"] = "my-cluster"

	wf := &Workflow{Spec: WorkflowSpec{}}
	job := &WorkflowJob{}
	step := &WorkflowStep{}

	env := exec.buildStepEnv(wf, job, step)

	assert.True(t, envHas(env, "KUBECONFIG", "/isolated/path/to/my-cluster.yaml"))
	assert.True(t, envHas(env, "HYVE_CLUSTER_NAME", "my-cluster"))
	assert.Empty(t, os.Getenv("KUBECONFIG"), "buildStepEnv must never mutate process-wide env as a side effect")
}

// TestBuildStepEnv_ExecutorVariablesOverrideSpecEnvDefaults preserves the
// documented precedence: workflow/job/step spec.env are fallback defaults,
// never overrides, for a variable the executor already has a real value
// for.
func TestBuildStepEnv_ExecutorVariablesOverrideSpecEnvDefaults(t *testing.T) {
	exec, _ := setupExecutor(t)
	exec.variables["PANGOLIN_ENDPOINT"] = "https://real-endpoint.example.com"

	wf := &Workflow{Spec: WorkflowSpec{Env: map[string]string{"PANGOLIN_ENDPOINT": ""}}}
	job := &WorkflowJob{}
	step := &WorkflowStep{}

	env := exec.buildStepEnv(wf, job, step)

	assert.True(t, envHas(env, "PANGOLIN_ENDPOINT", "https://real-endpoint.example.com"))
}

// TestBuildStepEnv_SpecEnvFallsBackWhenNotSet confirms spec.env still
// applies when the executor has no value of its own for that key.
func TestBuildStepEnv_SpecEnvFallsBackWhenNotSet(t *testing.T) {
	exec, _ := setupExecutor(t)

	wf := &Workflow{Spec: WorkflowSpec{Env: map[string]string{"WORKFLOW_LEVEL": "wf-value"}}}
	job := &WorkflowJob{Env: map[string]string{"JOB_LEVEL": "job-value"}}
	step := &WorkflowStep{Env: map[string]string{"STEP_LEVEL": "step-value"}}

	env := exec.buildStepEnv(wf, job, step)

	assert.True(t, envHas(env, "WORKFLOW_LEVEL", "wf-value"))
	assert.True(t, envHas(env, "JOB_LEVEL", "job-value"))
	assert.True(t, envHas(env, "STEP_LEVEL", "step-value"))
}

func TestCaptureHookOutputVars_ParsesHyvePrefixedLines(t *testing.T) {
	vars := captureHookOutputVars("some log line\nHYVE_VPC_ID=vpc-123\nnot-a-var\nHYVE_ROLE_NAME=my-role\n")
	assert.Equal(t, map[string]string{"HYVE_VPC_ID": "vpc-123", "HYVE_ROLE_NAME": "my-role"}, vars)
}

func TestCaptureHookOutputVars_IgnoresNonHyveLines(t *testing.T) {
	vars := captureHookOutputVars("PATH=/usr/bin\nFOO=bar\n")
	assert.Empty(t, vars)
}

// TestRecordHookOutputVars_VisibleToLaterStepsAndCaller proves captured
// HYVE_VAR output both (a) becomes visible to a later step in the same
// Executor via e.variables (no os.Setenv needed) and (b) is retrievable by
// the caller afterward via HookOutputVars, for reconcile/manager.go to
// merge explicitly into the next module.Executor's env.
func TestRecordHookOutputVars_VisibleToLaterStepsAndCaller(t *testing.T) {
	exec, _ := setupExecutor(t)

	exec.recordHookOutputVars(captureHookOutputVars("HYVE_VPC_ID=vpc-123\n"))

	assert.Equal(t, "vpc-123", exec.variables["HYVE_VPC_ID"])
	assert.Equal(t, map[string]string{"HYVE_VPC_ID": "vpc-123"}, exec.HookOutputVars())

	env := exec.buildStepEnv(&Workflow{Spec: WorkflowSpec{}}, &WorkflowJob{}, &WorkflowStep{})
	assert.True(t, envHas(env, "HYVE_VPC_ID", "vpc-123"))
}

// TestTwoExecutors_ConcurrentEnv_NeverCrossContaminate is the closest unit
// test to the real MaxConcurrentReconciles scenario: two Executors (one per
// cluster) building step env "concurrently" (interleaved here) must never
// see each other's variables, since e.variables is per-instance, not
// process-global.
func TestTwoExecutors_ConcurrentEnv_NeverCrossContaminate(t *testing.T) {
	execA, _ := setupExecutor(t)
	execA.variables["KUBECONFIG"] = "/a/kubeconfig.yaml"
	execA.variables["HYVE_CLUSTER_NAME"] = "cluster-a"

	execB, _ := setupExecutor(t)
	execB.variables["KUBECONFIG"] = "/b/kubeconfig.yaml"
	execB.variables["HYVE_CLUSTER_NAME"] = "cluster-b"

	wf := &Workflow{Spec: WorkflowSpec{}}
	job := &WorkflowJob{}
	step := &WorkflowStep{}

	envA := execA.buildStepEnv(wf, job, step)
	envB := execB.buildStepEnv(wf, job, step)

	assert.True(t, envHas(envA, "KUBECONFIG", "/a/kubeconfig.yaml"))
	assert.True(t, envHas(envA, "HYVE_CLUSTER_NAME", "cluster-a"))
	assert.False(t, envHas(envA, "KUBECONFIG", "/b/kubeconfig.yaml"))

	assert.True(t, envHas(envB, "KUBECONFIG", "/b/kubeconfig.yaml"))
	assert.True(t, envHas(envB, "HYVE_CLUSTER_NAME", "cluster-b"))
	assert.False(t, envHas(envB, "KUBECONFIG", "/a/kubeconfig.yaml"))
}
