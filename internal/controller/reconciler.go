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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

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

	reconcileErr := r.Reconciler.ReconcileOne(ctx, def, lf, false)

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

	if err := r.Reconciler.ReconcileOne(ctx, def, lf, false); err != nil {
		return ctrl.Result{}, fmt.Errorf("delete cleanup: %w", err)
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

// SetupWithManager wires this reconciler into mgr, watching ClusterDefinition.
func (r *ClusterDefinitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyvev1alpha1.ClusterDefinition{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
