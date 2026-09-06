package controller

import (
	"context"
	"fmt"
	"log"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/reconcile"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WorkflowRunReconciler drives one WorkflowRun CR to completion by calling
// reconcile.Reconciler.RunAdHocWorkflow exactly once — "same engine"
// lifecycle hooks already run through (see that method's own doc comment),
// just invoked directly instead of as part of a ClusterDefinition's own
// create/delete/drift cycle. Unlike ClusterDefinitionReconciler, this never
// requeues and has no finalizer/delete handling: a WorkflowRun is a one-shot
// request, not a standing declaration — once its phase leaves
// Pending/"", this reconciler has nothing left to do (see WorkflowRun's own
// doc comment on why it's never garbage-collected either).
type WorkflowRunReconciler struct {
	Client        client.Client
	APIReader     client.Reader
	Reconciler    *reconcile.Reconciler
	StateProvider *CRDStateProvider
	Namespace     string
}

// Reconcile implements the controller-runtime reconcile loop for one
// WorkflowRun.
func (r *WorkflowRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var wr hyvev1alpha1.WorkflowRun
	if err := r.Client.Get(ctx, req.NamespacedName, &wr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get WorkflowRun: %w", err)
	}

	// Already picked up (or finished) — a WorkflowRun executes exactly
	// once, so any phase past Pending/"" means there's nothing left to do.
	// This also protects against a spurious re-reconcile mid-run (e.g. this
	// process's own Status().Update calls below trigger a fresh watch
	// event) re-entering and running the same workflow twice.
	if wr.Status.Phase != "" && wr.Status.Phase != hyvev1alpha1.WorkflowRunPhasePending {
		return ctrl.Result{}, nil
	}

	now := metav1.Now()
	wr.Status.Phase = hyvev1alpha1.WorkflowRunPhaseRunning
	wr.Status.StartedAt = &now
	wr.Status.Message = ""
	if err := r.Client.Status().Update(ctx, &wr); err != nil {
		return ctrl.Result{}, fmt.Errorf("set Running status: %w", err)
	}

	var clusterCR hyvev1alpha1.ClusterDefinition
	if err := r.Client.Get(ctx, k8stypes.NamespacedName{Namespace: r.Namespace, Name: wr.Spec.ClusterRef}, &clusterCR); err != nil {
		return r.finish(ctx, &wr, "", fmt.Errorf("cluster %q not found: %w", wr.Spec.ClusterRef, err))
	}
	def := crdconv.ToTypesClusterDefinition(&clusterCR)

	lf, err := module.LoadLockFile(r.StateProvider.LocalPath())
	if err != nil {
		return r.finish(ctx, &wr, "", fmt.Errorf("load hyve.lock: %w", err))
	}

	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	secretsEnv := fetchCLISecretsFrom(ctx, reader, r.Namespace)

	ref := crdconv.ToTypesWorkflowRef(wr.Spec.WorkflowRef)
	output, runErr := r.Reconciler.RunAdHocWorkflow(ctx, def, ref, wr.Spec.Params, lf, secretsEnv)
	return r.finish(ctx, &wr, output, runErr)
}

// finish writes wr's terminal status (Succeeded/Failed) — never returns an
// error that would make controller-runtime requeue, since a WorkflowRun is
// one-shot: a failed run stays Failed, visible via `kubectl get
// workflowruns.hyve.io`/`hyve workflow run`'s own polling, not silently
// retried.
func (r *WorkflowRunReconciler) finish(ctx context.Context, wr *hyvev1alpha1.WorkflowRun, output string, runErr error) (ctrl.Result, error) {
	now := metav1.Now()
	wr.Status.CompletedAt = &now
	wr.Status.Output = output
	if runErr != nil {
		wr.Status.Phase = hyvev1alpha1.WorkflowRunPhaseFailed
		wr.Status.Message = runErr.Error()
	} else {
		wr.Status.Phase = hyvev1alpha1.WorkflowRunPhaseSucceeded
		wr.Status.Message = "workflow completed"
	}
	if err := r.Client.Status().Update(ctx, wr); err != nil {
		log.Printf("Warning: failed to update WorkflowRun %s status: %v", wr.Name, err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager wires this reconciler into mgr, watching WorkflowRun.
func (r *WorkflowRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyvev1alpha1.WorkflowRun{}).
		Complete(r)
}
