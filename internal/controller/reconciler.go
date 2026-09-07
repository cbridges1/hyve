package controller

import (
	"context"
	"fmt"
	"log"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/resourceref"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflowref"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// cliSecretsName is the single shared Secret `hyve env secrets` (cluster
// mode) manages — see that command's own design. The controller only ever
// reads it (RBAC: get, resourceNames: [cliSecretsName] — see
// deploy/helm/hyve/templates/controller-rbac.yaml), never writes it.
const cliSecretsName = "hyve-cli-secrets"

// cliSecretGitHubToken is the key module resolution (resolveModuleIfNeeded)
// reads out of the fetched hyve-cli-secrets map for a private Git-sourced
// module — see module.ResolveWithToken/EnsureResolvedWithToken.
const cliSecretGitHubToken = "GITHUB_TOKEN"

// resyncInterval is how often a ClusterDefinition is re-reconciled even
// with no spec change — the controller-mode equivalent of running `hyve
// reconcile` on a recurring cron/CI schedule in file mode. Chosen to
// roughly match a typical short cron cadence rather than any hard
// requirement; not currently configurable per-cluster.
const resyncInterval = 5 * time.Minute

// ClusterDefinitionReconciler drives one ClusterDefinition CR through the
// same internal/reconcile engine a file-based `hyve reconcile` run uses —
// "same engine, different source of truth," not a second implementation.
// It owns the concerns CRDStateProvider deliberately doesn't know about
// (finalizer lifecycle, status/condition bookkeeping) since those are
// specific to being a controller-runtime Reconciler, not part of the
// source-of-truth-agnostic StateProvider abstraction.
type ClusterDefinitionReconciler struct {
	Client        client.Client
	Reconciler    *reconcile.Reconciler
	StateProvider *CRDStateProvider
	Namespace     string

	// APIReader, when set, is used instead of Client for fetchCLISecrets —
	// an uncached, direct-to-API-server read (mgr.GetAPIReader(), same
	// pattern cmd/controller/run.go's own HyveConfig startup read uses).
	// The cached Client's informer needs unscoped list/watch RBAC to
	// populate its cache for a type at all — Kubernetes RBAC's
	// resourceNames restriction has no effect on list/watch, only get (see
	// controller-rbac.yaml's own comment) — so reading hyve-cli-secrets
	// through it would force granting the controller list/watch on every
	// Secret in the namespace just to read one by name. An uncached read
	// needs only "get" with resourceNames, and there's no cache staleness
	// concern to trade away either: this is already a live, once-per-
	// reconcile fetch, not something the cache's watch-driven freshness
	// would meaningfully improve on. Falls back to Client if left nil
	// (tests construct ClusterDefinitionReconciler directly without a real
	// manager).
	APIReader client.Reader

	// MaxConcurrentReconciles bounds how many ClusterDefinitions this
	// controller reconciles at once. controller-runtime defaults this to 1
	// if left at zero, which means a single cluster stuck in a long-running
	// step (e.g. a workflow Job wedged on ImagePullBackOff, polled for up to
	// KubernetesJobStepRunner's Timeout) blocks reconciliation of every
	// other ClusterDefinition in the namespace for the same duration.
	// Concurrent reconciles are safe: every place that used to communicate
	// KUBECONFIG/injected/hook-output vars via process-wide os.Setenv now
	// threads them explicitly per-cluster (module.Executor.ClusterName's
	// isolated kubeconfig path, workflow.Executor.variables, the env
	// []string threaded through reconcile/resources.go) — see
	// KubeconfigPathForCluster and workflow.Executor.HookOutputVars.
	MaxConcurrentReconciles int

	// Recorder, when set, is used to emit Kubernetes Events (visible via
	// `kubectl describe clusterdefinition <name>` and `kubectl get events`,
	// and via GET /api/clusters/{name}/events) for the lifecycle milestones
	// internal/reconcile.ReconcileHooks.OnEvent fires at — status checks,
	// create/delete start/success/failure, auth failures. cmd/controller/
	// run.go sets this from mgr.GetEventRecorderFor(...); left nil in tests
	// that construct ClusterDefinitionReconciler directly, in which case
	// event emission is silently skipped (see the hooks closures below).
	Recorder record.EventRecorder
}

// Reconcile implements the controller-runtime reconcile loop for one
// ClusterDefinition.
func (r *ClusterDefinitionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr hyvev1alpha1.ClusterDefinition
	if err := r.Client.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ClusterDefinition: %w", err)
	}

	if cr.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, &cr)
	}

	if !controllerutil.ContainsFinalizer(&cr, hyvev1alpha1.ClusterDefinitionFinalizer) {
		controllerutil.AddFinalizer(&cr, hyvev1alpha1.ClusterDefinitionFinalizer)
		if err := r.Client.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		// The Update above triggers a fresh watch event, which re-enters
		// Reconcile with the finalizer already present — no need to
		// duplicate the reconcile-body logic below in this branch too.
		return ctrl.Result{}, nil
	}

	def := crdconv.ToTypesClusterDefinition(&cr)

	lf, err := module.LoadLockFile(r.StateProvider.LocalPath())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("load hyve.lock: %w", err)
	}
	secretsEnv := r.fetchCLISecrets(ctx)
	lf = r.resolveModuleIfNeeded(ctx, lf, def.Spec.Driver.Source, def.Spec.Driver.Version, secretsEnv[cliSecretGitHubToken])
	lf = r.resolveWorkflowIfNeeded(ctx, lf, def, secretsEnv[cliSecretGitHubToken])
	lf = r.resolveResourceIfNeeded(ctx, lf, def, secretsEnv[cliSecretGitHubToken])

	var lastCreateOutput, lastDeleteOutput string
	hooks := &reconcile.ReconcileHooks{
		OnEvent: func(eventType, reason, message string) {
			if r.Recorder != nil {
				r.Recorder.Event(&cr, eventType, reason, message)
			}
		},
		OnOperationOutput: func(op module.OperationType, output string) {
			switch op {
			case module.OperationCreate:
				lastCreateOutput = output
			case module.OperationDelete:
				lastDeleteOutput = output
			}
		},
	}
	reconcileErr := r.Reconciler.ReconcileOne(ctx, def, lf, false, secretsEnv, hooks)

	// Re-fetch before the status update: ReconcileOne may have driven a
	// SaveClusterDefinition call (via CRDStateProvider) that already
	// touched .status, and updating a stale copy here would silently
	// revert that write (last-write-wins on a stale resourceVersion is
	// exactly the API server rejects, but only if we bothered to check —
	// re-fetching sidesteps needing to reconcile the two writes by hand).
	if err := r.Client.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			// Deleted mid-reconcile (e.g. NOT_FOUND-status cleanup path
			// removed it via RemoveClusterFile) — nothing left to update.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("re-get ClusterDefinition before status update: %w", err)
	}

	// Only overwrite whichever of these actually fired this cycle — most
	// reconciles hit neither (an ACTIVE, unchanged cluster runs no create/
	// delete op at all), and re-fetched cr above already carries whatever
	// was persisted on a previous cycle that did.
	if lastCreateOutput != "" {
		cr.Status.LastCreateOutput = lastCreateOutput
	}
	if lastDeleteOutput != "" {
		cr.Status.LastDeleteOutput = lastDeleteOutput
	}

	cond := metav1.Condition{Type: hyvev1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: "Reconciled", Message: "last reconcile succeeded"}
	if reconcileErr != nil {
		cond = metav1.Condition{Type: hyvev1alpha1.ConditionTypeError, Status: metav1.ConditionTrue, Reason: "ReconcileFailed", Message: reconcileErr.Error()}
	}
	if err := r.setCondition(ctx, &cr, cr.Generation, cond); err != nil {
		log.Printf("[%s] Warning: failed to update status: %v", cr.Name, err)
	}

	if reconcileErr != nil {
		// Returning the error lets controller-runtime's default
		// exponential backoff handle retry timing; resyncInterval below
		// only applies to the error-free path.
		return ctrl.Result{}, reconcileErr
	}
	return ctrl.Result{RequeueAfter: resyncInterval}, nil
}

// reconcileDelete runs cleanup for a ClusterDefinition with a
// deletionTimestamp set (a real `kubectl delete` was issued) and, once
// ReconcileOne's delete path completes without error, removes
// ClusterDefinitionFinalizer so Kubernetes' already-pending deletion can
// finish. crdconv.ToTypesClusterDefinition already sets the converted def's
// Spec.Delete to true whenever DeletionTimestamp is non-nil, so
// ReconcileOne needs no special-casing to know this is a delete — it's
// dispatched through exactly the same case cluster.Spec.Delete branch a
// file-mode spec.delete:true cluster would hit.
func (r *ClusterDefinitionReconciler) reconcileDelete(ctx context.Context, cr *hyvev1alpha1.ClusterDefinition) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, hyvev1alpha1.ClusterDefinitionFinalizer) {
		return ctrl.Result{}, nil
	}

	def := crdconv.ToTypesClusterDefinition(cr)

	lf, err := module.LoadLockFile(r.StateProvider.LocalPath())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("load hyve.lock: %w", err)
	}
	secretsEnv := r.fetchCLISecrets(ctx)
	lf = r.resolveModuleIfNeeded(ctx, lf, def.Spec.Driver.Source, def.Spec.Driver.Version, secretsEnv[cliSecretGitHubToken])
	lf = r.resolveWorkflowIfNeeded(ctx, lf, def, secretsEnv[cliSecretGitHubToken])
	lf = r.resolveResourceIfNeeded(ctx, lf, def, secretsEnv[cliSecretGitHubToken])

	var lastDeleteOutput string
	hooks := &reconcile.ReconcileHooks{
		OnEvent: func(eventType, reason, message string) {
			if r.Recorder != nil {
				r.Recorder.Event(cr, eventType, reason, message)
			}
		},
		OnOperationOutput: func(op module.OperationType, output string) {
			if op == module.OperationDelete {
				lastDeleteOutput = output
			}
		},
	}

	reconcileErr := r.Reconciler.ReconcileOne(ctx, def, lf, false, secretsEnv, hooks)
	if lastDeleteOutput != "" {
		if err := r.recordDeleteOutput(ctx, client.ObjectKeyFromObject(cr), lastDeleteOutput, reconcileErr); err != nil {
			log.Printf("[%s] Warning: failed to record delete output: %v", cr.Name, err)
		}
	}
	if reconcileErr != nil {
		return ctrl.Result{}, fmt.Errorf("delete cleanup: %w", reconcileErr)
	}

	// Re-fetch: the object may already be gone if ReconcileOne's delete
	// path (via RemoveClusterFile) issued its own client.Delete — in a
	// namespace with no other finalizers that could already have removed
	// it outright, in which case there's no finalizer left to clear.
	var fresh hyvev1alpha1.ClusterDefinition
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(cr), &fresh); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("re-get ClusterDefinition before removing finalizer: %w", err)
	}
	controllerutil.RemoveFinalizer(&fresh, hyvev1alpha1.ClusterDefinitionFinalizer)
	if err := r.Client.Update(ctx, &fresh); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// recordDeleteOutput persists a captured OperationDelete raw output (see
// module.OperationResult.RawOutput, threaded here via ReconcileHooks.
// OnOperationOutput) onto key's own ClusterDefinitionStatus.LastDeleteOutput
// — re-fetching first since reconcileDelete's own cr may be stale by the
// time ReconcileOne returns (mirrors Reconcile()'s identical re-fetch-
// before-status-update reasoning). deleteErr, when non-nil, also upserts an
// Error condition via setCondition (which performs the actual Status()
// .Update(), covering LastDeleteOutput in the same write) so a delete that
// fails — and so never clears its finalizer, leaving the object stuck
// indefinitely — is diagnosable through status/conditions, not just the
// Warning event hooks.OnEvent already emitted.
func (r *ClusterDefinitionReconciler) recordDeleteOutput(ctx context.Context, key client.ObjectKey, output string, deleteErr error) error {
	var fresh hyvev1alpha1.ClusterDefinition
	if err := r.Client.Get(ctx, key, &fresh); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	fresh.Status.LastDeleteOutput = output
	if deleteErr != nil {
		cond := metav1.Condition{Type: hyvev1alpha1.ConditionTypeError, Status: metav1.ConditionTrue, Reason: "DeleteFailed", Message: deleteErr.Error()}
		return r.setCondition(ctx, &fresh, fresh.Generation, cond)
	}
	return r.Client.Status().Update(ctx, &fresh)
}

// setCondition upserts cond into cr.Status.Conditions (replacing any
// existing entry of the same Type) and sets ObservedGeneration, then writes
// both via the status subresource. LastTransitionTime is only bumped when
// Status actually changes, matching the standard meta/v1 Condition
// convention.
func (r *ClusterDefinitionReconciler) setCondition(ctx context.Context, cr *hyvev1alpha1.ClusterDefinition, generation int64, cond metav1.Condition) error {
	cond.LastTransitionTime = metav1.Now()
	replaced := false
	for i, existing := range cr.Status.Conditions {
		if existing.Type == cond.Type {
			if existing.Status == cond.Status {
				cond.LastTransitionTime = existing.LastTransitionTime
			}
			cr.Status.Conditions[i] = cond
			replaced = true
			break
		}
	}
	if !replaced {
		cr.Status.Conditions = append(cr.Status.Conditions, cond)
	}
	cr.Status.ObservedGeneration = generation
	return r.Client.Status().Update(ctx, cr)
}

// fetchCLISecrets reads the shared hyve-cli-secrets Secret once per
// Reconcile/reconcileDelete call and returns its contents as a plain map —
// apierrors.IsNotFound is treated as "not configured yet" (an empty map),
// matching the same soft-failure stance the rest of cluster mode already
// takes for optional configuration (e.g. HyveConfig itself). This map is
// then threaded explicitly through resolveModuleIfNeeded (for
// GITHUB_TOKEN) and ReconcileOne (for every module/workflow Job's env) —
// never stashed on a shared, mutable field, since MaxConcurrentReconciles
// already permits concurrent reconciles of different ClusterDefinitions in
// this one process.
func (r *ClusterDefinitionReconciler) fetchCLISecrets(ctx context.Context) map[string]string {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	return fetchCLISecretsFrom(ctx, reader, r.Namespace)
}

// fetchCLISecretsFrom is the shared implementation behind
// ClusterDefinitionReconciler.fetchCLISecrets and
// WorkflowRunReconciler.fetchCLISecrets — see the former's doc comment for
// why an uncached reader is used and why this is a live, once-per-reconcile
// fetch rather than a cached field.
func fetchCLISecretsFrom(ctx context.Context, reader client.Reader, namespace string) map[string]string {
	var secret corev1.Secret
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cliSecretsName}, &secret); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Printf("Warning: failed to read %s Secret: %v", cliSecretsName, err)
		}
		return nil
	}
	out := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		out[k] = string(v)
	}
	return out
}

// resolveModuleIfNeeded calls module.EnsureResolvedWithToken when
// source@version isn't already locked in lf, and records the outcome on a
// Module CR either way — a plain write from this same reconcile pass, not a
// second watch loop (see this session's design discussion on why: no
// separate human-run install step exists in cluster mode, so the
// controller has to be self-sufficient, but the Module CR still gives the
// same visibility local mode's hyve.lock gives on disk). githubToken, when
// non-empty, is the live-fetched hyve-cli-secrets GITHUB_TOKEN value (see
// fetchCLISecrets) — passed explicitly rather than via os.Setenv, which
// would race under MaxConcurrentReconciles > 1; empty falls back to the
// process's own GITHUB_TOKEN env var exactly as local/CLI mode always has
// (see module.resolveGitHubToken). Never returns an error: a resolution
// failure here is intentionally left to surface through the existing
// shared validateDriverModuleLocked check inside ReconcileOne, so the
// ClusterDefinition's own condition message is unchanged — this just adds
// a second, richer place to look. Returns the (possibly reloaded) lock
// file for the caller to pass into ReconcileOne.
func (r *ClusterDefinitionReconciler) resolveModuleIfNeeded(ctx context.Context, lf *module.LockFile, source, version, githubToken string) *module.LockFile {
	if source == "" || lf.GetLocked(source, version) != nil {
		return lf
	}

	mod := &hyvev1alpha1.Module{ObjectMeta: metav1.ObjectMeta{Name: module.CRName(source, version), Namespace: r.Namespace}}
	mod.Spec = hyvev1alpha1.ModuleSpec{Source: source, Version: version}

	if _, err := module.EnsureResolvedWithToken(r.StateProvider.LocalPath(), source, version, githubToken); err != nil {
		mod.Status = hyvev1alpha1.ModuleStatus{Resolved: false, Error: err.Error()}
		r.upsertModuleCR(ctx, mod)
		return lf // unchanged — the shared check below will still fail, as today
	}

	reloaded, err := module.LoadLockFile(r.StateProvider.LocalPath())
	if err != nil {
		log.Printf("Warning: module %s@%s resolved but failed to reload hyve.lock: %v", source, version, err)
		return lf
	}
	mod.Status = hyvev1alpha1.ModuleStatus{Resolved: true, ResolvedAt: metav1.Now()}
	if locked := reloaded.GetLocked(source, version); locked != nil {
		mod.Status.SHA256 = locked.SHA256
	}
	r.upsertModuleCR(ctx, mod)
	return reloaded
}

// resolveWorkflowIfNeeded resolves every remote WorkflowRef across def's
// lifecycle hooks (see reconcile.AllWorkflowHookRefs) that isn't already
// locked in lf — cluster mode's equivalent of resolveModuleIfNeeded, for
// the identical reason: no separate human-run install step exists here.
// `hyve workflow install` (the CLI command that does this in local mode)
// is explicitly disabled in cluster mode today (cmd/workflow/install.go:
// "hyve.lock-based remote-ref resolution is a local-checkout concept
// only") — this closes that gap by resolving inline, per-reconcile,
// exactly like resolveModuleIfNeeded already does for driver modules.
//
// Reuses workflowref.Install directly rather than reimplementing per-ref
// resolution: it already short-circuits to a no-op for any ref whose
// content hasn't changed since it was last locked (Resolve's own SHA256
// cache-hit check), so calling it with the full set of hook refs on every
// reconcile is cheap once everything's resolved — no network at all after
// the first successful pass, matching the "resolve once, cache forever
// until content changes" model hyve.lock already uses everywhere else.
//
// githubToken, when non-empty, is the live-fetched hyve-cli-secrets
// GITHUB_TOKEN value (see fetchCLISecrets) — passed explicitly rather than
// via os.Setenv, which would race under MaxConcurrentReconciles > 1; empty
// falls back to the process's own GITHUB_TOKEN env var exactly as
// resolveModuleIfNeeded's own githubToken param does (see
// module.ResolveToken). This is what makes workflowref.FetchRepoArchive's
// git clone able to authenticate against a private repo in cluster mode at
// all — previously no token reached this call site.
//
// Also mirrors every resolved ref's outcome onto a WorkflowRefStatus CR —
// one per reconcile, regardless of whether anything actually changed —
// exactly like resolveModuleIfNeeded's Module CR, and for the identical
// reason: hyve.lock lives on the controller's own local checkout, invisible
// to kubectl/hyve workflow list; the CR is what makes it discoverable (see
// WorkflowRefStatus's own doc comment).
//
// Never returns an error: a resolution failure here is intentionally left
// to surface through the existing shared validateWorkflowRefsLocked check
// inside ReconcileOne, matching resolveModuleIfNeeded's own stance exactly.
func (r *ClusterDefinitionReconciler) resolveWorkflowIfNeeded(ctx context.Context, lf *module.LockFile, def types.ClusterDefinition, githubToken string) *module.LockFile {
	var refs []types.WorkflowRef
	for _, ref := range reconcile.AllWorkflowHookRefs(def) {
		if ref.IsRemote() {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return lf
	}

	_, _, resolveErrors, results, changed, err := workflowref.Install(r.StateProvider.LocalPath(), refs, githubToken)
	if err != nil {
		log.Printf("[%s] Warning: failed to resolve workflow refs: %v", def.Metadata.Name, err)
		return lf
	}
	for _, e := range resolveErrors {
		log.Printf("[%s] Warning: failed to resolve workflow ref: %s", def.Metadata.Name, e)
	}
	// results covers every ref this call attempted, changed or not — mirror
	// all of them before the changed-only early return below, since the
	// common steady-state (nothing changed) case is exactly what CR
	// mirroring needs to run on every reconcile.
	for _, res := range results {
		r.upsertWorkflowRefStatusCR(ctx, res)
	}
	if !changed {
		return lf
	}

	reloaded, err := module.LoadLockFile(r.StateProvider.LocalPath())
	if err != nil {
		log.Printf("[%s] Warning: workflow refs resolved but failed to reload hyve.lock: %v", def.Metadata.Name, err)
		return lf
	}
	return reloaded
}

// resolveResourceIfNeeded mirrors resolveWorkflowIfNeeded exactly, one tier
// below it: it resolves every remote ResourceRef in def.Spec.Resources
// that isn't already locked in lf. Simpler than resolveWorkflowIfNeeded's
// ref-gathering — resources have no lifecycle-hook fan-out to scan, just
// the one flat def.Spec.Resources list. Also mirrors every resolved ref's
// outcome onto a ResourceRefStatus CR every reconcile — see
// resolveWorkflowIfNeeded's own doc comment for why.
func (r *ClusterDefinitionReconciler) resolveResourceIfNeeded(ctx context.Context, lf *module.LockFile, def types.ClusterDefinition, githubToken string) *module.LockFile {
	var refs []types.ResourceRef
	for _, ref := range def.Spec.Resources {
		if ref.IsRemote() {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return lf
	}

	_, _, resolveErrors, results, changed, err := resourceref.Install(r.StateProvider.LocalPath(), refs, githubToken)
	if err != nil {
		log.Printf("[%s] Warning: failed to resolve resource refs: %v", def.Metadata.Name, err)
		return lf
	}
	for _, e := range resolveErrors {
		log.Printf("[%s] Warning: failed to resolve resource ref: %s", def.Metadata.Name, e)
	}
	for _, res := range results {
		r.upsertResourceRefStatusCR(ctx, res)
	}
	if !changed {
		return lf
	}

	reloaded, err := module.LoadLockFile(r.StateProvider.LocalPath())
	if err != nil {
		log.Printf("[%s] Warning: resource refs resolved but failed to reload hyve.lock: %v", def.Metadata.Name, err)
		return lf
	}
	return reloaded
}

// upsertModuleCR creates want, or updates an existing Module CR of the same
// name to match it — spec and status both. desiredStatus is saved and
// re-applied immediately before each Status().Update() call rather than
// trusted to survive on want/existing themselves: both Create() and
// Update() unmarshal the API server's full response back into the passed
// object (status subresource enabled means that response's status is
// whatever the server already had — zero on a fresh Create, the pre-update
// value on a plain Update), silently clobbering whatever status was set
// beforehand — confirmed live, this was the actual first-cut bug (every
// Module CR's status came back completely empty).
func (r *ClusterDefinitionReconciler) upsertModuleCR(ctx context.Context, want *hyvev1alpha1.Module) {
	desiredStatus := want.Status

	var existing hyvev1alpha1.Module
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(want), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Client.Create(ctx, want); err != nil {
			log.Printf("Warning: failed to create Module CR %s: %v", want.Name, err)
			return
		}
		want.Status = desiredStatus
		if err := r.Client.Status().Update(ctx, want); err != nil {
			log.Printf("Warning: failed to set Module CR %s status: %v", want.Name, err)
		}
		return
	}
	if err != nil {
		log.Printf("Warning: failed to get Module CR %s: %v", want.Name, err)
		return
	}
	existing.Spec = want.Spec
	if err := r.Client.Update(ctx, &existing); err != nil {
		log.Printf("Warning: failed to update Module CR %s: %v", want.Name, err)
		return
	}
	existing.Status = desiredStatus
	if err := r.Client.Status().Update(ctx, &existing); err != nil {
		log.Printf("Warning: failed to update Module CR %s status: %v", want.Name, err)
	}
}

// upsertWorkflowRefStatusCR builds and upserts one WorkflowRefStatus CR from
// a single workflowref.RefResult — same Get→Create-with-status-update /
// Get→Update-spec→Update-status flow as upsertModuleCR, for the identical
// reason (status subresource writes need the desired status re-applied
// after Create/Update, since both echo back whatever the server already
// had). metadata.name is a derived slug (module.CRName), not res.Name
// directly, since two different sources can legitimately share the same
// short Name (see workflowref.NameCollision) — spec.name carries the
// human-facing identity instead.
//
// Called on every reconcile for every remote ref, including the common
// steady-state case where nothing about it changed (see
// resolveWorkflowIfNeeded's own doc comment for why) — so an existing CR
// whose status already matches is left untouched rather than getting a
// fresh ResolvedAt and a Status().Update() every single cycle; only an
// actual content/error change (or a brand new CR) writes anything.
func (r *ClusterDefinitionReconciler) upsertWorkflowRefStatusCR(ctx context.Context, res workflowref.RefResult) {
	want := &hyvev1alpha1.WorkflowRefStatus{
		ObjectMeta: metav1.ObjectMeta{Name: module.CRName(res.Name, res.CanonicalSource), Namespace: r.Namespace},
		Spec:       hyvev1alpha1.WorkflowRefStatusSpec{Name: res.Name, Source: res.CanonicalSource},
	}
	if res.Err != nil {
		want.Status = hyvev1alpha1.WorkflowRefStatusStatus{Resolved: false, Error: res.Err.Error(), RawVersion: res.RawVersion}
	} else {
		want.Status = hyvev1alpha1.WorkflowRefStatusStatus{
			Resolved: true, SHA256: res.SHA256, RawVersion: res.RawVersion, ResolvedVersion: res.ResolvedVersion,
		}
	}

	desiredStatus := want.Status
	var existing hyvev1alpha1.WorkflowRefStatus
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(want), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Client.Create(ctx, want); err != nil {
			log.Printf("Warning: failed to create WorkflowRefStatus CR %s: %v", want.Name, err)
			return
		}
		desiredStatus.ResolvedAt = metav1.Now()
		want.Status = desiredStatus
		if err := r.Client.Status().Update(ctx, want); err != nil {
			log.Printf("Warning: failed to set WorkflowRefStatus CR %s status: %v", want.Name, err)
		}
		return
	}
	if err != nil {
		log.Printf("Warning: failed to get WorkflowRefStatus CR %s: %v", want.Name, err)
		return
	}
	if statusUnchanged := existing.Status.Resolved == desiredStatus.Resolved &&
		existing.Status.RawVersion == desiredStatus.RawVersion &&
		existing.Status.ResolvedVersion == desiredStatus.ResolvedVersion &&
		existing.Status.SHA256 == desiredStatus.SHA256 &&
		existing.Status.Error == desiredStatus.Error; statusUnchanged {
		return
	}
	existing.Spec = want.Spec
	if err := r.Client.Update(ctx, &existing); err != nil {
		log.Printf("Warning: failed to update WorkflowRefStatus CR %s: %v", want.Name, err)
		return
	}
	desiredStatus.ResolvedAt = metav1.Now()
	existing.Status = desiredStatus
	if err := r.Client.Status().Update(ctx, &existing); err != nil {
		log.Printf("Warning: failed to update WorkflowRefStatus CR %s status: %v", want.Name, err)
	}
}

// upsertResourceRefStatusCR mirrors upsertWorkflowRefStatusCR exactly, one
// tier below it — see its doc comment, including the no-op-on-unchanged-
// status skip.
func (r *ClusterDefinitionReconciler) upsertResourceRefStatusCR(ctx context.Context, res resourceref.RefResult) {
	want := &hyvev1alpha1.ResourceRefStatus{
		ObjectMeta: metav1.ObjectMeta{Name: module.CRName(res.Name, res.CanonicalSource), Namespace: r.Namespace},
		Spec:       hyvev1alpha1.ResourceRefStatusSpec{Name: res.Name, Source: res.CanonicalSource},
	}
	if res.Err != nil {
		want.Status = hyvev1alpha1.ResourceRefStatusStatus{Resolved: false, Error: res.Err.Error(), RawVersion: res.RawVersion}
	} else {
		want.Status = hyvev1alpha1.ResourceRefStatusStatus{Resolved: true, SHA256: res.SHA256, RawVersion: res.RawVersion}
	}

	desiredStatus := want.Status
	var existing hyvev1alpha1.ResourceRefStatus
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(want), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Client.Create(ctx, want); err != nil {
			log.Printf("Warning: failed to create ResourceRefStatus CR %s: %v", want.Name, err)
			return
		}
		desiredStatus.ResolvedAt = metav1.Now()
		want.Status = desiredStatus
		if err := r.Client.Status().Update(ctx, want); err != nil {
			log.Printf("Warning: failed to set ResourceRefStatus CR %s status: %v", want.Name, err)
		}
		return
	}
	if err != nil {
		log.Printf("Warning: failed to get ResourceRefStatus CR %s: %v", want.Name, err)
		return
	}
	if statusUnchanged := existing.Status.Resolved == desiredStatus.Resolved &&
		existing.Status.RawVersion == desiredStatus.RawVersion &&
		existing.Status.SHA256 == desiredStatus.SHA256 &&
		existing.Status.Error == desiredStatus.Error; statusUnchanged {
		return
	}
	existing.Spec = want.Spec
	if err := r.Client.Update(ctx, &existing); err != nil {
		log.Printf("Warning: failed to update ResourceRefStatus CR %s: %v", want.Name, err)
		return
	}
	desiredStatus.ResolvedAt = metav1.Now()
	existing.Status = desiredStatus
	if err := r.Client.Status().Update(ctx, &existing); err != nil {
		log.Printf("Warning: failed to update ResourceRefStatus CR %s status: %v", want.Name, err)
	}
}

// SetupWithManager wires this reconciler into mgr, watching ClusterDefinition.
func (r *ClusterDefinitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("hyve-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyvev1alpha1.ClusterDefinition{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
