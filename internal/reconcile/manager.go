package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

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
}

func NewReconciler(stateMgr *state.Manager) *Reconciler {
	return &Reconciler{stateMgr: stateMgr}
}

// ReconcileAll reconciles every cluster definition, looping until all have been
// processed and the repository state has converged.
func (r *Reconciler) ReconcileAll(ctx context.Context, clusterDefs []types.ClusterDefinition) error {
	lf, err := module.LoadLockFile(r.stateMgr.LocalPath())
	if err != nil {
		return fmt.Errorf("failed to load hyve.lock: %w", err)
	}

	for _, c := range clusterDefs {
		if c.Spec.Driver.Source == "" {
			return fmt.Errorf("cluster %s: no driver specified — set spec.driver.source in the cluster YAML", c.Metadata.Name)
		}
		if lf.GetLocked(c.Spec.Driver.Source, c.Spec.Driver.Version) == nil {
			return fmt.Errorf("cluster %s: module %s@%s not in hyve.lock — run `hyve module install`",
				c.Metadata.Name, c.Spec.Driver.Source, c.Spec.Driver.Version)
		}
		if err := validateWorkflowRefsLocked(c, lf); err != nil {
			return err
		}
	}

	if len(clusterDefs) == 0 {
		log.Println("No cluster definitions found — skipping reconcile")
		return nil
	}

	log.Printf("═══════════════════════════════════════════")
	log.Printf("  Reconciling %d cluster(s)", len(clusterDefs))
	log.Printf("═══════════════════════════════════════════")

	r.convergenceLoop(ctx, clusterDefs, lf)
	return nil
}

func (r *Reconciler) convergenceLoop(ctx context.Context, initialDefs []types.ClusterDefinition, lf *module.LockFile) []types.ClusterDefinition {
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

		log.Printf("───────────────────────────────────────────")
		log.Printf("  [%s]  driver=%s@%s  region=%s", name, next.Spec.Driver.Source, next.Spec.Driver.Version, next.Metadata.Region)
		log.Printf("───────────────────────────────────────────")

		if next.Spec.Pause {
			log.Printf("[%s] Paused — skipping reconciliation", name)
		} else {
			if next.Spec.ExpiresAt != "" {
				if t, err := time.Parse(time.RFC3339, next.Spec.ExpiresAt); err == nil {
					if time.Now().After(t) {
						log.Printf("[%s] Cluster has expired (expiresAt: %s) — marking for deletion", name, next.Spec.ExpiresAt)
						next.Spec.Delete = true
					}
				} else {
					log.Printf("[%s] Warning: invalid expiresAt value '%s': %v", name, next.Spec.ExpiresAt, err)
				}
			}

			if err := r.reconcileCluster(ctx, *next, lf); err != nil {
				log.Printf("[%s] reconcile error: %v", name, err)
			}
		}

		if err := r.stateMgr.SyncWithRemote(ctx); err != nil {
			log.Printf("Warning: failed to sync after %s: %v", name, err)
		}

		reloaded, err := r.stateMgr.LoadClusterDefinitions()
		if err != nil {
			log.Printf("Warning: failed to reload cluster definitions: %v", err)
		} else {
			currentDefs = reloaded
		}
	}

	return currentDefs
}

func (r *Reconciler) reconcileCluster(ctx context.Context, cluster types.ClusterDefinition, lf *module.LockFile) error {
	name := cluster.Metadata.Name
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	resolved, err := module.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, r.stateMgr.LocalPath())
	if err != nil {
		return fmt.Errorf("resolve module: %w", err)
	}

	env := buildModuleEnv(cluster, nil)
	exec := &module.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: r.stateMgr.LocalPath()}

	statusResult, err := exec.Execute(ctx, module.OperationStatus)
	if err != nil {
		return fmt.Errorf("status check failed: %w", err)
	}
	status := statusResult.Outputs["HYVE_CLUSTER_STATUS"]
	log.Printf("[%s] status: %s", name, status)

	switch {
	case cluster.Spec.Delete && (status == "ACTIVE" || status == "FAILED"):
		return r.deleteCluster(ctx, cluster, exec, env, lf)

	case cluster.Spec.Delete && status == "NOT_FOUND":
		log.Printf("[%s] Already gone — removing YAML", name)
		return r.removeClusterFile(ctx, cluster)

	case !cluster.Spec.Delete && (status == "NOT_FOUND" || status == "FAILED"):
		return r.createCluster(ctx, cluster, exec, env, lf)

	case status == "ACTIVE" && !cluster.Spec.Delete:
		if r.paramsChanged(cluster) {
			log.Printf("[%s] Param drift detected — scaling", name)
			if _, authErr := exec.Execute(ctx, module.OperationAuth); authErr != nil {
				log.Printf("[%s] Warning: auth failed before scale: %v", name, authErr)
			}
			r.runWorkflows(ctx, cluster.Spec.Workflows.PreReconcile, cluster, env, lf)
			if _, scaleErr := exec.Execute(ctx, module.OperationScale); scaleErr != nil {
				log.Printf("[%s] Warning: scale failed: %v", name, scaleErr)
			}
		} else {
			log.Printf("[%s] Up to date — no action needed", name)
		}

	case status == "CREATING" || status == "UPDATING" || status == "DELETING":
		log.Printf("[%s] Operation in progress (%s) — skipping", name, status)

	default:
		log.Printf("[%s] Unhandled status %q", name, status)
	}

	return nil
}

func (r *Reconciler) createCluster(ctx context.Context, cluster types.ClusterDefinition, exec *module.Executor, env []string, lf *module.LockFile) error {
	name := cluster.Metadata.Name
	log.Printf("[%s] Creating cluster...", name)

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
		log.Printf("[%s] Warning: failed to save driverOutputs: %v", name, err)
	} else {
		if commitErr := r.stateMgr.CommitAndPush(ctx, "reconcile: create "+name); commitErr != nil {
			log.Printf("[%s] Warning: failed to commit driverOutputs: %v", name, commitErr)
		}
	}

	log.Printf("[%s] ✅ Cluster created", name)

	// Rebuild env so onCreate workflows see the new driverOutputs.
	env = buildModuleEnv(cluster, nil)
	exec.Env = env

	if _, authErr := exec.Execute(ctx, module.OperationAuth); authErr != nil {
		log.Printf("[%s] Warning: auth failed: %v", name, authErr)
	}

	r.runWorkflows(ctx, cluster.Spec.Workflows.OnCreate, cluster, env, lf)
	return nil
}

func (r *Reconciler) deleteCluster(ctx context.Context, cluster types.ClusterDefinition, exec *module.Executor, env []string, lf *module.LockFile) error {
	name := cluster.Metadata.Name
	log.Printf("[%s] Deleting cluster...", name)

	if _, authErr := exec.Execute(ctx, module.OperationAuth); authErr != nil {
		log.Printf("[%s] Warning: auth failed before onDelete: %v", name, authErr)
	}

	r.runWorkflows(ctx, cluster.Spec.Workflows.OnDelete, cluster, env, lf)

	if _, err := exec.Execute(ctx, module.OperationDelete); err != nil {
		return fmt.Errorf("delete operation failed: %w", err)
	}

	log.Printf("[%s] ✅ Cluster deleted", name)

	r.runWorkflows(ctx, cluster.Spec.Workflows.AfterDelete, cluster, env, lf)

	return r.removeClusterFile(ctx, cluster)
}

func (r *Reconciler) removeClusterFile(ctx context.Context, cluster types.ClusterDefinition) error {
	name := cluster.Metadata.Name
	if err := r.stateMgr.RemoveClusterFile(name); err != nil {
		return fmt.Errorf("remove cluster file: %w", err)
	}
	if err := r.stateMgr.CommitAndPush(ctx, "reconcile: delete "+name); err != nil {
		log.Printf("[%s] Warning: failed to commit cluster file removal: %v", name, err)
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
		log.Printf("[%s] Failed to create workflow manager: %v", name, err)
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
		log.Printf("[%s] Failed to create workflow executor: %v", name, err)
		return
	}
	defer executor.Close()
	executor.InjectVars(injected)

	for _, ref := range refs {
		label := ref.String()
		log.Printf("[%s] ▶  Workflow '%s' starting...", name, label)

		var execution *workflow.WorkflowExecution
		var runErr error
		if !ref.IsRemote() {
			execution, runErr = executor.RunWorkflow(ctx, ref.Name, "")
		} else {
			execution, runErr = r.runRemoteWorkflowHook(ctx, executor, ref, lf)
		}
		if runErr != nil {
			log.Printf("[%s] ⚠️  Workflow '%s' failed: %v", name, label, runErr)
			continue
		}
		if execution.Status == workflow.StatusCompleted {
			log.Printf("[%s] ✅ Workflow '%s' completed", name, label)
		} else {
			log.Printf("[%s] ⚠️  Workflow '%s' finished with status: %s", name, label, execution.Status)
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

// validateWorkflowRefsLocked checks — with no network access — that every
// remote WorkflowRef in a cluster's lifecycle hooks is already present in
// hyve.lock, mirroring the existing driver-module pre-flight check.
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
