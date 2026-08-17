package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// executeJobs executes all jobs in the workflow
func (e *Executor) executeJobs(ctx context.Context, workflow *Workflow) error {
	// Build dependency graph
	jobDeps := make(map[string][]string)
	for _, job := range workflow.Spec.Jobs {
		jobDeps[job.Name] = job.DependsOn
	}

	// Execute jobs in dependency order
	completed := make(map[string]bool)

	for len(completed) < len(workflow.Spec.Jobs) {
		progress := false

		for _, job := range workflow.Spec.Jobs {
			if completed[job.Name] {
				continue
			}

			// Check if all dependencies are completed
			canRun := true
			for _, dep := range job.DependsOn {
				if !completed[dep] {
					canRun = false
					break
				}
			}

			if !canRun {
				continue
			}

			// Execute job
			e.addLog("INFO", job.Name, "", fmt.Sprintf("Starting job '%s'", job.Name))

			jobResult, err := e.executeJob(ctx, &job, workflow)
			e.execution.JobResults[job.Name] = jobResult

			if err != nil {
				e.addLog("ERROR", job.Name, "", fmt.Sprintf("Job failed: %v", err))
				return fmt.Errorf("job '%s' failed: %w", job.Name, err)
			}

			completed[job.Name] = true
			progress = true
			e.addLog("INFO", job.Name, "", fmt.Sprintf("Job '%s' completed successfully", job.Name))
		}

		if !progress {
			return fmt.Errorf("circular dependency detected in jobs")
		}
	}

	return nil
}

// executeJob executes a single job
func (e *Executor) executeJob(ctx context.Context, job *WorkflowJob, workflow *Workflow) (*JobResult, error) {
	result := &JobResult{
		Status:    JobStatusRunning,
		StartTime: time.Now(),
		Steps:     make(map[string]*StepResult),
	}

	// Check job condition
	if job.If != "" {
		shouldRun, err := e.evaluateCondition(job.If)
		if err != nil {
			result.Status = JobStatusFailed
			result.Error = fmt.Sprintf("failed to evaluate condition: %v", err)
			return result, err
		}
		if !shouldRun {
			result.Status = JobStatusSkipped
			e.addLog("INFO", job.Name, "", "Job skipped due to condition")
			return result, nil
		}
	}

	// Set job-specific cluster if specified
	targetCluster := job.Cluster
	if targetCluster == "" {
		targetCluster = e.currentCluster
	}

	// Execute steps
	for _, step := range job.Steps {
		e.addLog("INFO", job.Name, step.Name, fmt.Sprintf("Starting step '%s'", step.Name))

		stepResult, err := e.executeStep(ctx, &step, job, workflow, targetCluster)
		result.Steps[step.Name] = stepResult

		if err != nil && !step.ContinueOnError {
			result.Status = JobStatusFailed
			result.Error = fmt.Sprintf("step '%s' failed: %v", step.Name, err)
			endTime := time.Now()
			result.EndTime = &endTime
			result.Duration = endTime.Sub(result.StartTime)
			e.addLog("ERROR", job.Name, step.Name, fmt.Sprintf("Step failed: %v", err))
			return result, err
		} else if err != nil {
			e.addLog("WARN", job.Name, step.Name, fmt.Sprintf("Step failed but continuing: %v", err))
		}

		e.addLog("INFO", job.Name, step.Name, fmt.Sprintf("Step '%s' completed", step.Name))
	}

	result.Status = JobStatusCompleted
	endTime := time.Now()
	result.EndTime = &endTime
	result.Duration = endTime.Sub(result.StartTime)

	return result, nil
}

// executeStep executes a single step
func (e *Executor) executeStep(ctx context.Context, step *WorkflowStep, job *WorkflowJob, workflow *Workflow, cluster string) (*StepResult, error) {
	result := &StepResult{
		Status:    JobStatusRunning,
		StartTime: time.Now(),
	}

	// Check step condition
	if step.If != "" {
		shouldRun, err := e.evaluateCondition(step.If)
		if err != nil {
			result.Status = JobStatusFailed
			result.Error = fmt.Sprintf("failed to evaluate condition: %v", err)
			return result, err
		}
		if !shouldRun {
			result.Status = JobStatusSkipped
			return result, nil
		}
	}

	// step.Action bypasses StepRunner entirely — it's a small, fixed set of
	// hyve-defined kubectl wrappers, always run locally regardless of which
	// StepRunner is selected (there's no meaningful "run this action as a
	// Kubernetes Job" translation for it).
	if step.Command == "" && step.Script == "" {
		if step.Action != "" {
			return e.executeAction(ctx, step.Action, step.With, result)
		}
		result.Status = JobStatusFailed
		result.Error = "no command, script, or action specified"
		return result, fmt.Errorf("no command, script, or action specified")
	}

	// Expand workflow variables in the command/script text itself before
	// handing it to StepRunner — variable expansion is Executor's own
	// concern (it owns e.variables), not something every StepRunner
	// implementation should have to reimplement.
	resolvedStep := *step
	resolvedStep.Command = e.expandVariables(step.Command)
	resolvedStep.Script = e.expandVariables(step.Script)

	// A runtime: client workflow always runs as a local subprocess,
	// regardless of e.StepRunner — the second of two independent paths to
	// LocalStepRunner (the other being plain CLI/local mode). This is also
	// what makes container: naturally a no-op rather than a validation
	// error for this runtime: LocalStepRunner.RequiresContainer() is always
	// false, so the pre-flight check below never fires here even if
	// container: is set — same "informational, ignored" treatment it
	// already gets under plain CLI/local mode, no special-casing needed.
	runner := e.StepRunner
	if workflow.Spec.Runtime == RuntimeClient {
		runner = LocalStepRunner{}
	}

	// Resolution order: per-step container: -> per-job container: ->
	// HyveConfig.spec.defaultWorkflowImage (Executor.DefaultWorkflowImage,
	// empty for every local-mode caller) -> hard failure, but ONLY when
	// the selected runner actually needs one — container: is
	// meaningless, and ignored rather than validated, under LocalStepRunner.
	resolvedStep.Container = step.Container
	if resolvedStep.Container == "" {
		resolvedStep.Container = job.Container
	}
	if resolvedStep.Container == "" {
		resolvedStep.Container = e.DefaultWorkflowImage
	}
	if runner.RequiresContainer() && resolvedStep.Container == "" {
		result.Status = JobStatusFailed
		result.Error = fmt.Sprintf("step %q resolved to no container image — set container: on the step, its job, or configure HyveConfig.spec.defaultWorkflowImage", step.Name)
		return result, fmt.Errorf("%s", result.Error)
	}

	workingDir := e.workingDir
	if step.WorkingDir != "" {
		workingDir = filepath.Join(e.workingDir, step.WorkingDir)
	}

	env := e.buildStepEnv(workflow, job, step)

	stdout, _, exitCode, runErr := runner.RunStep(ctx, resolvedStep, env, workingDir, e.Output)
	result.Output = stdout
	result.ExitCode = exitCode

	endTime := time.Now()
	result.EndTime = &endTime
	result.Duration = endTime.Sub(result.StartTime)

	// Capture any HYVE_VAR=value lines printed by the step so lifecycle-hook
	// handlers can read them after the workflow ends (see HookOutputVars).
	e.recordHookOutputVars(captureHookOutputVars(stdout))

	if runErr != nil {
		result.Status = JobStatusFailed
		result.Error = runErr.Error()
		return result, runErr
	}

	result.Status = JobStatusCompleted
	return result, nil
}

// buildStepEnv layers workflow/job/step-level env vars onto the inherited
// process environment plus this Executor's own e.variables (definition/
// injected vars, KUBECONFIG, captured hook outputs — see
// exportDefinitionEnvironmentVariables, applyInjectedVars,
// recordHookOutputVars), add-if-absent (e.variables always wins over
// workflow/job/step spec.env; those are fallback defaults for a variable
// that isn't otherwise present, not overrides). e.variables is layered
// explicitly here, not via os.Environ(), specifically so two clusters'
// Executors running concurrently (see MaxConcurrentReconciles) never share
// mutable env state — each Executor only ever contributes its own
// variables to its own steps' subprocess env. A duplicate KEY= entry later
// in the slice silently blanks out an earlier real value (confirmed: the
// shell resolves duplicate env entries to the last one), so a workflow
// declaring e.g. `PANGOLIN_ENDPOINT: ""` purely to self-document an
// expected external input would otherwise wipe out the real value a caller
// had already exported. Extracted from executeStep's prior inline
// cmd.Env-building so it's identical regardless of which StepRunner
// ultimately consumes it.
func (e *Executor) buildStepEnv(workflow *Workflow, job *WorkflowJob, step *WorkflowStep) []string {
	env := os.Environ()
	envPresent := make(map[string]bool, len(env)+len(e.variables))
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			envPresent[kv[:idx]] = true
		}
	}
	for k, v := range e.variables {
		env = append(env, k+"="+v)
		envPresent[k] = true
	}
	addDefaultEnv := func(key, value string) {
		if envPresent[key] {
			return
		}
		env = append(env, fmt.Sprintf("%s=%s", key, e.expandVariables(value)))
		envPresent[key] = true
	}

	for key, value := range workflow.Spec.Env {
		addDefaultEnv(key, value)
	}
	for key, value := range job.Env {
		addDefaultEnv(key, value)
	}
	for key, value := range step.Env {
		addDefaultEnv(key, value)
	}
	return env
}

// executeAction executes a predefined action
func (e *Executor) executeAction(ctx context.Context, action string, params map[string]string, result *StepResult) (*StepResult, error) {
	// Expand variables in all parameters
	expandedParams := make(map[string]string)
	for key, value := range params {
		expandedParams[key] = e.expandVariables(value)
	}

	switch action {
	case "kubectl-apply":
		file := expandedParams["file"]
		if file == "" {
			result.Status = JobStatusFailed
			result.Error = "kubectl-apply action requires 'file' parameter"
			return result, fmt.Errorf("kubectl-apply action requires 'file' parameter")
		}
		cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", file)
		cmd.Dir = e.workingDir
		cmd.Env = os.Environ()
		output, err := cmd.CombinedOutput()
		result.Output = string(output)

		// Print command output to user
		if len(output) > 0 {
			fmt.Print(string(output))
			if !strings.HasSuffix(string(output), "\n") {
				fmt.Println()
			}
		}

		if err != nil {
			result.Status = JobStatusFailed
			result.Error = err.Error()
			return result, err
		}
		result.Status = JobStatusCompleted
		return result, nil

	case "kubectl-delete":
		file := expandedParams["file"]
		if file == "" {
			result.Status = JobStatusFailed
			result.Error = "kubectl-delete action requires 'file' parameter"
			return result, fmt.Errorf("kubectl-delete action requires 'file' parameter")
		}
		cmd := exec.CommandContext(ctx, "kubectl", "delete", "-f", file)
		cmd.Dir = e.workingDir
		cmd.Env = os.Environ()
		output, err := cmd.CombinedOutput()
		result.Output = string(output)

		// Print command output to user
		if len(output) > 0 {
			fmt.Print(string(output))
			if !strings.HasSuffix(string(output), "\n") {
				fmt.Println()
			}
		}

		if err != nil {
			result.Status = JobStatusFailed
			result.Error = err.Error()
			return result, err
		}
		result.Status = JobStatusCompleted
		return result, nil

	default:
		result.Status = JobStatusFailed
		result.Error = fmt.Sprintf("unknown action: %s", action)
		return result, fmt.Errorf("unknown action: %s", action)
	}
}

// evaluateCondition evaluates a condition string
func (e *Executor) evaluateCondition(condition string) (bool, error) {
	// Simple condition evaluation - can be extended
	// For now, just support basic variable checks
	expanded := e.expandVariables(condition)
	return expanded == "true", nil
}

// getShellCommand returns the appropriate shell command and flag for the current platform
func getShellCommand() (string, string) {
	if runtime.GOOS == "windows" {
		// On Windows, use cmd.exe
		return "cmd", "/C"
	}
	// On Unix-like systems (Linux, macOS, etc.), use sh
	return "sh", "-c"
}

// captureHookOutputVars scans step output for lines of the form HYVE_VAR=value
// and returns them. This lets beforeCreate workflow steps communicate
// resource IDs (VPC ID, role names, etc.) to later steps in the same
// Executor and, via HookOutputVars, to the reconciler once the workflow
// completes — see recordHookOutputVars.
func captureHookOutputVars(output string) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "HYVE_") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := line[:idx]
		value := strings.TrimSpace(line[idx+1:])
		vars[key] = value
	}
	return vars
}
