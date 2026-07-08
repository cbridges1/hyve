package workflow

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/types"
)

// Executor handles workflow execution
type Executor struct {
	manager        *Manager
	execution      *WorkflowExecution
	currentCluster string
	variables      map[string]string
	injectedVars   map[string]string // extra vars provided by caller (--set flags or definition injection)
	workingDir     string
	repoName       string

	// Output, when set, additionally receives every log line and step
	// output byte produced during execution — used by hyve-server to
	// capture live progress for polling/WebSocket streaming without
	// affecting the CLI's normal stdout/log.Printf behavior. Left nil by
	// the CLI, which never sets it.
	Output io.Writer
}

// NewExecutor creates a new workflow executor.
//
// The cluster argument identifies the cluster the workflow is associated with
// (it is set as WORKFLOW_CLUSTER and can be referenced by the workflow).
// In the module-based architecture the executor does NOT auto-sync kubeconfigs
// or contact cloud providers — that is the responsibility of the calling code
// (typically the module's auth operation).
func NewExecutor(manager *Manager, cluster string) (*Executor, error) {
	var repoName string
	execRepoMgr, repoErr := repository.NewManager()
	if repoErr == nil {
		defer execRepoMgr.Close()
		if currentRepo, err := execRepoMgr.GetCurrentRepository(); err == nil {
			repoName = currentRepo.Name
		}
	}

	return &Executor{
		manager:        manager,
		currentCluster: cluster,
		variables:      make(map[string]string),
		injectedVars:   make(map[string]string),
		workingDir:     manager.localPath,
		repoName:       repoName,
	}, nil
}

// InjectVars pre-loads key/value pairs into the executor's variable set.
// These are applied with highest priority — after all automatically derived
// variables — so they can override anything.
func (e *Executor) InjectVars(vars map[string]string) {
	for k, v := range vars {
		e.injectedVars[k] = v
	}
}

// RunWorkflowNoCluster is retained for API compatibility with the reconciler.
// Definition env vars are injected if a clusterDef is supplied.
func (e *Executor) RunWorkflowNoCluster(ctx context.Context, workflowName string, clusterDef *types.ClusterDefinition) (*WorkflowExecution, error) {
	if clusterDef != nil {
		e.exportDefinitionEnvironmentVariables(clusterDef)
	}
	return e.RunWorkflow(ctx, workflowName, "")
}

// RunWorkflow executes a workflow, looking it up by name in the local
// workflows/ directory first.
func (e *Executor) RunWorkflow(ctx context.Context, workflowName string, cluster string) (*WorkflowExecution, error) {
	wf, err := e.manager.GetWorkflow(workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}
	return e.runWorkflow(ctx, wf, workflowName, cluster)
}

// RunResolvedWorkflow executes an already-loaded workflow definition (e.g.
// one fetched from a remote source via internal/workflowref) without going
// through the local workflows/ directory lookup RunWorkflow performs.
// displayName is used for logging/execution IDs/env vars only — typically
// wf.Metadata.Name or the workflow's full remote source string.
func (e *Executor) RunResolvedWorkflow(ctx context.Context, wf *Workflow, displayName, cluster string) (*WorkflowExecution, error) {
	return e.runWorkflow(ctx, wf, displayName, cluster)
}

// runWorkflow is the shared body of RunWorkflow/RunResolvedWorkflow.
func (e *Executor) runWorkflow(ctx context.Context, wf *Workflow, displayName string, cluster string) (*WorkflowExecution, error) {
	targetCluster := cluster
	if targetCluster == "" {
		targetCluster = e.currentCluster
	}

	execution := &WorkflowExecution{
		ID:           generateExecutionID(),
		WorkflowName: displayName,
		Cluster:      targetCluster,
		Status:       StatusRunning,
		StartTime:    time.Now(),
		Trigger:      "manual",
		JobResults:   make(map[string]*JobResult),
		Logs:         []WorkflowLogEntry{},
		Variables:    make(map[string]string),
	}

	e.execution = execution
	e.addLog("INFO", "", "", fmt.Sprintf("Starting workflow '%s'", displayName))

	// Apply caller-injected variables (--set flags, or values the interactive TUI
	// collected for spec.inputs) before requirements validation, so a --set/prompted
	// value can satisfy a spec.requirements.secrets entry of the same name.
	// Previously this only happened in setupEnvironmentVariables, which runs after
	// validation — meaning no --set or prompted value could ever satisfy a
	// requirements.secrets check, only a variable already present in the process
	// environment before hyve was invoked at all.
	e.applyInjectedVars()

	// Validate workflow requirements
	if wf.Spec.Requirements != nil {
		e.addLog("INFO", "", "", "Validating workflow requirements...")
		validator, err := NewRequirementValidator()
		if err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Failed to create requirement validator: %v", err))
			return execution, fmt.Errorf("failed to create requirement validator: %w", err)
		}
		defer validator.Close()

		if err := validator.ValidateRequirements(wf.Spec.Requirements); err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Requirements validation failed: %v", err))
			return execution, fmt.Errorf("requirements validation failed: %w", err)
		}

		if err := validator.LoadSecretsIntoEnvironment(wf.Spec.Requirements); err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Failed to load secrets: %v", err))
			return execution, fmt.Errorf("failed to load secrets: %w", err)
		}

		e.addLog("INFO", "", "", "✅ All requirements validated successfully")
	}

	// Inject cluster definition env vars when a target cluster is known.
	// The KUBECONFIG environment is left to the caller (or module's auth op).
	if targetCluster != "" {
		if clusterDef, defErr := e.loadClusterDefinition(targetCluster); defErr == nil {
			e.exportDefinitionEnvironmentVariables(clusterDef)
		} else {
			e.addLog("WARN", "", "", fmt.Sprintf("Could not load cluster definition for env injection: %v", defErr))
		}
	}

	if err := e.setupEnvironmentVariables(wf); err != nil {
		e.execution.Status = StatusFailed
		e.addLog("ERROR", "", "", fmt.Sprintf("Failed to setup environment variables: %v", err))
		return execution, fmt.Errorf("failed to setup environment variables: %w", err)
	}

	if err := e.validateInputs(wf); err != nil {
		e.execution.Status = StatusFailed
		e.addLog("ERROR", "", "", err.Error())
		return execution, err
	}

	if err := e.executeJobs(ctx, wf); err != nil {
		e.execution.Status = StatusFailed
		e.addLog("ERROR", "", "", fmt.Sprintf("Workflow failed: %v", err))
		e.finalizeExecution()
		return execution, fmt.Errorf("workflow execution failed: %w", err)
	}

	e.execution.Status = StatusCompleted
	e.addLog("INFO", "", "", "Workflow completed successfully")
	e.finalizeExecution()

	return execution, nil
}

func (e *Executor) setupEnvironmentVariables(workflow *Workflow) error {
	e.variables["WORKFLOW_NAME"] = workflow.Metadata.Name
	e.variables["WORKFLOW_CLUSTER"] = e.currentCluster
	e.variables["WORKFLOW_EXECUTION_ID"] = e.execution.ID
	e.variables["HYVE_REPOSITORY"] = e.repoName
	e.variables["HYVE_REPOSITORY_PATH"] = e.manager.localPath

	// Honour KUBECONFIG from the caller's environment.
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		e.variables["KUBECONFIG"] = kc
	}

	// Apply caller-injected variables last — highest priority, override everything above
	e.applyInjectedVars()

	return nil
}

// applyInjectedVars exports e.injectedVars (--set flags, or values the interactive
// TUI collected for spec.inputs) into both e.variables and the process
// environment. Idempotent — safe to call more than once per run.
func (e *Executor) applyInjectedVars() {
	for k, v := range e.injectedVars {
		e.variables[k] = v
		os.Setenv(k, v)
	}
}

// exportDefinitionEnvironmentVariables injects HYVE_* variables derived from a cluster
// definition into the executor's variable set.
func (e *Executor) exportDefinitionEnvironmentVariables(clusterDef *types.ClusterDefinition) {
	setEnv := func(key, value string) {
		if value == "" {
			return
		}
		e.variables[key] = value
		os.Setenv(key, value)
	}

	setEnv("HYVE_CLUSTER_NAME", clusterDef.Metadata.Name)
	setEnv("HYVE_CLUSTER_REGION", clusterDef.Metadata.Region)
	setEnv("HYVE_DRIVER_SOURCE", clusterDef.Spec.Driver.Source)
	setEnv("HYVE_DRIVER_VERSION", clusterDef.Spec.Driver.Version)

	// Flatten params into HYVE_PARAM_<KEY>
	for k, v := range clusterDef.Spec.Params {
		setEnv("HYVE_PARAM_"+strings.ToUpper(k), v)
	}
	// Pass through previously captured driver outputs.
	for k, v := range clusterDef.Spec.DriverOutputs {
		setEnv(k, v)
	}
}

// validateInputs checks that every input declared in the workflow's spec.inputs section
// has a value — either in the executor's variable set or already in the process environment.
func (e *Executor) validateInputs(wf *Workflow) error {
	if len(wf.Spec.Inputs) == 0 {
		return nil
	}

	var missing []string
	for _, input := range wf.Spec.Inputs {
		if e.variables[input.Name] == "" && os.Getenv(input.Name) == "" {
			label := input.Name
			if input.Description != "" {
				label += " (" + input.Description + ")"
			}
			missing = append(missing, label)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	return fmt.Errorf("workflow requires the following inputs that are not set:\n  - %s\nUse --set KEY=VALUE to provide them, or run interactively for a prompt",
		strings.Join(missing, "\n  - "))
}

// loadClusterDefinition loads a cluster definition from YAML file
func (e *Executor) loadClusterDefinition(clusterName string) (*types.ClusterDefinition, error) {
	clustersDir := filepath.Join(e.manager.localPath, "clusters")
	var clusterDef *types.ClusterDefinition

	if _, err := os.Stat(clustersDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("clusters directory not found at %s", clustersDir)
	}

	err := filepath.WalkDir(clustersDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		var cluster types.ClusterDefinition
		if err := yaml.Unmarshal(data, &cluster); err != nil {
			return fmt.Errorf("failed to unmarshal cluster definition from %s: %w", path, err)
		}

		if cluster.Metadata.Name == clusterName {
			clusterDef = &cluster
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if clusterDef == nil {
		return nil, fmt.Errorf("cluster %s not found in clusters directory", clusterName)
	}

	return clusterDef, nil
}

// expandVariables expands variables in a string
func (e *Executor) expandVariables(input string) string {
	result := input
	for key, value := range e.variables {
		result = strings.ReplaceAll(result, fmt.Sprintf("${%s}", key), value)
		result = strings.ReplaceAll(result, fmt.Sprintf("$%s", key), value)
	}
	return result
}

// addLog adds a log entry to the execution
func (e *Executor) addLog(level, job, step, message string) {
	entry := WorkflowLogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Job:       job,
		Step:      step,
		Message:   message,
	}

	e.execution.Logs = append(e.execution.Logs, entry)

	prefix := fmt.Sprintf("[%s]", level)
	if e.currentCluster != "" {
		prefix += fmt.Sprintf("[%s]", e.currentCluster)
	}
	if job != "" {
		prefix += fmt.Sprintf("[%s]", job)
	}
	if step != "" {
		prefix += fmt.Sprintf("[%s]", step)
	}
	log.Printf("%s %s", prefix, message)
	if e.Output != nil {
		fmt.Fprintf(e.Output, "%s %s\n", prefix, message)
	}
}

// finalizeExecution finalizes the workflow execution
func (e *Executor) finalizeExecution() {
	endTime := time.Now()
	e.execution.EndTime = &endTime
	e.execution.Duration = endTime.Sub(e.execution.StartTime)
}

// generateExecutionID generates a unique execution ID
func generateExecutionID() string {
	return fmt.Sprintf("exec_%d", time.Now().Unix())
}

// Close closes the executor and cleans up resources.
func (e *Executor) Close() error {
	return nil
}
