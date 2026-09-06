package reconcile

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// maxAdHocWorkflowOutputBytes caps how much captured step output
// RunAdHocWorkflow returns — this ends up written into a WorkflowRun CR's
// status.output field (etcd-backed, not a log stream), so it must stay
// bounded regardless of how verbose a workflow's steps are.
const maxAdHocWorkflowOutputBytes = 256 << 10 // 256KiB

// RunAdHocWorkflow executes exactly one workflow against an existing,
// already-provisioned cluster — the primitive behind cluster mode's
// WorkflowRun CRD (see internal/controller/workflowrun_reconciler.go) and
// local mode's own `hyve workflow run --cluster`. Unlike ReconcileOne/
// reconcileCluster, this never touches cluster lifecycle at all (no create/
// delete/scale/resource reconciliation) — it only authenticates (to get
// KUBECONFIG) and runs the one named workflow, mirroring the ACTIVE-branch
// reconcile's own auth-then-workflow sequence in reconcileCluster.
//
// adHocParams, when non-empty, overlays cluster.Spec.Params (adHocParams
// wins on conflict) before HYVE_PARAM_* env is computed — this is what lets
// a caller pass one-off values without mutating the ClusterDefinition
// itself.
//
// Output is captured into its own local buffer rather than r.Logger:
// r.Logger is a single field shared across this whole Reconciler, and
// controller mode reconciles many ClusterDefinitions concurrently
// (MaxConcurrentReconciles) against the *same* Reconciler instance —
// writing ad hoc workflow output there would race with, and interleave
// into, unrelated clusters' reconcile logs. The returned output string is
// always populated (even on error, best-effort) so a caller can surface
// whatever ran before the failure.
func (r *Reconciler) RunAdHocWorkflow(ctx context.Context, cluster types.ClusterDefinition, ref types.WorkflowRef, adHocParams map[string]string, lf *module.LockFile, secretsEnv map[string]string) (string, error) {
	if len(adHocParams) > 0 {
		merged := make(map[string]string, len(cluster.Spec.Params)+len(adHocParams))
		for k, v := range cluster.Spec.Params {
			merged[k] = v
		}
		for k, v := range adHocParams {
			merged[k] = v
		}
		cluster.Spec.Params = merged
	}

	name := cluster.Metadata.Name
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	resolved, err := module.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, r.stateMgr.LocalPath())
	if err != nil {
		return "", fmt.Errorf("resolve module: %w", err)
	}

	env := buildModuleEnv(cluster, secretsEnv)
	exec := &module.Executor{
		ModuleDir:   resolved.Dir,
		Env:         env,
		WorkDir:     r.stateMgr.LocalPath(),
		ClusterName: name,
		Runner:      r.ModuleRunner,
		Image:       r.moduleImage(cluster),
	}

	authResult, authErr := exec.Execute(ctx, module.OperationAuth)
	if authErr != nil {
		return "", fmt.Errorf("auth failed: %w", authErr)
	}
	if kc := authResult.Outputs["KUBECONFIG"]; kc != "" {
		env = append(env, "KUBECONFIG="+kc)
	}
	r.dedupeKubeconfigAfterAuth(name, authResult.Outputs["KUBECONFIG"])

	wfMgr, err := workflow.NewManagerWithSource(r.stateMgr.LocalPath(), r.stateMgr.WorkflowSource())
	if err != nil {
		return "", fmt.Errorf("create workflow manager: %w", err)
	}

	injected := make(map[string]string, len(env))
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			injected[kv[:idx]] = kv[idx+1:]
		}
	}

	executor, err := workflow.NewExecutor(wfMgr, "")
	if err != nil {
		return "", fmt.Errorf("create workflow executor: %w", err)
	}
	defer executor.Close()

	var buf bytes.Buffer
	executor.Output = &buf
	if r.StepRunner != nil {
		executor.StepRunner = r.StepRunner
	}
	executor.DefaultWorkflowImage = r.DefaultWorkflowImage
	// Same reasoning as runWorkflows' lifecycle-hook call: a `runtime:
	// client` workflow step is meant to run on the invoking human's own
	// machine, which doesn't exist here either — a WorkflowRun executes
	// inside the controller pod (dispatched by an API request), not on the
	// CLI process that issued it. Only local mode's own direct `hyve
	// workflow run` (cmd/workflow, executor.AllowClientRuntime left at
	// NewExecutor's true default) actually has an invoking machine to run
	// runtime: client on.
	executor.AllowClientRuntime = false
	executor.KubeconfigLocator = module.KubeconfigPathForCluster
	executor.InjectVars(injected)

	var execution *workflow.WorkflowExecution
	var runErr error
	if !ref.IsRemote() {
		execution, runErr = executor.RunWorkflow(ctx, ref.Name, "")
	} else {
		execution, runErr = r.runRemoteWorkflowHook(ctx, executor, ref, lf, envValue(env, "GITHUB_TOKEN"))
	}

	output := buf.String()
	if len(output) > maxAdHocWorkflowOutputBytes {
		output = output[:maxAdHocWorkflowOutputBytes] + "\n... (truncated)"
	}

	if runErr != nil {
		return output, fmt.Errorf("workflow failed: %w", runErr)
	}
	if execution.Status != workflow.StatusCompleted {
		return output, fmt.Errorf("workflow finished with status: %s", execution.Status)
	}
	return output, nil
}
