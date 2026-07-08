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
	stateMgr *state.Manager

	// Logger, when set, additionally receives every progress line logged
	// during a reconcile run — used by hyve-server to capture live progress
	// for polling/WebSocket streaming without affecting the CLI's normal
	// stdout/log.Printf behavior. Left nil by the CLI, which never sets it.
	Logger io.Writer
}

func NewReconciler(stateMgr *state.Manager) *Reconciler {
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

		r.logf("───────────────────────────────────────────")
		r.logf("  [%s]  driver=%s@%s  region=%s", name, next.Spec.Driver.Source, next.Spec.Driver.Version, next.Metadata.Region)
		r.logf("───────────────────────────────────────────")

		if next.Spec.Pause {
			r.logf("[%s] Paused — skipping reconciliation", name)
		} else {
			if next.Spec.ExpiresAt != "" {
				if t, err := time.Parse(time.RFC3339, next.Spec.ExpiresAt); err == nil {
					if time.Now().After(t) {
						r.logf("[%s] Cluster has expired (expiresAt: %s) — marking for deletion", name, next.Spec.ExpiresAt)
						next.Spec.Delete = true
					}
				} else {
					r.logf("[%s] Warning: invalid expiresAt value '%s': %v", name, next.Spec.ExpiresAt, err)
				}
			}

			if err := r.reconcileCluster(ctx, *next, lf, dryRun); err != nil {
				r.logf("[%s] reconcile error: %v", name, err)
			}
		}

		if err := r.stateMgr.SyncWithRemote(ctx); err != nil {
			r.logf("Warning: failed to sync after %s: %v", name, err)
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

func (r *Reconciler) reconcileCluster(ctx context.Context, cluster types.ClusterDefinition, lf *module.LockFile, dryRun bool) error {
	name := cluster.Metadata.Name
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	resolved, err := module.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, r.stateMgr.LocalPath())
	if err != nil {
		return fmt.Errorf("resolve module: %w", err)
	}

	manifest, _ := module.LoadManifestForSource(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, r.stateMgr.LocalPath(), lf)
	if manifest != nil {
		if reqErr := module.ValidateToolRequirements(manifest.Spec.Requirements.Tools); reqErr != nil {
			return reqErr
		}
	}
	isAuthOnly := manifest != nil && manifest.Metadata.Type == module.ModuleTypeAuthOnly

	env := buildModuleEnv(cluster, nil)
	exec := &module.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: r.stateMgr.LocalPath()}

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
		return r.createCluster(ctx, cluster, exec, env, lf)

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
		if _, authErr := exec.Execute(ctx, module.OperationAuth); authErr != nil {
			r.logf("[%s] Warning: auth failed: %v", name, authErr)
		} else {
			r.dedupeKubeconfigAfterAuth(name)
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
		return r.reconcileResources(ctx, &cluster, repoCfg.Reconcile.StrictResourceDelete, dryRun)

	case status == "CREATING" || status == "UPDATING" || status == "DELETING":
		r.logf("[%s] Operation in progress (%s) — skipping", name, status)

	default:
		r.logf("[%s] Unhandled status %q", name, status)
	}

	return nil
}

func (r *Reconciler) createCluster(ctx context.Context, cluster types.ClusterDefinition, exec *module.Executor, env []string, lf *module.LockFile) error {
	name := cluster.Metadata.Name
	r.logf("[%s] Creating cluster...", name)

	r.runWorkflows(ctx, cluster.Spec.Workflows.BeforeCreate, cluster, env, lf)

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
	} else {
		if commitErr := r.stateMgr.CommitAndPush(ctx, "reconcile: create "+name); commitErr != nil {
			r.logf("[%s] Warning: failed to commit driverOutputs: %v", name, commitErr)
		}
	}

	r.logf("[%s] ✅ Cluster created", name)

	// Rebuild env so onCreate workflows see the new driverOutputs.
	env = buildModuleEnv(cluster, nil)
	exec.Env = env

	if _, authErr := exec.Execute(ctx, module.OperationAuth); authErr != nil {
		r.logf("[%s] Warning: auth failed: %v", name, authErr)
	} else {
		r.dedupeKubeconfigAfterAuth(name)
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
	if resErr := r.reconcileResources(ctx, &cluster, repoCfg.Reconcile.StrictResourceDelete, false); resErr != nil {
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

	if _, authErr := exec.Execute(ctx, module.OperationAuth); authErr != nil {
		r.logf("[%s] Warning: auth failed before onDelete: %v", name, authErr)
	} else {
		r.dedupeKubeconfigAfterAuth(name)
	}

	r.runWorkflows(ctx, cluster.Spec.Workflows.OnDelete, cluster, env, lf)

	if _, err := exec.Execute(ctx, module.OperationDelete); err != nil {
		return fmt.Errorf("delete operation failed: %w", err)
	}

	r.logf("[%s] ✅ Cluster deleted", name)

	r.runWorkflows(ctx, cluster.Spec.Workflows.AfterDelete, cluster, env, lf)

	return r.removeClusterFile(ctx, cluster)
}

// dedupeKubeconfigAfterAuth rewrites ~/.kube/config to remove duplicate
// entries an external auth tool may have appended (e.g. civo without
// --merge). Failures are logged as warnings, never fatal — kubeconfig
// hygiene is best-effort, not part of the reconcile contract.
func (r *Reconciler) dedupeKubeconfigAfterAuth(name string) {
	kcPath, err := module.DefaultKubeconfigPath()
	if err != nil {
		r.logf("[%s] Warning: could not resolve kubeconfig path: %v", name, err)
		return
	}
	if err := kubeconfig.DeduplicateKubeconfigEntries(kcPath); err != nil {
		r.logf("[%s] Warning: failed to deduplicate kubeconfig: %v", name, err)
	}
}

func (r *Reconciler) removeClusterFile(ctx context.Context, cluster types.ClusterDefinition) error {
	name := cluster.Metadata.Name
	if err := r.stateMgr.RemoveClusterFile(name); err != nil {
		return fmt.Errorf("remove cluster file: %w", err)
	}
	if err := r.stateMgr.CommitAndPush(ctx, "reconcile: delete "+name); err != nil {
		r.logf("[%s] Warning: failed to commit cluster file removal: %v", name, err)
	}
	return nil
}

func (r *Reconciler) runWorkflows(ctx context.Context, refs []types.WorkflowRef, cluster types.ClusterDefinition, env []string, lf *module.LockFile) {
	if len(refs) == 0 {
		return
	}
	name := cluster.Metadata.Name

	wfMgr, err := workflow.NewManager(r.stateMgr.LocalPath())
	if err != nil {
		r.logf("[%s] Failed to create workflow manager: %v", name, err)
		return
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
		return
	}
	defer executor.Close()
	executor.Output = r.Logger
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
func validateWorkflowRefsLocked(c types.ClusterDefinition, lf *module.LockFile) error {
	lists := [][]types.WorkflowRef{
		c.Spec.Workflows.PreReconcile, c.Spec.Workflows.BeforeCreate,
		c.Spec.Workflows.OnCreate, c.Spec.Workflows.OnDelete, c.Spec.Workflows.AfterDelete,
	}
	for _, list := range lists {
		for _, ref := range list {
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
				return fmt.Errorf("cluster %s: workflow %s not in hyve.lock — run `hyve workflow install`", c.Metadata.Name, ref.Source)
			}
		}
	}
	return nil
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
