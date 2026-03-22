package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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

	// Determine command to execute
	var command string
	var args []string

	if step.Command != "" {
		// Execute command via shell for consistent variable substitution across platforms
		// Expand workflow variables first
		expandedCommand := e.expandVariables(step.Command)
		// Get the appropriate shell for the platform
		shellCmd, shellFlag := getShellCommand()
		command = shellCmd
		args = []string{shellFlag, expandedCommand}
	} else if step.Script != "" {
		// Execute script via shell (shell will expand variables)
		// Expand workflow variables first
		expandedScript := e.expandVariables(step.Script)
		// Get the appropriate shell for the platform
		shellCmd, shellFlag := getShellCommand()
		command = shellCmd
		args = []string{shellFlag, expandedScript}
	} else if step.Action != "" {
		// Execute predefined action
		return e.executeAction(ctx, step.Action, step.With, result)
	} else {
		result.Status = JobStatusFailed
		result.Error = "no command, script, or action specified"
		return result, fmt.Errorf("no command, script, or action specified")
	}

	// Set up command
	cmd := exec.CommandContext(ctx, command, args...)

	// Set working directory
	if step.WorkingDir != "" {
		cmd.Dir = filepath.Join(e.workingDir, step.WorkingDir)
	} else {
		cmd.Dir = e.workingDir
	}

	// Set environment variables
	cmd.Env = os.Environ()

	// Add workflow-level env vars
	for key, value := range workflow.Spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, e.expandVariables(value)))
	}

	// Add job-level env vars
	for key, value := range job.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, e.expandVariables(value)))
	}

	// Add step-level env vars
	for key, value := range step.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, e.expandVariables(value)))
	}

	// Execute command
	output, err := cmd.CombinedOutput()
	result.Output = string(output)

	endTime := time.Now()
	result.EndTime = &endTime
	result.Duration = endTime.Sub(result.StartTime)

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

		// Try to get exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
				result.ExitCode = status.ExitStatus()
			}
		}

		return result, err
	}

	result.Status = JobStatusCompleted
	result.ExitCode = 0
	return result, nil
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
