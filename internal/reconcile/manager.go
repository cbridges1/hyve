package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/kubeconfig"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
	"github.com/cbridges1/hyve/internal/workflowref"
)

// Reconciler orchestrates cluster lifecycle by delegating all cloud operations
// to the module identified by each cluster's spec.driver.
type Reconciler struct {
	stateMgr StateProvider

	// Logger, when set, additionally receives every progress line logged
	// during a reconcile run, without affecting the CLI's normal
	// stdout/log.Printf behavior. Left nil by the CLI, which never sets it.
	Logger io.Writer

	// StepRunner, when set, is propagated onto every workflow.Executor this
	// Reconciler creates for lifecycle-hook workflows (onCreate/onDelete/
	// etc. — see runWorkflows). Left nil by the CLI, which lets
	// workflow.NewExecutor's own default (LocalStepRunner) apply — zero
	// behavior change for local/CLI mode. cmd/controller/run.go is the
	// only caller that sets this, to a *workflow.KubernetesJobStepRunner.
	StepRunner workflow.StepRunner

	// DefaultWorkflowImage is propagated the same way — see
	// workflow.Executor.DefaultWorkflowImage's doc comment for the
	// container: resolution order it participates in. Left empty by the
	// CLI; cmd/controller/run.go sets it from HyveConfig.spec.defaultWorkflowImage.
	DefaultWorkflowImage string

	// ModuleRunner, when set, is propagated onto every module.Executor this
	// Reconciler creates — cluster mode's equivalent of StepRunner, moving
	// create/status/delete/auth execution from an inline os/exec child
	// process to a fresh per-execution Kubernetes Job. Left nil by the CLI,
	// which leaves module.Executor's default (Runner == nil, today's inline
	// path) untouched — zero behavior change for local/CLI mode.
	// cmd/controller/run.go is the only caller that sets this.
	ModuleRunner *module.JobRunner

	// DefaultModuleImage is the image a module.Executor falls back to when
	// its module has no spec.runner.image of its own (see
	// HyveConfigSpec.DefaultModuleImage's doc comment for the two-tier
	// resolution order). Only consulted when ModuleRunner != nil. Left
	// empty by the CLI; cmd/controller/run.go sets it from
	// HyveConfig.spec.defaultModuleImage.
	DefaultModuleImage string
}

// moduleImage resolves the image a module.Executor should use when
// dispatching to ModuleRunner — cluster.Spec.Runner.Image (set directly, or
// inherited from a Template at creation time — see
// hyvev1alpha1.RenderClusterDefinitionSpec) if set, else
// r.DefaultModuleImage. Deliberately does not consult the module's own
// module.yaml spec.runner.image/hyve.lock entry: a module can
// recommend/document a suitable image (its requirements.tools entries),
// but doesn't choose one — the same module may need different images
// across different deployments, which is a per-cluster/per-Template
// decision, not the module's to make.
func (r *Reconciler) moduleImage(cluster types.ClusterDefinition) string {
	if cluster.Spec.Runner.Image != "" {
		return cluster.Spec.Runner.Image
	}
	return r.DefaultModuleImage
}

func NewReconciler(stateMgr StateProvider) *Reconciler {
	return &Reconciler{stateMgr: stateMgr}
}

// logf logs a progress line the same way log.Printf does, and additionally
// writes it to r.Logger when set.
func (r *Reconciler) logf(format string, args ...interface{}) {
	log.Printf(format, args...)
	if r.Logger != nil {
		fmt.Fprintf(r.Logger, format+"\n", args...)
	}
}

// ReconcileAll reconciles every cluster definition, looping until all have been
// processed and the repository state has converged.
//
// When dryRun is true, the whole cycle is read-only: cluster create/delete/
// scale/workflows and resource apply/delete are all skipped and logged as
// "would run" instead of executed — see reconcileCluster and
// reconcileResources for exactly what's gated. Pre-flight validation
// (hyve.lock presence) still runs and still fails hard, matching Terraform's
// `plan` failing on invalid config.
func (r *Reconciler) ReconcileAll(ctx context.Context, clusterDefs []types.ClusterDefinition, dryRun bool) error {
	lf, err := module.LoadLockFile(r.stateMgr.LocalPath())
	if err != nil {
		return fmt.Errorf("failed to load hyve.lock: %w", err)
	}

	for _, c := range clusterDefs {
		if err := validateDriverModuleLocked(c, lf); err != nil {
			return err
		}
		if err := validateWorkflowRefsLocked(c, lf); err != nil {
			return err
		}
	}

	if len(clusterDefs) == 0 {
		r.logf("No cluster definitions found — skipping reconcile")
		return nil
	}

	r.logf("═══════════════════════════════════════════")
	if dryRun {
		r.logf("  DRY RUN: previewing %d cluster(s) — nothing will be changed", len(clusterDefs))
	} else {
		r.logf("  Reconciling %d cluster(s)", len(clusterDefs))
	}
	r.logf("═══════════════════════════════════════════")

	r.convergenceLoop(ctx, clusterDefs, lf, dryRun)
	return nil
}

func (r *Reconciler) convergenceLoop(ctx context.Context, initialDefs []types.ClusterDefinition, lf *module.LockFile, dryRun bool) []types.ClusterDefinition {
	processed := make(map[string]bool)
	currentDefs := initialDefs

	for {
		var next *types.ClusterDefinition
		for i := range currentDefs {
			if !processed[currentDefs[i].Metadata.Name] {
				next = &currentDefs[i]
				break
			}
		}
		if next == nil {
			break
		}

		name := next.Metadata.Name
		processed[name] = true

		if err := r.ReconcileOne(ctx, *next, lf, dryRun, nil); err != nil {
			r.logf("[%s] reconcile error: %v", name, err)
		}

		reloaded, err := r.stateMgr.LoadClusterDefinitions()
		if err != nil {
			r.logf("Warning: failed to reload cluster definitions: %v", err)
		} else {
			currentDefs = reloaded
		}
	}

	return currentDefs
}

// ReconcileOne reconciles a single cluster definition — the pause check,
// expiry-to-delete promotion, and dispatch logic ReconcileAll's convergence
// loop runs per cluster today, extracted so a controller-mode reconcile
// loop (one ClusterDefinition CR per Reconcile(ctx, req) call, no batch of
// definitions to iterate) can drive the exact same engine a file-based
// `hyve reconcile` run does — "same engine, different source of truth," not
// a second implementation. def is taken by value and only ever mutated on
// a local copy (matching convergenceLoop's prior in-place mutation of its
// own loop variable, never the caller's), so lf's per-cluster lock
// validation below runs against exactly what def's driver/workflow refs
// name, independent of whatever ReconcileAll's own upfront batch validation
// already checked for a file-mode run.
//
// secretsEnv, when non-nil, is merged into every module/workflow
// operation's env as "KEY=VALUE" pairs — cluster mode's live, per-reconcile
// fetch of the hyve-cli-secrets Secret (see
// internal/controller/reconciler.go's Reconcile), passed explicitly rather
// than via a shared mutable field so concurrent reconciles of different
// clusters (MaxConcurrentReconciles > 1) can't race on it. File/CLI mode
// always passes nil here: its module/workflow child processes already
// inherit the CLI's own os.Environ() directly, the same secrets flow
// that's always existed for local mode.
func (r *Reconciler) ReconcileOne(ctx context.Context, def types.ClusterDefinition, lf *module.LockFile, dryRun bool, secretsEnv map[string]string) error {
	name := def.Metadata.Name

	if err := validateDriverModuleLocked(def, lf); err != nil {
		return err
	}
	if err := validateWorkflowRefsLocked(def, lf); err != nil {
		return err
	}

	r.logf("───────────────────────────────────────────")
	r.logf("  [%s]  driver=%s@%s  region=%s", name, def.Spec.Driver.Source, def.Spec.Driver.Version, def.Metadata.Region)
	r.logf("───────────────────────────────────────────")

	if def.Spec.Pause {
		r.logf("[%s] Paused — skipping reconciliation", name)
		return nil
	}

	if def.Spec.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, def.Spec.ExpiresAt); err == nil {
			if time.Now().After(t) {
				r.logf("[%s] Cluster has expired (expiresAt: %s) — marking for deletion", name, def.Spec.ExpiresAt)
				def.Spec.Delete = true
			}
		} else {
			r.logf("[%s] Warning: invalid expiresAt value '%s': %v", name, def.Spec.ExpiresAt, err)
		}
	}

	if unmet, err := r.unmetDependency(ctx, def, lf, secretsEnv); err != nil {
		r.logf("[%s] Warning: failed to check dependsOn: %v", name, err)
	} else if unmet != "" {
		r.logf("[%s] Waiting on dependsOn cluster %q to become ACTIVE — skipping this cycle", name, unmet)
		return nil
	}

	return r.reconcileCluster(ctx, def, lf, dryRun, secretsEnv)
}

// effectiveStatus applies the authOnly default: an authOnly module's status
// op typically doesn't exist, so an empty HYVE_CLUSTER_STATUS is treated as
// ACTIVE rather than falling into the reconciler's "Unhandled status" no-op.
// A status script that exists but legitimately prints nothing is treated
// identically to one that's absent — this function doesn't distinguish the
// two cases, by design (see internal/module/executor.go's Execute).
func effectiveStatus(status string, isAuthOnly bool) string {
	if status == "" && isAuthOnly {
		return "ACTIVE"
	}
	return status
}

func (r *Reconciler) reconcileCluster(ctx context.Context, cluster types.ClusterDefinition, lf *module.LockFile, dryRun bool, secretsEnv map[string]string) error {
	name := cluster.Metadata.Name
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	resolved, err := module.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, r.stateMgr.LocalPath())
	if err != nil {
		return fmt.Errorf("resolve module: %w", err)
	}

	manifest, _ := module.LoadManifestForSource(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, r.stateMgr.LocalPath(), lf)
	if manifest != nil {
		// ValidateToolRequirements checks *this process's own* PATH — only
		// meaningful when the module actually runs inline in it (local/CLI
		// mode, r.ModuleRunner == nil). In cluster mode the module runs
		// inside a separate Job, on its own runner.image, which this
		// process has no visibility into and was never meant to share
		// tooling with — that's the entire point of Job dispatch. Skipping
		// this pre-flight check there is safe: a genuinely missing tool
		// still fails, just naturally, as an ordinary "command not found"
		// from inside the Job's own script.
		if r.ModuleRunner == nil {
			if reqErr := module.ValidateToolRequirements(manifest.Spec.Requirements.Tools); reqErr != nil {
				return reqErr
			}
		}
		if reqErr := r.validateMgmtClusterRequirement(name, manifest.Spec.Requirements.MgmtCluster); reqErr != nil {
			return reqErr
		}
	}
	isAuthOnly := manifest != nil && manifest.Metadata.Type == module.ModuleTypeAuthOnly

	env := buildModuleEnv(cluster, secretsEnv)
	exec := &module.Executor{
		ModuleDir:   resolved.Dir,
		Env:         env,
		WorkDir:     r.stateMgr.LocalPath(),
		ClusterName: name,
		Runner:      r.ModuleRunner,
		Image:       r.moduleImage(cluster),
	}

	statusResult, err := exec.Execute(ctx, module.OperationStatus)
	if err != nil {
		return fmt.Errorf("status check failed: %w", err)
	}
	status := effectiveStatus(statusResult.Outputs["HYVE_CLUSTER_STATUS"], isAuthOnly)
	r.logf("[%s] status: %s", name, status)

	switch {
	case cluster.Spec.Delete && (status == "ACTIVE" || status == "FAILED"):
		if dryRun {
			r.logf("[%s] DRY RUN: would delete cluster", name)
			return nil
		}
		return r.deleteCluster(ctx, cluster, exec, env, lf)

	case cluster.Spec.Delete && status == "NOT_FOUND":
		if dryRun {
			r.logf("[%s] DRY RUN: already gone in cloud, would remove YAML", name)
			return nil
		}
		r.logf("[%s] Already gone — removing YAML", name)
		return r.removeClusterFile(ctx, cluster)

	case !cluster.Spec.Delete && (status == "NOT_FOUND" || status == "FAILED"):
		if dryRun {
			r.logf("[%s] DRY RUN: would create cluster", name)
			return nil
		}
		return r.createCluster(ctx, cluster, exec, env, lf, secretsEnv)

	case status == "ACTIVE" && !cluster.Spec.Delete:
		// Hoisted out of the paramsChanged branch: resource reconciliation
		// below needs KUBECONFIG regardless of param drift, and must not
		// assume auth already ran this cycle (the no-drift path previously
		// never called OperationAuth at all). Calling it once here,
		// unconditionally, also avoids a redundant double-auth call on
		// cycles that have both param drift and resource work. Auth itself
		// is read-only (credential/kubeconfig setup) so it still runs for
		// real even in dry-run — kubectl diff needs it to reach the live
		// cluster.
		authResult, authErr := exec.Execute(ctx, module.OperationAuth)
		if authErr != nil {
			r.logf("[%s] Warning: auth failed: %v", name, authErr)
		} else {
			if kc := authResult.Outputs["KUBECONFIG"]; kc != "" {
				env = append(env, "KUBECONFIG="+kc)
				exec.Env = env
			}
			r.dedupeKubeconfigAfterAuth(name, authResult.Outputs["KUBECONFIG"])
		}

		if r.paramsChanged(cluster) {
			if dryRun {
				r.logf("[%s] DRY RUN: param drift detected — would run PreReconcile workflows and scale", name)
			} else {
				r.logf("[%s] Param drift detected — scaling", name)
				r.runWorkflows(ctx, cluster.Spec.Workflows.PreReconcile, cluster, env, lf)
				if _, scaleErr := exec.Execute(ctx, module.OperationScale); scaleErr != nil {
					r.logf("[%s] Warning: scale failed: %v", name, scaleErr)
				}
			}
		} else {
			r.logf("[%s] Up to date — no action needed", name)
		}

		repoCfg, cfgErr := r.stateMgr.LoadRepoConfig()
		if cfgErr != nil {
			r.logf("[%s] Warning: failed to load hyve.yaml (defaulting strictResourceDelete=false): %v", name, cfgErr)
			repoCfg = &state.RepoConfig{}
		}
		return r.reconcileResources(ctx, &cluster, env, repoCfg.Reconcile.StrictResourceDelete, dryRun)

	case status == "CREATING" || status == "UPDATING" || status == "DELETING":
		r.logf("[%s] Operation in progress (%s) — skipping", name, status)

	default:
		r.logf("[%s] Unhandled status %q", name, status)
	}

	return nil
}

func (r *Reconciler) createCluster(ctx context.Context, cluster types.ClusterDefinition, exec *module.Executor, env []string, lf *module.LockFile, secretsEnv map[string]string) error {
	name := cluster.Metadata.Name
	r.logf("[%s] Creating cluster...", name)

	hookVars := r.runWorkflows(ctx, cluster.Spec.Workflows.BeforeCreate, cluster, env, lf)
	for k, v := range hookVars {
		env = append(env, k+"="+v)
	}
	exec.Env = env

	result, err := exec.Execute(ctx, module.OperationCreate)
	if err != nil {
		return fmt.Errorf("create operation failed: %w", err)
	}

	if cluster.Spec.DriverOutputs == nil {
		cluster.Spec.DriverOutputs = make(map[string]string)
	}
	for k, v := range result.Outputs {
		cluster.Spec.DriverOutputs[k] = v
	}
	cluster.Spec.DriverOutputs["HYVE_LAST_PARAMS_HASH"] = paramsHash(cluster.Spec.Params)

	if err := r.stateMgr.SaveClusterDefinition(&cluster); err != nil {
		r.logf("[%s] Warning: failed to save driverOutputs: %v", name, err)
	}

	r.logf("[%s] ✅ Cluster created", name)

	// Rebuild env so onCreate workflows see the new driverOutputs.
	env = buildModuleEnv(cluster, secretsEnv)
	exec.Env = env

	authResult, authErr := exec.Execute(ctx, module.OperationAuth)
	if authErr != nil {
		r.logf("[%s] Warning: auth failed: %v", name, authErr)
	} else {
		if kc := authResult.Outputs["KUBECONFIG"]; kc != "" {
			env = append(env, "KUBECONFIG="+kc)
			exec.Env = env
		}
		r.dedupeKubeconfigAfterAuth(name, authResult.Outputs["KUBECONFIG"])
	}

	r.runWorkflows(ctx, cluster.Spec.Workflows.OnCreate, cluster, env, lf)

	// Apply spec.resources on the same cycle the cluster is created, rather
	// than waiting for the next ACTIVE-branch reconcile. A failure here is a
	// warning, not a hard error, matching the soft-failure convention already
	// used for everything else past OperationCreate in this function.
	repoCfg, cfgErr := r.stateMgr.LoadRepoConfig()
	if cfgErr != nil {
		r.logf("[%s] Warning: failed to load hyve.yaml (defaulting strictResourceDelete=false): %v", name, cfgErr)
		repoCfg = &state.RepoConfig{}
	}
	if resErr := r.reconcileResources(ctx, &cluster, env, repoCfg.Reconcile.StrictResourceDelete, false); resErr != nil {
		r.logf("[%s] Warning: resource reconciliation failed: %v", name, resErr)
	}

	// Runs after spec.resources so afterCreate workflows can rely on resource-created
	// objects (namespaces, Deployments) already existing — unlike onCreate, which runs
	// before resources. Fires regardless of the resource-reconciliation outcome above,
	// matching this function's existing soft-failure convention.
	r.runWorkflows(ctx, cluster.Spec.Workflows.AfterCreate, cluster, env, lf)

	return nil
}

func (r *Reconciler) deleteCluster(ctx context.Context, cluster types.ClusterDefinition, exec *module.Executor, env []string, lf *module.LockFile) error {
	name := cluster.Metadata.Name
	r.logf("[%s] Deleting cluster...", name)

	authResult, authErr := exec.Execute(ctx, module.OperationAuth)
	if authErr != nil {
		r.logf("[%s] Warning: auth failed before onDelete: %v", name, authErr)
	} else {
		if kc := authResult.Outputs["KUBECONFIG"]; kc != "" {
			env = append(env, "KUBECONFIG="+kc)
			exec.Env = env
		}
		r.dedupeKubeconfigAfterAuth(name, authResult.Outputs["KUBECONFIG"])
	}

	r.runWorkflows(ctx, cluster.Spec.Workflows.OnDelete, cluster, env, lf)

	if _, err := exec.Execute(ctx, module.OperationDelete); err != nil {
		return fmt.Errorf("delete operation failed: %w", err)
	}

	r.logf("[%s] ✅ Cluster deleted", name)

	r.runWorkflows(ctx, cluster.Spec.Workflows.AfterDelete, cluster, env, lf)

	return r.removeClusterFile(ctx, cluster)
}

// dedupeKubeconfigAfterAuth rewrites cluster name's kubeconfig file to
// remove duplicate entries an external auth tool may have appended across
// reconcile cycles (e.g. civo without --merge). kcPath is empty when auth
// didn't export a kubeconfig at all, in which case there's nothing to
// dedupe. Failures are logged as warnings, never fatal — kubeconfig hygiene
// is best-effort, not part of the reconcile contract.
func (r *Reconciler) dedupeKubeconfigAfterAuth(name, kcPath string) {
	if kcPath == "" {
		return
	}
	if err := kubeconfig.DeduplicateKubeconfigEntries(kcPath); err != nil {
		r.logf("[%s] Warning: failed to deduplicate kubeconfig: %v", name, err)
	}
}

func (r *Reconciler) removeClusterFile(_ context.Context, cluster types.ClusterDefinition) error {
	name := cluster.Metadata.Name
	if err := r.stateMgr.RemoveClusterFile(name); err != nil {
		return fmt.Errorf("remove cluster file: %w", err)
	}
	return nil
}

// runWorkflows runs every ref in refs against one shared workflow.Executor,
// then returns whatever HYVE_VAR=value lines those steps printed (see
// workflow.Executor.HookOutputVars) — createCluster merges these explicitly
// into the driver module's env before OperationCreate, replacing the old
// os.Setenv-based hand-off so concurrent reconciles of different clusters
// (see MaxConcurrentReconciles) can't cross-contaminate each other's
// captured values.
func (r *Reconciler) runWorkflows(ctx context.Context, refs []types.WorkflowRef, cluster types.ClusterDefinition, env []string, lf *module.LockFile) map[string]string {
	if len(refs) == 0 {
		return nil
	}
	name := cluster.Metadata.Name

	wfMgr, err := workflow.NewManagerWithSource(r.stateMgr.LocalPath(), r.stateMgr.WorkflowSource())
	if err != nil {
		r.logf("[%s] Failed to create workflow manager: %v", name, err)
		return nil
	}

	injected := make(map[string]string, len(env))
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			injected[kv[:idx]] = kv[idx+1:]
		}
	}

	executor, err := workflow.NewExecutor(wfMgr, "")
	if err != nil {
		r.logf("[%s] Failed to create workflow executor: %v", name, err)
		return nil
	}
	defer executor.Close()
	executor.Output = r.Logger
	if r.StepRunner != nil {
		executor.StepRunner = r.StepRunner
	}
	executor.DefaultWorkflowImage = r.DefaultWorkflowImage
	// Lifecycle hooks (onCreate/onDelete/etc.) are triggered by an
	// automated reconcile, never a human — a runtime: client workflow
	// referenced here would have no "invoking machine" to run on in
	// controller mode, so refuse it explicitly rather than silently running
	// it on whichever process happens to host the reconcile loop.
	executor.AllowClientRuntime = false
	executor.KubeconfigLocator = module.KubeconfigPathForCluster
	executor.InjectVars(injected)

	for _, ref := range refs {
		label := ref.String()
		r.logf("[%s] ▶  Workflow '%s' starting...", name, label)

		var execution *workflow.WorkflowExecution
		var runErr error
		if !ref.IsRemote() {
			execution, runErr = executor.RunWorkflow(ctx, ref.Name, "")
		} else {
			execution, runErr = r.runRemoteWorkflowHook(ctx, executor, ref, lf)
		}
		if runErr != nil {
			r.logf("[%s] ⚠️  Workflow '%s' failed: %v", name, label, runErr)
			continue
		}
		if execution.Status == workflow.StatusCompleted {
			r.logf("[%s] ✅ Workflow '%s' completed", name, label)
		} else {
			r.logf("[%s] ⚠️  Workflow '%s' finished with status: %s", name, label, execution.Status)
		}
	}

	return executor.HookOutputVars()
}

// runRemoteWorkflowHook resolves a remote WorkflowRef using hyve.lock as a
// cache hint (a matching locked+cached entry means no network call — the
// pre-flight check in ReconcileAll already guarantees a locked entry
// exists) and executes it. A lifecycle hook ref must resolve to a single
// file: directory-kind sources are rejected here — a hook names exactly one
// workflow to run, not a batch.
func (r *Reconciler) runRemoteWorkflowHook(ctx context.Context, executor *workflow.Executor, ref types.WorkflowRef, lf *module.LockFile) (*workflow.WorkflowExecution, error) {
	ps, err := workflowref.ParseSource(ref.Source)
	if err != nil {
		return nil, err
	}
	ps, _ = workflowref.ApplyPathOverride(ps, ref.Path)
	kind, err := workflowref.ClassifyPath(ps.Path)
	if err != nil {
		return nil, err
	}
	if kind == workflowref.PathKindDir {
		return nil, fmt.Errorf("lifecycle hook workflow ref %q resolves to a directory — must reference exactly one file", ref.Source)
	}

	files, err := workflowref.Resolve(ref.Source, ref.Path, lf)
	if err != nil {
		return nil, err
	}
	var wf workflow.Workflow
	if err := yaml.Unmarshal(files[0].Data, &wf); err != nil {
		return nil, fmt.Errorf("parse remote workflow %s: %w", ref.Source, err)
	}
	return executor.RunResolvedWorkflow(ctx, &wf, ref.String(), "")
}

// validateDriverModuleLocked checks that a cluster's driver module is either
// a local path (needs no hyve.lock entry — module.Resolve reads it straight
// off disk via module.resolveLocal, with no digest to verify, so a lock
// entry for one only ever holds an empty resolved/sha256 pair: required
// presence, zero actual integrity value) or already present in hyve.lock.
// Mirrors validateWorkflowRefsLocked's local/remote split below.
func validateDriverModuleLocked(c types.ClusterDefinition, lf *module.LockFile) error {
	if c.Spec.Driver.Source == "" {
		return fmt.Errorf("cluster %s: no driver specified — set spec.driver.source in the cluster YAML", c.Metadata.Name)
	}
	if module.IsLocalSource(c.Spec.Driver.Source) {
		return nil
	}
	if lf.GetLocked(c.Spec.Driver.Source, c.Spec.Driver.Version) == nil {
		return fmt.Errorf("cluster %s: module %s@%s not in hyve.lock — run `hyve module install`",
			c.Metadata.Name, c.Spec.Driver.Source, c.Spec.Driver.Version)
	}
	return nil
}

// validateWorkflowRefsLocked checks — with no network access — that every
// remote WorkflowRef in a cluster's lifecycle hooks is already present in
// hyve.lock. Mirrors validateDriverModuleLocked's local/remote split above.
// AllWorkflowHookRefs returns every WorkflowRef across all of c's lifecycle
// hooks — the single list validateWorkflowRefsLocked validates against and
// internal/controller's resolveWorkflowIfNeeded resolves against, so the
// two can never drift out of sync with each other (or with runWorkflows'
// own set of hook fields it actually runs).
func AllWorkflowHookRefs(c types.ClusterDefinition) []types.WorkflowRef {
	lists := [][]types.WorkflowRef{
		c.Spec.Workflows.PreReconcile, c.Spec.Workflows.BeforeCreate,
		c.Spec.Workflows.OnCreate, c.Spec.Workflows.AfterCreate,
		c.Spec.Workflows.OnDelete, c.Spec.Workflows.AfterDelete,
	}
	var refs []types.WorkflowRef
	for _, list := range lists {
		refs = append(refs, list...)
	}
	return refs
}

func validateWorkflowRefsLocked(c types.ClusterDefinition, lf *module.LockFile) error {
	for _, ref := range AllWorkflowHookRefs(c) {
		if !ref.IsRemote() {
			continue
		}
		ps, err := workflowref.ParseSource(ref.Source)
		if err != nil {
			return fmt.Errorf("cluster %s: %w", c.Metadata.Name, err)
		}
		ps, _ = workflowref.ApplyPathOverride(ps, ref.Path)
		kind, err := workflowref.ClassifyPath(ps.Path)
		if err != nil {
			return fmt.Errorf("cluster %s: %w", c.Metadata.Name, err)
		}
		if kind == workflowref.PathKindDir {
			return fmt.Errorf("cluster %s: workflow ref %q resolves to a directory — lifecycle hooks must reference a single file", c.Metadata.Name, ref.Source)
		}
		if lf.GetLockedWorkflow(ps.CanonicalSource(), ps.Version) == nil {
			return fmt.Errorf("cluster %s: workflow %s not in hyve.lock — run `hyve workflow install` (local mode), or check the controller logs for a resolution failure (cluster mode resolves this automatically per-reconcile — see resolveWorkflowIfNeeded)", c.Metadata.Name, ref.Source)
		}
	}
	return nil
}

// checkDependencyStatus resolves depCluster's driver module and returns its
// current status — a lighter-weight duplicate of reconcileCluster's own
// module-resolve-then-status preamble (deliberately not shared: that
// preamble also builds a module.Executor for the caller to run create/
// delete against afterward, which a dependsOn check never does — reusing
// it would mean threading an unused Executor back out for no reason).
// Treats a resolve/execute failure as "not ACTIVE" rather than propagating
// the error — dependsOn's whole point is "skip this cycle, don't fail
// hard," so a dependency that's erroring out should read the same as one
// that's simply not ready yet.
func (r *Reconciler) checkDependencyStatus(ctx context.Context, depCluster types.ClusterDefinition, lf *module.LockFile, secretsEnv map[string]string) string {
	locked := lf.GetLocked(depCluster.Spec.Driver.Source, depCluster.Spec.Driver.Version)
	resolved, err := module.Resolve(depCluster.Spec.Driver.Source, depCluster.Spec.Driver.Version, locked, r.stateMgr.LocalPath())
	if err != nil {
		return ""
	}
	manifest, _ := module.LoadManifestForSource(depCluster.Spec.Driver.Source, depCluster.Spec.Driver.Version, r.stateMgr.LocalPath(), lf)
	isAuthOnly := manifest != nil && manifest.Metadata.Type == module.ModuleTypeAuthOnly

	env := buildModuleEnv(depCluster, secretsEnv)
	exec := &module.Executor{
		ModuleDir:   resolved.Dir,
		Env:         env,
		WorkDir:     r.stateMgr.LocalPath(),
		ClusterName: depCluster.Metadata.Name,
		Runner:      r.ModuleRunner,
		Image:       r.moduleImage(depCluster),
	}
	statusResult, err := exec.Execute(ctx, module.OperationStatus)
	if err != nil {
		return ""
	}
	return effectiveStatus(statusResult.Outputs["HYVE_CLUSTER_STATUS"], isAuthOnly)
}

// unmetDependency returns the first entry in def.Spec.DependsOn that isn't
// currently ACTIVE, if any — see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// "Optional dependsOn ordering" section. A named dependency that doesn't
// exist at all counts as unmet, same as one that exists but isn't ACTIVE
// yet — both mean "not ready," and ReconcileOne's caller treats either the
// same way (skip this cycle, log it, don't fail hard).
func (r *Reconciler) unmetDependency(ctx context.Context, def types.ClusterDefinition, lf *module.LockFile, secretsEnv map[string]string) (string, error) {
	if len(def.Spec.DependsOn) == 0 {
		return "", nil
	}
	defs, err := r.stateMgr.LoadClusterDefinitions()
	if err != nil {
		return "", fmt.Errorf("failed to load cluster definitions for dependsOn check: %w", err)
	}
	byName := make(map[string]types.ClusterDefinition, len(defs))
	for _, d := range defs {
		byName[d.Metadata.Name] = d
	}
	for _, depName := range def.Spec.DependsOn {
		dep, ok := byName[depName]
		if !ok || r.checkDependencyStatus(ctx, dep, lf, secretsEnv) != "ACTIVE" {
			return depName, nil
		}
	}
	return "", nil
}

// validateMgmtClusterRequirement checks that a module's optional
// requirements.mgmtCluster (see internal/module.ModuleRequirements) names a
// cluster that actually exists in the current StateProvider, before
// reconcile ever attempts one of the module's operations against it — a
// missing/wrong mgmtCluster would otherwise only ever surface as a script
// failure deep inside create.yaml (or wherever the module's own op files
// try to use credentials for it). Works identically in local/CLI mode and
// controller mode — LoadClusterDefinitions is a StateProvider method, not
// something either mode implements specially.
func (r *Reconciler) validateMgmtClusterRequirement(clusterName, mgmtCluster string) error {
	if mgmtCluster == "" {
		return nil
	}
	defs, err := r.stateMgr.LoadClusterDefinitions()
	if err != nil {
		return fmt.Errorf("cluster %s: failed to check mgmtCluster requirement %q: %w", clusterName, mgmtCluster, err)
	}
	for _, d := range defs {
		if d.Metadata.Name == mgmtCluster {
			return nil
		}
	}
	return fmt.Errorf("cluster %s: module requires mgmtCluster %q, which doesn't exist — create it first, or check for a typo", clusterName, mgmtCluster)
}

func (r *Reconciler) paramsChanged(cluster types.ClusterDefinition) bool {
	stored := cluster.Spec.DriverOutputs["HYVE_LAST_PARAMS_HASH"]
	return stored != "" && stored != paramsHash(cluster.Spec.Params)
}

func paramsHash(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(params))
	for _, k := range keys {
		ordered[k] = params[k]
	}
	data, _ := json.Marshal(ordered)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}
