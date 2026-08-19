package workflow

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/secretsfrom"
	"github.com/cbridges1/hyve/internal/types"

	k8syaml "sigs.k8s.io/yaml"
)

// Executor handles workflow execution
type Executor struct {
	manager        *Manager
	execution      *WorkflowExecution
	currentCluster string
	variables      map[string]string
	injectedVars   map[string]string // extra vars provided by caller (--set flags or definition injection)
	hookOutputVars map[string]string // HYVE_VAR=value lines captured from step output — see recordHookOutputVars
	workingDir     string
	repoName       string

	// Output, when set, additionally receives every log line and step
	// output byte produced during execution, without affecting the CLI's
	// normal stdout/log.Printf behavior. Left nil by the CLI, which never
	// sets it.
	Output io.Writer

	// StepRunner executes each step's command/script. Defaults to
	// LocalStepRunner{} in NewExecutor — every existing call site (hyve
	// workflow run, hyve reconcile's lifecycle hooks) keeps today's exact
	// local-subprocess behavior unchanged. cmd/controller/run.go is the
	// only caller that overrides this, to KubernetesJobStepRunner.
	StepRunner StepRunner

	// DefaultWorkflowImage is the last-resort container: fallback — see
	// WorkflowJob.Container's doc comment for the full resolution order.
	// Left empty by every local-mode caller (there's nothing to fall back
	// for); cmd/controller/run.go sets it from HyveConfig.spec.defaultWorkflowImage.
	DefaultWorkflowImage string

	// AllowClientRuntime gates whether this Executor will run a
	// runtime: client workflow at all — see WorkflowSpec.Runtime. Defaults
	// to true in NewExecutor, so every direct `hyve workflow run` call site
	// (cmd/workflow) behaves as documented with no changes needed there.
	// internal/reconcile/manager.go's runWorkflows (lifecycle hooks —
	// onCreate/onDelete/etc., triggered by an automated reconcile, not a
	// human) explicitly sets this to false: a runtime: client workflow is
	// meant to run on the invoking human's machine, which doesn't exist for
	// a reconcile loop (especially in controller mode, where there's no
	// "client machine" for a controller pod to hand off to).
	AllowClientRuntime bool

	// KubeconfigLocator resolves a secretsFrom entry's Cluster name to a
	// kubeconfig file path — see secretsfrom.KubeconfigLocator. Left nil by
	// NewExecutor (internal/workflow deliberately has no dependency on
	// internal/module, to avoid an import cycle: internal/template already
	// imports internal/workflow for local-workflow-ref validation, and
	// internal/module imports internal/template). Every caller that expects
	// secretsFrom to work sets this explicitly, in practice always to
	// module.KubeconfigPathForCluster — cmd/workflow's run commands and
	// internal/reconcile/manager.go's runWorkflows both do. A workflow with
	// no secretsFrom entries never touches this field at all.
	KubeconfigLocator secretsfrom.KubeconfigLocator
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
		manager:            manager,
		currentCluster:     cluster,
		variables:          make(map[string]string),
		injectedVars:       make(map[string]string),
		hookOutputVars:     make(map[string]string),
		workingDir:         manager.localPath,
		repoName:           repoName,
		StepRunner:         LocalStepRunner{},
		AllowClientRuntime: true,
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

// recordHookOutputVars merges vars (from captureHookOutputVars) into both
// e.hookOutputVars (retrievable by the caller via HookOutputVars once the
// workflow completes) and e.variables (so a later step within the same
// workflow run sees them immediately via buildStepEnv's overlay).
func (e *Executor) recordHookOutputVars(vars map[string]string) {
	for k, v := range vars {
		e.hookOutputVars[k] = v
		e.variables[k] = v
	}
}

// HookOutputVars returns every HYVE_VAR=value the workflow's steps printed
// to their output over the course of this Executor's run — e.g. a
// beforeCreate step announcing HYVE_VPC_ID=vpc-123 for the driver's create
// operation to pick up afterward. The reconciler merges these explicitly
// into the next module.Executor.Env rather than relying on process-wide
// env, so concurrent reconciles of different clusters can't cross-
// contaminate each other's captured values (see MaxConcurrentReconciles).
func (e *Executor) HookOutputVars() map[string]string {
	return e.hookOutputVars
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

	if wf.Spec.Runtime == RuntimeClient && !e.AllowClientRuntime {
		e.execution.Status = StatusFailed
		msg := fmt.Sprintf("workflow '%s' has runtime: client, which only `hyve workflow run` may execute — an automated reconcile (lifecycle hook or controller loop) cannot run it", displayName)
		e.addLog("ERROR", "", "", msg)
		return execution, fmt.Errorf("%s", msg)
	}

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

		if err := validator.ValidateRequirements(wf.Spec.Requirements, e.variables); err != nil {
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

	if err := e.resolveSecretsFrom(ctx, wf); err != nil {
		e.execution.Status = StatusFailed
		e.addLog("ERROR", "", "", fmt.Sprintf("Failed to resolve secretsFrom: %v", err))
		return execution, fmt.Errorf("failed to resolve secretsFrom: %w", err)
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

	// KUBECONFIG (when the caller's auth step produced one) arrives via
	// InjectVars/applyInjectedVars below, not process env — reading
	// os.Getenv here would race with another cluster's concurrent reconcile
	// mutating the same process-wide variable (see MaxConcurrentReconciles).

	// Apply caller-injected variables last — highest priority, override everything above
	e.applyInjectedVars()

	return nil
}

// resolveSecretsFrom fetches every wf.Spec.SecretsFrom entry and merges the
// results into e.variables — the same "explicit per-Executor state, never
// process-wide os.Setenv" pattern applyInjectedVars/exportDefinitionEnvironmentVariables
// use, so concurrent Executors (different clusters, or a human's `hyve
// workflow run` alongside a concurrent reconcile) never share a resolved
// secret's value. Runs after setupEnvironmentVariables so a resolved
// secret's value takes priority over anything setupEnvironmentVariables
// already set for the same key — declaring secretsFrom is an explicit,
// authoritative request for that value.
func (e *Executor) resolveSecretsFrom(ctx context.Context, wf *Workflow) error {
	if len(wf.Spec.SecretsFrom) == 0 {
		return nil
	}
	if e.KubeconfigLocator == nil {
		return fmt.Errorf("workflow declares secretsFrom but this Executor has no KubeconfigLocator configured")
	}
	for _, src := range wf.Spec.SecretsFrom {
		resolved, err := secretsfrom.Resolve(ctx, e.KubeconfigLocator, src)
		if err != nil {
			return err
		}
		for k, v := range resolved {
			e.variables[k] = v
		}
	}
	return nil
}

// applyInjectedVars exports e.injectedVars (--set flags, or values the interactive
// TUI collected for spec.inputs, or KUBECONFIG/hook vars threaded explicitly
// from the reconciler) into e.variables — not the process environment, so
// concurrent Executors for different clusters never share mutable state (see
// MaxConcurrentReconciles). Idempotent — safe to call more than once per run.
func (e *Executor) applyInjectedVars() {
	for k, v := range e.injectedVars {
		e.variables[k] = v
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

// loadClusterDefinition loads a cluster definition by name, merging its
// state sidecar (driverOutputs/appliedResources) if present. Deliberately
// duplicates internal/state.Manager's read logic (read primary file,
// validate apiVersion/kind, overlay cluster-state/<name>.state.yaml)
// instead of importing internal/state directly — internal/state needs to
// import this package too (for WorkflowSource's default FileSource — see
// reconcile.StateProvider), and Go doesn't allow that cycle. Read-only,
// git-agnostic — exactly what an ad-hoc `hyve workflow run --cluster`
// lookup needs, same small cross-boundary duplication precedent used
// elsewhere in this codebase (e.g. internal/apis/hyve/v1alpha1 mirroring
// internal/types).
func (e *Executor) loadClusterDefinition(clusterName string) (*types.ClusterDefinition, error) {
	clustersDir := filepath.Join(e.manager.localPath, "clusters")
	primaryPath := filepath.Join(clustersDir, clusterName+".yaml")

	data, err := os.ReadFile(primaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cluster %s not found in clusters directory", clusterName)
		}
		return nil, err
	}

	var local struct {
		hyvev1alpha1.ClusterDefinition `json:",inline"`
	}
	if err := k8syaml.Unmarshal(data, &local); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", primaryPath, err)
	}
	def := crdconv.ToTypesClusterDefinition(&local.ClusterDefinition)

	sidecarPath := filepath.Join(filepath.Dir(clustersDir), "cluster-state", clusterName+".state.yaml")
	sdata, err := os.ReadFile(sidecarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &def, nil
		}
		return nil, err
	}
	var status hyvev1alpha1.ClusterDefinitionStatus
	if err := k8syaml.Unmarshal(sdata, &status); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", sidecarPath, err)
	}
	def.Spec.DriverOutputs = status.DriverOutputs
	def.Spec.AppliedResources = crdconv.ToTypesAppliedResources(status.AppliedResources)
	return &def, nil
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
