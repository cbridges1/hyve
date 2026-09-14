package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	internalcontroller "github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/migrate"
	"github.com/cbridges1/hyve/internal/orgdb"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// registerOrganizationRoutes wires POST/GET /organizations — mounted under
// /api/ (behind requireAuth+requireRole) by Server.Routes. Replaces the
// former POST/GET /environments (internal/api/environments.go, deleted) as
// of HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 2 — see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md (nexus-config/docs) for why: the
// business record (name, plan, org-id<->namespace mapping) now lives in
// Server.OrgStore (Postgres/SQLite), not a HyveEnvironment CRD, alongside
// the new "an organization has multiple environments" capability that CRD
// never supported at all.
func (s *Server) registerOrganizationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /organizations", s.handleCreateOrganization)
	mux.HandleFunc("GET /organizations", s.handleListOrganizations)
	mux.HandleFunc("PATCH /organizations/{name}", s.handlePatchOrganization)
	mux.HandleFunc("DELETE /organizations/{name}", s.handleDeleteOrganization)
	mux.HandleFunc("POST /organizations/{name}/environments", s.handleCreateOrgEnvironment)
	mux.HandleFunc("GET /organizations/{name}/environments", s.handleListOrgEnvironments)
}

type organizationDTO struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Plan      string `json:"plan,omitempty"`

	// ReconcilingCluster is the registered cluster (POST /reconciling-clusters)
	// this organization's own Namespace and four resource types currently
	// live on (Milestone 6) — empty means the control plane's own home
	// cluster, never a raw id (a UI has no other way to show something
	// meaningful; the id alone means nothing to a human).
	ReconcilingCluster string `json:"reconcilingCluster,omitempty"`
	// Migrating reflects reconciling_cluster_migration_status != nil — every
	// request against this organization's own resource types currently gets
	// 423 (see requireOrganizationNotMigrating) until the in-flight
	// PATCH /organizations/{name} migration completes.
	Migrating bool `json:"migrating,omitempty"`
}

// toOrganizationDTO resolves org's own reconciling cluster name for
// display — a method, not a plain function, since org.ReconcilingClusterID
// is only ever an id; showing something meaningful means a Store lookup.
// A lookup failure is logged but never fails the request: this is a
// cosmetic display detail, not something an otherwise-successful org
// list/create/patch response should 500 over.
func (s *Server) toOrganizationDTO(ctx context.Context, org orgdb.Organization) organizationDTO {
	dto := organizationDTO{
		Name:      org.Name,
		Namespace: org.Namespace,
		Plan:      org.Plan,
		Migrating: org.ReconcilingClusterMigrationStatus != nil,
	}
	if org.ReconcilingClusterID != nil {
		rc, err := s.OrgStore.GetReconcilingCluster(ctx, *org.ReconcilingClusterID)
		if err != nil {
			log.Printf("api: failed to resolve reconciling cluster name for organization %q: %v", org.Name, err)
		} else {
			dto.ReconcilingCluster = rc.Name
		}
	}
	return dto
}

// handleListOrganizations lists every organization — the same list a
// `--namespace hyve-system` migration enumerates, exposed for an admin UI.
// Superadmin-only, same reasoning as create: this is a cross-namespace view
// by definition.
func (s *Server) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	ctx := r.Context()
	orgs, err := s.OrgStore.ListOrganizations(ctx)
	if err != nil {
		log.Printf("api: failed to list organizations: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list organizations")
		return
	}
	out := make([]organizationDTO, 0, len(orgs))
	for _, org := range orgs {
		out = append(out, s.toOrganizationDTO(ctx, org))
	}
	writeJSON(w, http.StatusOK, out)
}

type createOrganizationRequest struct {
	Name string `json:"name"`

	// AdminIdentity/AdminRole, if both set, seed the organization's initial
	// admin Binding row in the same transaction as the organization and
	// its default environment. Optional as of Milestone 2 — nothing reads
	// Binding rows for authorization yet (that's Milestone 4), so omitting
	// these and granting access the pre-existing way still works during
	// the transition.
	AdminIdentity string `json:"adminIdentity,omitempty"`
	AdminRole     string `json:"adminRole,omitempty"`

	// ReconcilingCluster names an already-registered reconciling cluster
	// (POST /reconciling-clusters) this organization's own Namespace and
	// four resource types should live on instead of the control plane's
	// own home cluster (Milestone 6 — see
	// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization reconciling
	// cluster" section). Optional: omitted (or empty) creates the
	// organization on the home cluster, exactly Milestone 2's original
	// behavior.
	ReconcilingCluster string `json:"reconcilingCluster,omitempty"`
}

// handleCreateOrganization turns a namespace into a real, hyve-recognized
// tenant — nothing more. Creating actual login credentials for its first
// user is a separate, already-existing call (POST /accounts). Gated to
// superadmin only: this is the one operation that spans namespaces by
// design.
//
// Each Kubernetes-side step checks "does this already exist" before
// creating, so a re-POST after a partial failure (e.g. the namespace got
// created but the RBAC scaffolding failed) fills in what's missing instead
// of erroring on "already exists" — no rollback needed, matching
// internal/migrate's own objectExists precedent. The organization/
// environment/admin-binding rows themselves are written atomically (one
// Store transaction) before any Kubernetes call is made — see
// CreateOrganizationWithDefaults's own doc comment.
func (s *Server) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	var req createOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateOrganizationName(req.Name, s.Namespace); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if (req.AdminIdentity == "") != (req.AdminRole == "") {
		writeError(w, http.StatusBadRequest, "adminIdentity and adminRole must be set together, or both omitted")
		return
	}

	ctx := r.Context()
	name := req.Name

	var reconcilingClusterID *string
	if req.ReconcilingCluster != "" {
		rc, err := s.OrgStore.GetReconcilingClusterByName(ctx, req.ReconcilingCluster)
		if err == orgdb.ErrNotFound {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("reconciling cluster %q not found", req.ReconcilingCluster))
			return
		} else if err != nil {
			log.Printf("api: failed to look up reconciling cluster %q: %v", req.ReconcilingCluster, err)
			writeError(w, http.StatusInternalServerError, "failed to create organization")
			return
		}
		reconcilingClusterID = &rc.ID
	}

	// Check-then-create, not create-then-handle-conflict: this endpoint is
	// idempotent by design, matching every other "does this already exist"
	// step below it (ensureNamespace/ensureAccessRoleScaffolding) and
	// internal/migrate's own objectExists precedent — a re-POST after a
	// partial failure (the organization/environment rows landed, but a
	// Kubernetes step failed) must fill in what's missing, not 409 just
	// because the Postgres/SQLite side already succeeded.
	org, err := s.OrgStore.GetOrganizationByName(ctx, name)
	switch {
	case err == nil:
		// Already exists — fall through to the Kubernetes-side steps
		// below without touching the Store again.
	case err == orgdb.ErrNotFound:
		org, _, err = s.OrgStore.CreateOrganizationWithDefaults(ctx, orgdb.Organization{Name: name, Namespace: name, ReconcilingClusterID: reconcilingClusterID}, req.AdminIdentity, req.AdminRole)
		if err != nil {
			log.Printf("api: failed to create organization %q: %v", name, err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create organization: %v", err))
			return
		}
	default:
		log.Printf("api: failed to check existing organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to create organization")
		return
	}

	// Resolved once the organization row exists (its own
	// ReconcilingClusterID, just written above, is what this looks up) —
	// so the Namespace/RBAC scaffolding below lands on the right cluster,
	// not always the control plane's own home cluster.
	targetClient, err := s.resourceClient(ctx, name)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("organization record created, but resolving its reconciling cluster failed: %v — retry this same request to finish", err))
		return
	}

	if err := s.ensureNamespace(ctx, targetClient, name); err != nil {
		log.Printf("api: failed to ensure namespace %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("organization record created, but namespace provisioning failed: %v — retry this same request to finish", err))
		return
	}
	if err := s.ensureAccessRoleScaffolding(ctx, targetClient, name); err != nil {
		log.Printf("api: failed to ensure RBAC scaffolding for %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("organization record created, but RBAC scaffolding failed: %v — retry this same request to finish", err))
		return
	}

	writeJSON(w, http.StatusCreated, s.toOrganizationDTO(ctx, org))
}

type patchOrganizationRequest struct {
	// ReconcilingCluster names the reconciling cluster (POST
	// /reconciling-clusters) to move this organization onto — "" moves it
	// back to the control plane's own home cluster. A pointer so an
	// omitted field can be distinguished from an explicit "": this
	// endpoint exists for exactly one purpose (Milestone 6's reconciling-
	// cluster migration), so there's nothing else a PATCH here would mean.
	ReconcilingCluster *string `json:"reconcilingCluster"`
}

// handlePatchOrganization implements Milestone 6's reconciling-cluster
// migration (HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization
// reconciling cluster" section, nexus-config/docs): moves name's Namespace
// and everything hyve-managed inside it — ClusterDefinitions, Templates,
// Workflows, Resources, its own HyveConfig singleton, and (per Milestone
// 4's own scope reduction) credentials Secrets only, never binding rows
// themselves, which live in Store, not on any cluster — from wherever it
// currently reconciles to a different registered reconciling cluster, or
// back to the control plane's own home cluster ("").
//
// Implements the proposal's decided lock-don't-dual-serve design: while
// the copy is in flight, reconciling_cluster_migration_status is
// 'migrating', and requireOrganizationNotMigrating (wired onto every route
// in clusters.go/templates.go/workflows.go/workflowruns.go/resources.go)
// returns 423 for any request against this organization in the meantime.
// The copy itself uses internal/migrate's own primitives directly — the
// same ones `hyve migrate cluster` uses — invoked programmatically rather
// than requiring a human to run the CLI by hand, per the proposal's own
// framing.
//
// A copy failure aborts cleanly: the migration lock is cleared but
// reconciling_cluster_id is left untouched, so the organization stays on
// its original cluster (the source is only ever read, never deleted)
// instead of being left half-moved. This is not automatically retried —
// the caller sees the specific failure (most likely an unreachable
// destination cluster) and can re-issue the identical PATCH once it's
// fixed.
func (s *Server) handlePatchOrganization(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	ctx := r.Context()
	name := r.PathValue("name")

	var req patchOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ReconcilingCluster == nil {
		writeError(w, http.StatusBadRequest, "reconcilingCluster is required")
		return
	}

	org, err := s.OrgStore.GetOrganizationByName(ctx, name)
	if err == orgdb.ErrNotFound {
		writeError(w, http.StatusNotFound, "organization not found")
		return
	} else if err != nil {
		log.Printf("api: failed to get organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to migrate organization")
		return
	}
	if org.PendingDeletion {
		writeError(w, http.StatusConflict, fmt.Sprintf("organization %q is pending deletion", name))
		return
	}
	if org.ReconcilingClusterMigrationStatus != nil {
		writeError(w, http.StatusLocked, fmt.Sprintf("organization %q is already migrating", name))
		return
	}

	var targetClusterID *string
	if *req.ReconcilingCluster != "" {
		rc, err := s.OrgStore.GetReconcilingClusterByName(ctx, *req.ReconcilingCluster)
		if err == orgdb.ErrNotFound {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("reconciling cluster %q not found", *req.ReconcilingCluster))
			return
		} else if err != nil {
			log.Printf("api: failed to look up reconciling cluster %q: %v", *req.ReconcilingCluster, err)
			writeError(w, http.StatusInternalServerError, "failed to migrate organization")
			return
		}
		targetClusterID = &rc.ID
	}

	if reconcilingClusterIDsEqual(org.ReconcilingClusterID, targetClusterID) {
		// Already there — a no-op PATCH, matching this file's own
		// idempotent-by-design precedent elsewhere (handleCreateOrganization,
		// handleDeleteOrganization).
		writeJSON(w, http.StatusOK, s.toOrganizationDTO(ctx, org))
		return
	}

	sourceClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve source reconciling cluster for organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to migrate organization")
		return
	}
	var destClient client.Client
	if targetClusterID == nil {
		destClient = s.Client
	} else {
		handle, err := s.reconcilingClusterClientHandle(ctx, *targetClusterID)
		if err != nil {
			log.Printf("api: failed to resolve destination reconciling cluster for organization %q: %v", name, err)
			writeError(w, http.StatusInternalServerError, "failed to migrate organization")
			return
		}
		destClient = handle.Client
	}

	migrating := "migrating"
	if err := s.OrgStore.SetOrganizationMigrationStatus(ctx, org.ID, &migrating); err != nil {
		log.Printf("api: failed to lock organization %q for migration: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to migrate organization")
		return
	}

	if err := s.runOrganizationMigration(ctx, org, sourceClient, destClient); err != nil {
		log.Printf("api: migration failed for organization %q: %v", name, err)
		if unlockErr := s.OrgStore.SetOrganizationMigrationStatus(ctx, org.ID, nil); unlockErr != nil {
			log.Printf("api: failed to clear migration lock for organization %q after a failed migration: %v", name, unlockErr)
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migration failed, organization left on its original reconciling cluster: %v", err))
		return
	}

	if err := s.OrgStore.SetOrganizationReconcilingCluster(ctx, org.ID, targetClusterID); err != nil {
		log.Printf("api: failed to finalize reconciling cluster for organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "migration data copied, but finalizing the organization's reconciling cluster failed — retry this same request to finish")
		return
	}

	updated, err := s.OrgStore.GetOrganization(ctx, org.ID)
	if err != nil {
		log.Printf("api: failed to reload organization %q after migration: %v", name, err)
		writeError(w, http.StatusInternalServerError, "migration completed, but reloading the organization record failed")
		return
	}
	writeJSON(w, http.StatusOK, s.toOrganizationDTO(ctx, updated))
}

func reconcilingClusterIDsEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// runOrganizationMigration performs the actual copy — Namespace/RBAC
// scaffolding on the destination first, then every namespace-scoped
// object internal/migrate knows how to copy, using the exact same
// primitives `hyve migrate cluster` uses. Any single failure aborts the
// whole operation rather than partially migrating — the same
// stop-don't-partially-commit stance handlePatchOrganization itself takes.
func (s *Server) runOrganizationMigration(ctx context.Context, org orgdb.Organization, sourceClient, destClient client.Client) error {
	if err := s.ensureNamespace(ctx, destClient, org.Namespace); err != nil {
		return fmt.Errorf("provision destination namespace: %w", err)
	}
	if err := s.ensureAccessRoleScaffolding(ctx, destClient, org.Namespace); err != nil {
		return fmt.Errorf("provision destination RBAC: %w", err)
	}

	sourceProvider := &internalcontroller.CRDStateProvider{Client: sourceClient, Namespace: org.Namespace, ConfigName: s.configName()}

	if _, err := migrate.HyveConfig(ctx, sourceProvider, destClient, org.Namespace, s.configName(), false); err != nil {
		return fmt.Errorf("migrate HyveConfig: %w", err)
	}

	clusterSummary, err := migrate.ClusterDefinitions(ctx, sourceProvider, destClient, org.Namespace, false, false)
	if err != nil {
		return fmt.Errorf("migrate cluster definitions: %w", err)
	}
	if !clusterSummary.OK() {
		return fmt.Errorf("migrate cluster definitions: %d failed: %v", len(clusterSummary.Failed), clusterSummary.Failed)
	}
	if err := s.copyClusterDefinitionLabels(ctx, sourceClient, destClient, org.Namespace); err != nil {
		return fmt.Errorf("copy cluster definition labels: %w", err)
	}

	templateSummary, err := migrate.Templates(ctx, sourceClient, destClient, org.Namespace, false, false)
	if err != nil {
		return fmt.Errorf("migrate templates: %w", err)
	}
	if !templateSummary.OK() {
		return fmt.Errorf("migrate templates: %d failed: %v", len(templateSummary.Failed), templateSummary.Failed)
	}

	workflowSummary, err := migrate.Workflows(ctx, sourceClient, destClient, org.Namespace, false, false)
	if err != nil {
		return fmt.Errorf("migrate workflows: %w", err)
	}
	if !workflowSummary.OK() {
		return fmt.Errorf("migrate workflows: %d failed: %v", len(workflowSummary.Failed), workflowSummary.Failed)
	}

	resourceSummary, err := migrate.Resources(ctx, sourceClient, destClient, org.Namespace, false, false)
	if err != nil {
		return fmt.Errorf("migrate resources: %w", err)
	}
	if !resourceSummary.OK() {
		return fmt.Errorf("migrate resources: %d failed: %v", len(resourceSummary.Failed), resourceSummary.Failed)
	}

	bindingSummary, err := migrate.AccessBindings(ctx, sourceClient, destClient, org.Namespace, false, false)
	if err != nil {
		return fmt.Errorf("migrate credentials secrets: %w", err)
	}
	if !bindingSummary.OK() {
		return fmt.Errorf("migrate credentials secrets: %d failed: %v", len(bindingSummary.Failed), bindingSummary.Failed)
	}

	return nil
}

// copyClusterDefinitionLabels syncs every ClusterDefinition's Labels from
// sourceClient onto its already-migrated counterpart on destClient — most
// notably HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 3
// hyve.io/environment label, whose loss during migration would silently
// break environment-scoped naming/lookup on the destination.
//
// Handled as a separate pass, deliberately not folded into
// migrate.ClusterDefinitions itself: that function takes a
// reconcile.StateProvider (source), not a raw client.Client, so it can
// also serve `hyve migrate to-cluster`'s file/git-mode source — and
// internal/types.ClusterMetadata (what LoadClusterDefinitions returns)
// has no concept of Kubernetes labels at all, by design, since a local
// YAML file doesn't either. Milestone 6's own migration is always
// cluster-to-cluster, so reading labels directly from sourceClient here —
// bypassing that generic, necessarily lossy abstraction — is both
// possible and correct for this one caller, without changing
// migrate.ClusterDefinitions' own signature or the shared reconcile
// engine's core data model for every other mode. Confirmed live: without
// this, a migrated environment-scoped ClusterDefinition's name/environment
// silently reverted to looking unscoped on the destination.
func (s *Server) copyClusterDefinitionLabels(ctx context.Context, sourceClient, destClient client.Client, namespace string) error {
	var list hyvev1alpha1.ClusterDefinitionList
	if err := sourceClient.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("list source cluster definitions: %w", err)
	}
	for i := range list.Items {
		name := list.Items[i].Name
		var dest hyvev1alpha1.ClusterDefinition
		if err := destClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &dest); err != nil {
			return fmt.Errorf("get destination cluster definition %s: %w", name, err)
		}
		if reflect.DeepEqual(dest.Labels, list.Items[i].Labels) {
			continue
		}
		dest.Labels = list.Items[i].Labels
		if err := destClient.Update(ctx, &dest); err != nil {
			return fmt.Errorf("update destination cluster definition %s labels: %w", name, err)
		}
	}
	return nil
}

// handleDeleteOrganization implements the proposal's Namespace-finalizer
// design (HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Organization deletion"
// section, Milestone 5): mark the row pending_deletion, then issue the
// Kubernetes namespace delete. Kubernetes' own cascade tears down
// everything inside the namespace, but the Namespace object itself can't
// fully disappear until internal/controller's NamespaceReconciler confirms
// that and removes hyvev1alpha1.OrganizationNamespaceFinalizer — this
// handler never waits for that synchronously (it can take an arbitrary
// amount of time), it just kicks the process off and returns 202. The row
// itself isn't deleted here except via the opportunistic fast-path check
// below — the durable path is SweepPendingOrganizationDeletions, wired up
// as a periodic background task in cmd/api/run.go, so a process restart
// between "namespace delete issued" and "namespace fully gone" loses
// nothing: pending_deletion survives, and the next sweep resumes exactly
// where this left off.
//
// Idempotent: re-issuing this against an already-pending_deletion
// organization just re-issues the (already-in-flight, harmless) namespace
// delete and re-checks the fast path, matching every other
// organization-lifecycle handler's re-POST-safe design in this file.
func (s *Server) handleDeleteOrganization(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleSuperadmin) {
		return
	}
	ctx := r.Context()
	name := r.PathValue("name")

	org, err := s.OrgStore.GetOrganizationByName(ctx, name)
	if err == orgdb.ErrNotFound {
		writeError(w, http.StatusNotFound, "organization not found")
		return
	} else if err != nil {
		log.Printf("api: failed to get organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete organization")
		return
	}

	if !org.PendingDeletion {
		if err := s.OrgStore.MarkOrganizationPendingDeletion(ctx, org.ID); err != nil {
			log.Printf("api: failed to mark organization %q pending deletion: %v", name, err)
			writeError(w, http.StatusInternalServerError, "failed to delete organization")
			return
		}
		org.PendingDeletion = true
	}

	targetClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("organization marked for deletion, but resolving its reconciling cluster failed: %v — retry this same request to finish", err))
		return
	}
	ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: org.Namespace}}
	if err := targetClient.Delete(ctx, &ns); err != nil && !apierrors.IsNotFound(err) {
		log.Printf("api: failed to delete namespace %q for organization %q: %v", org.Namespace, name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("organization marked for deletion, but namespace delete failed: %v — retry this same request to finish", err))
		return
	}

	// Opportunistic fast path: most deletes will still find the Namespace
	// Terminating right after this (everything inside it hasn't finished
	// tearing down yet) and this is a no-op — the periodic sweep finishes
	// the job later. But an already-empty organization's Namespace can
	// terminate essentially immediately, in which case there's no reason
	// to make the caller wait for the next sweep interval.
	s.trySweepOrganizationDeletion(ctx, org)

	w.WriteHeader(http.StatusAccepted)
}

// trySweepOrganizationDeletion checks whether org's Namespace has finished
// terminating (a Get 404) and, if so, permanently deletes its row — and
// every environment/binding scoped to it — via Store.DeleteOrganization.
// Safe to call speculatively and repeatedly: the common case is the
// Namespace still exists (Terminating or, for an org never marked for
// deletion at all, just ordinarily present), in which case this does
// nothing.
func (s *Server) trySweepOrganizationDeletion(ctx context.Context, org orgdb.Organization) {
	targetClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster during organization-deletion sweep for %q: %v", org.Name, err)
		return
	}
	var ns corev1.Namespace
	err = targetClient.Get(ctx, types.NamespacedName{Name: org.Namespace}, &ns)
	if err == nil {
		return
	}
	if !apierrors.IsNotFound(err) {
		log.Printf("api: failed to check namespace %q during organization-deletion sweep: %v", org.Namespace, err)
		return
	}
	if err := s.OrgStore.DeleteOrganization(ctx, org.ID); err != nil {
		log.Printf("api: failed to delete organization row %q after namespace termination: %v", org.Name, err)
	}
}

// SweepPendingOrganizationDeletions checks every organization currently
// pending_deletion and finishes deleting its row once its Namespace has
// fully terminated — the periodic half of the "check on next relevant
// request, or a periodic sweep" design the proposal calls for
// (handleDeleteOrganization's own opportunistic call to
// trySweepOrganizationDeletion is the other half). Meant to be called on a
// recurring interval by cmd/api/run.go; safe to call concurrently with
// itself and with a live handleDeleteOrganization request, since every
// step it takes is idempotent (a namespace Get, and a DeleteOrganization
// that's already a no-op-safe DELETE by id).
func (s *Server) SweepPendingOrganizationDeletions(ctx context.Context) {
	orgs, err := s.OrgStore.ListOrganizationsPendingDeletion(ctx)
	if err != nil {
		log.Printf("api: failed to list organizations pending deletion: %v", err)
		return
	}
	for _, org := range orgs {
		s.trySweepOrganizationDeletion(ctx, org)
	}
}

type organizationEnvironmentDTO struct {
	Name string `json:"name"`
}

func toOrganizationEnvironmentDTO(env orgdb.Environment) organizationEnvironmentDTO {
	return organizationEnvironmentDTO{Name: env.Name}
}

// requireOrgAccess resolves orgName and authorizes the caller against it —
// unlike every other organization-management endpoint in this file
// (create/delete/patch/list, all genuinely cross-namespace-by-design and
// so superadmin-only), environments and reconciling-cluster visibility are
// entirely within one organization's own scope, so an ordinary admin
// managing their own tenant should be able to reach them too — a
// superadmin is authorized for any organization by name (matching every
// other endpoint here); an admin is authorized only when orgName resolves
// to exactly their own TenantNamespace, never an arbitrary name in the
// URL, so one tenant's admin can never manage another tenant's
// environments just by naming it. Writes its own error response and
// returns ok=false when access should be denied.
func (s *Server) requireOrgAccess(w http.ResponseWriter, r *http.Request, orgName string) (org orgdb.Organization, ok bool) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin) {
		return orgdb.Organization{}, false
	}
	org, err := s.OrgStore.GetOrganizationByName(r.Context(), orgName)
	if err == orgdb.ErrNotFound {
		writeError(w, http.StatusNotFound, "organization not found")
		return orgdb.Organization{}, false
	}
	if err != nil {
		log.Printf("api: failed to get organization %q: %v", orgName, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve organization")
		return orgdb.Organization{}, false
	}
	role, _ := RoleFromContext(r.Context())
	if role != hyvev1alpha1.RoleSuperadmin && org.Namespace != s.TenantNamespace(r) {
		writeError(w, http.StatusForbidden, "not permitted to manage this organization")
		return orgdb.Organization{}, false
	}
	return org, true
}

// handleListOrgEnvironments lists every environment for the named
// organization — a superadmin can reach any organization by name; an
// ordinary admin only their own (see requireOrgAccess).
func (s *Server) handleListOrgEnvironments(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	envs, err := s.OrgStore.ListEnvironments(r.Context(), org.ID)
	if err != nil {
		log.Printf("api: failed to list environments for %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to list environments")
		return
	}
	out := make([]organizationEnvironmentDTO, 0, len(envs))
	for _, env := range envs {
		out = append(out, toOrganizationEnvironmentDTO(env))
	}
	writeJSON(w, http.StatusOK, out)
}

type createOrgEnvironmentRequest struct {
	Name string `json:"name"`
}

// handleCreateOrgEnvironment adds a new named environment to an existing
// organization — the sub-scoping capability
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md exists for. The organization's own
// `default` environment is created automatically by
// CreateOrganizationWithDefaults (see handleCreateOrganization); this
// endpoint is for every environment after that first one (`staging`,
// `production`, ...). A superadmin can target any organization by name; an
// ordinary admin only their own (see requireOrgAccess).
func (s *Server) handleCreateOrgEnvironment(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	var req createOrgEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ctx := r.Context()

	if existing, err := s.OrgStore.GetEnvironmentByName(ctx, org.ID, req.Name); err == nil {
		writeJSON(w, http.StatusCreated, toOrganizationEnvironmentDTO(existing))
		return
	} else if err != orgdb.ErrNotFound {
		log.Printf("api: failed to check existing environment %q/%q: %v", org.Name, req.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to create environment")
		return
	}

	env, err := s.OrgStore.CreateEnvironment(ctx, orgdb.Environment{OrganizationID: org.ID, Name: req.Name, Metadata: "{}"})
	if err != nil {
		log.Printf("api: failed to create environment %q/%q: %v", org.Name, req.Name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create environment: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toOrganizationEnvironmentDTO(env))
}

// validateOrganizationName rejects the two names that would collide with,
// or be confused with, the control plane itself:
//   - controlPlaneNamespace (s.Namespace — "hyve-system" in every real
//     deployment, but checked against the actual configured value rather
//     than that literal, since it's a --namespace flag/Helm value, not a
//     hardcoded constant): creating an organization with this name would
//     make ensureNamespace/ensureAccessRoleScaffolding operate directly on
//     the control plane's own namespace instead of a new tenant one,
//     injecting tenant-style ServiceAccounts into it and listing it
//     alongside real tenants.
//   - "control plane" (any case/spacing): not a real collision — Kubernetes
//     namespace names can't contain a space or uppercase letter, so the
//     literal EnvironmentSwitcher label could never collide at the
//     namespace level — but confusingly duplicates the always-present
//     "Control plane" entry in that same dropdown, and would otherwise
//     surface as an unhelpful raw Kubernetes "invalid name" error instead
//     of an explanation.
//
// Case-insensitive both ways: nothing about a namespace/organization name
// makes case load-bearing here, and a caller typing "Hyve-System" clearly
// means the same reserved thing as "hyve-system".
func validateOrganizationName(name, controlPlaneNamespace string) error {
	if strings.EqualFold(name, controlPlaneNamespace) {
		return fmt.Errorf("%q is the control plane's own namespace — choose a different organization name", name)
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(name, "-", " ")), "-"))
	if normalized == "control-plane" {
		return fmt.Errorf("%q is reserved for the control plane — choose a different organization name", name)
	}
	return nil
}

// ensureNamespace creates name's Namespace if it doesn't already exist,
// carrying hyvev1alpha1.OrganizationNamespaceFinalizer from the start —
// see that constant's own doc comment for why: a Namespace created without
// it would let `kubectl delete namespace` (or the Milestone 5 delete path
// below) remove it immediately, before internal/controller's
// NamespaceReconciler ever gets a chance to confirm every hyve-owned
// object inside it is actually gone. An existing Namespace missing the
// finalizer (e.g. one created before this milestone) gets it added too,
// so re-running this idempotent step on an older organization still
// closes the gap.
func (s *Server) ensureNamespace(ctx context.Context, c client.Client, name string) error {
	var ns corev1.Namespace
	err := c.Get(ctx, types.NamespacedName{Name: name}, &ns)
	if err == nil {
		if controllerutil.ContainsFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer) {
			return nil
		}
		controllerutil.AddFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer)
		if err := c.Update(ctx, &ns); err != nil {
			return fmt.Errorf("add finalizer to existing namespace: %w", err)
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check existing namespace: %w", err)
	}
	ns = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	controllerutil.AddFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer)
	if err := c.Create(ctx, &ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace: %w", err)
	}
	return nil
}

// ensureAccessRoleScaffolding creates namespace's own
// hyve-access-admin/hyve-access-readonly ServiceAccounts + namespaced
// RoleBindings to the built-in admin/view ClusterRoles — the same two
// ServiceAccount+RoleBinding pairs deploy/helm/hyve/templates/
// api-access-roles.yaml already defines per-install under Phase 1 (one
// install = one Helm release = one namespace), created here
// programmatically instead, once per tenant, since Phase 2 has exactly one
// install total.
func (s *Server) ensureAccessRoleScaffolding(ctx context.Context, c client.Client, namespace string) error {
	roles := []struct {
		saName      string
		clusterRole string
	}{
		{"hyve-access-admin", "cluster-admin"},
		{"hyve-access-readonly", "view"},
	}
	for _, role := range roles {
		sa := corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: role.saName, Namespace: namespace}}
		if err := c.Create(ctx, &sa); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create ServiceAccount %s/%s: %w", namespace, role.saName, err)
		}

		rb := rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: role.saName, Namespace: namespace},
			Subjects: []rbacv1.Subject{
				{Kind: "ServiceAccount", Name: role.saName, Namespace: namespace},
			},
			RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: role.clusterRole, APIGroup: "rbac.authorization.k8s.io"},
		}
		if err := c.Create(ctx, &rb); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create RoleBinding %s/%s: %w", namespace, role.saName, err)
		}
	}
	return nil
}
