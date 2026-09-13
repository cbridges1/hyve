package controller

import (
	"context"
	"fmt"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// namespaceDeletionRequeueInterval is how long NamespaceReconciler waits
// before re-checking a Namespace whose hyve-owned objects aren't all gone
// yet. Deleting one of those objects (a ClusterDefinition still running
// its own delete cleanup via ClusterDefinitionFinalizer, most notably)
// doesn't generate a watch event on the Namespace object itself, so
// there's nothing to re-trigger Reconcile the moment that finishes —
// requeueing is what "already has retry/backoff machinery" (see this
// type's own doc comment) actually means in practice here, rather than a
// second set of Watches() mapping every owned kind back to its Namespace.
const namespaceDeletionRequeueInterval = 10 * time.Second

// NamespaceReconciler implements the other half of Milestone 5's
// organization-deletion design (see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's
// "Organization deletion" section, nexus-config/docs): internal/api's
// handleDeleteOrganization marks the organization's Store row
// pending_deletion and issues `kubectl delete namespace`, and this
// reconciler — already watching every hyve.io kind that can live in that
// namespace — is what actually clears
// hyvev1alpha1.OrganizationNamespaceFinalizer once it's safe to, letting
// Kubernetes finish removing the Namespace object for real. Registered on
// the same manager as ClusterDefinitionReconciler/WorkflowRunReconciler —
// no new process. Deliberately never touches internal/orgdb.Store: the
// API server's own periodic sweep (Server.SweepPendingOrganizationDeletions),
// not this reconciler, is what deletes the organization's row once the
// Namespace is fully gone — the controller's existing, load-bearing
// "never touches Postgres" boundary stays intact.
//
// Namespace is cluster-scoped, unlike every other kind this controller
// reconciles — there's no RBAC mechanism to restrict its watch/list to
// only this install's own tenant namespaces (see
// deploy/helm/hyve/templates/controller-rbac.yaml's own comment on this),
// so Reconcile necessarily receives requests for every namespace on the
// cluster, not just organization ones. The finalizer/DeletionTimestamp
// check below is what makes every irrelevant one a cheap no-op.
type NamespaceReconciler struct {
	Client client.Client

	// APIReader, when set, is used instead of Client for
	// hyveOwnedObjectsGone's own List calls — an uncached, direct-to-
	// API-server read (mgr.GetAPIReader(), same pattern
	// ClusterDefinitionReconciler.fetchCLISecrets already uses, see that
	// field's own doc comment). This is not optional the way it is there:
	// the manager's cache is deliberately restricted to this install's own
	// control-plane namespace only (cmd/controller/run.go's
	// cache.Options.DefaultNamespaces — Phase 1's one-controller-
	// reconciles-its-own-namespace architecture, unchanged by this
	// milestone), so a cached List scoped to an organization's own
	// namespace would find nothing there at all, regardless of what
	// actually exists — silently defeating the entire safety property this
	// reconciler exists for. An uncached read bypasses the cache's
	// namespace restriction; it still needs its own narrow cluster-wide
	// get/list RBAC (deploy/helm/hyve/templates/controller-rbac.yaml),
	// deliberately not the cache-populating watch a real For()/Owns()
	// controller would need, and deliberately not folded into widening the
	// shared cache itself — doing that would also change what
	// ClusterDefinitionReconciler/WorkflowRunReconciler each watch and
	// reconcile, a real Milestone 6 ("per-organization reconciling
	// cluster") change, not this one. Falls back to Client if left nil
	// (tests construct NamespaceReconciler directly without a real
	// manager).
	APIReader client.Reader
}

// Reconcile implements the controller-runtime reconcile loop for one
// Namespace.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ns corev1.Namespace
	if err := r.Client.Get(ctx, req.NamespacedName, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get Namespace: %w", err)
	}

	if ns.DeletionTimestamp == nil || !controllerutil.ContainsFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer) {
		return ctrl.Result{}, nil
	}

	empty, err := r.hyveOwnedObjectsGone(ctx, ns.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check hyve-owned objects in %q: %w", ns.Name, err)
	}
	if !empty {
		return ctrl.Result{RequeueAfter: namespaceDeletionRequeueInterval}, nil
	}

	controllerutil.RemoveFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer)
	if err := r.Client.Update(ctx, &ns); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// hyveOwnedObjectsGone checks the four namespaced hyve.io kinds this
// controller actually reconciles (ClusterDefinition, Workflow, Resource,
// WorkflowRun) for anything still left in namespace. Template is
// deliberately not checked: it's a plain CRUD record with no reconcile
// loop and no finalizer of its own (see deploy/helm/hyve/templates/
// controller-rbac.yaml, which grants this controller no access to it at
// all), so Kubernetes' own namespace-content cascade — which proceeds
// independently of hyvev1alpha1.OrganizationNamespaceFinalizer, since that
// finalizer only blocks the Namespace object's own final removal, not the
// cascade through its contents — removes it with nothing to wait for.
// ClusterDefinition is the one kind here with a finalizer of its own
// (ClusterDefinitionFinalizer) that can keep it present, running real
// cleanup, for a nontrivial time after the Namespace enters Terminating;
// the other three are checked defensively for the same "every hyve-owned
// object... confirmed gone" property even though none of them currently
// carry a finalizer that could delay their own removal.
func (r *NamespaceReconciler) hyveOwnedObjectsGone(ctx context.Context, namespace string) (bool, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}

	var cds hyvev1alpha1.ClusterDefinitionList
	if err := reader.List(ctx, &cds, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list ClusterDefinitions: %w", err)
	}
	if len(cds.Items) > 0 {
		return false, nil
	}

	var wfs hyvev1alpha1.WorkflowList
	if err := reader.List(ctx, &wfs, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list Workflows: %w", err)
	}
	if len(wfs.Items) > 0 {
		return false, nil
	}

	var res hyvev1alpha1.ResourceList
	if err := reader.List(ctx, &res, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list Resources: %w", err)
	}
	if len(res.Items) > 0 {
		return false, nil
	}

	var wrs hyvev1alpha1.WorkflowRunList
	if err := reader.List(ctx, &wrs, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("list WorkflowRuns: %w", err)
	}
	return len(wrs.Items) == 0, nil
}

// SetupWithManager wires this reconciler into mgr, watching Namespace.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Complete(r)
}
