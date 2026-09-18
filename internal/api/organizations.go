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
	"k8s.io/client-go/tools/clientcmd"
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
	mux.HandleFunc("DELETE /organizations/{name}/environments/{env}", s.handleDeleteOrgEnvironment)
	mux.HandleFunc("GET /organizations/{name}/reconciling-cluster", s.handleGetOrgReconcilingCluster)
	mux.HandleFunc("PUT /organizations/{name}/reconciling-cluster", s.handlePutOrgReconcilingCluster)
	mux.HandleFunc("DELETE /organizations/{name}/reconciling-cluster", s.handleDeleteOrgReconcilingCluster)
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
	// PendingDeletion reflects pending_deletion — DELETE /organizations/{name}
	// was already called and the namespace teardown is in flight (see that
	// handler's own doc comment); the row disappears on its own once the
	// namespace finishes terminating, nothing further to call.
	PendingDeletion bool `json:"pendingDeletion,omitempty"`
}

// toOrganizationDTO resolves org's own reconciling cluster name for
// display — a method, not a plain function, since org.ReconcilingClusterID
// is only ever an id; showing something meaningful means a Store lookup.
// A lookup failure is logged but never fails the request: this is a
// cosmetic display detail, not something an otherwise-successful org
// list/create/patch response should 500 over.
func (s *Server) toOrganizationDTO(ctx context.Context, org orgdb.Organization) organizationDTO {
	dto := organizationDTO{
		Name:            org.Name,
		Namespace:       org.Namespace,
		Plan:            org.Plan,
		Migrating:       org.ReconcilingClusterMigrationStatus != nil,
		PendingDeletion: org.PendingDeletion,
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
		// The control plane's own organization row (ensureControlPlaneOrganization,
		// cmd/api/controlplaneorg.go) is never a tenant to manage or switch
		// into through this list — it's not creatable here
		// (validateOrganizationName) and not deletable here (see
		// handleDeleteOrganization's identical org.Namespace == s.Namespace
		// check) either. Every caller of this endpoint already has its own
		// dedicated way to represent the control plane (the web console's
		// "Viewing" switcher hardcodes its own "Control plane" option at
		// actAs=""; OrganizationsPage manages tenants specifically) —
		// without this exclusion, both would show a second, redundant
		// "hyve-system" row alongside it that resolves to the exact same
		// namespace, confirmed live as confusing rather than useful.
		if org.Namespace == s.Namespace {
			continue
		}
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
	// RequireReconcilingCluster (a --require-reconciling-cluster install):
	// no organization may land on the control plane's own home cluster at
	// all — see this field's own doc comment on Server for the operator
	// intent (hosted/multi-tenant installs that must never let a tenant's
	// resources touch the cluster hyve-controller/hyve-api themselves run
	// on). No exemption needed here the way handlePatchOrganization needs
	// one for the control plane's own organization: validateOrganizationName
	// above already refuses to ever create an organization named
	// s.Namespace through this endpoint, so this check can never
	// mistakenly reject that one legitimate case.
	if s.RequireReconcilingCluster && req.ReconcilingCluster == "" {
		writeError(w, http.StatusBadRequest, "this install requires every organization to have an explicit reconcilingCluster (see --require-reconciling-cluster) — register one first with POST /reconciling-clusters")
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
	// Name renames the organization's own display Name — its Namespace
	// (the real, immutable Kubernetes namespace everything else actually
	// keys on: bindings, resourceClient, login resolution) is never
	// touched, so this is purely cosmetic/addressing, not a migration of
	// any kind. Must stay unique across every organization (checked
	// explicitly below, ahead of the name column's own UNIQUE constraint,
	// for a clean 409 instead of a raw DB error) and passes through the
	// same reserved-name rules creation does (validateOrganizationName).
	// A pointer so omitted (no rename) is distinguishable from — though
	// never actually reachable via the JSON wire format the same way as
	// ReconcilingCluster's own "" — an empty string, which is rejected as
	// invalid rather than silently ignored. Optional independently of
	// ReconcilingCluster: a request may rename, migrate, or both in one
	// call.
	Name *string `json:"name,omitempty"`

	// ReconcilingCluster names the reconciling cluster (POST
	// /reconciling-clusters) to move this organization onto — "" moves it
	// back to the control plane's own home cluster. A pointer so an
	// omitted field can be distinguished from an explicit "". Optional
	// independently of Name — see that field's own doc comment.
	ReconcilingCluster *string `json:"reconcilingCluster,omitempty"`
}

// handlePatchOrganization handles two independent, optional changes to an
// organization — renaming it (see patchOrganizationRequest.Name's own doc
// comment) and/or Milestone 6's reconciling-cluster migration, in that
// order, in one request. The rename half is simple: validate, check
// uniqueness, update the name column, done — it never touches the
// Namespace anything else in this codebase actually resolves against. The
// migration half implements Milestone 6's reconciling-cluster migration
// (HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization
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
	if req.Name == nil && req.ReconcilingCluster == nil {
		writeError(w, http.StatusBadRequest, "at least one of name or reconcilingCluster is required")
		return
	}
	if req.Name != nil && *req.Name == "" {
		writeError(w, http.StatusBadRequest, "name must not be empty")
		return
	}

	org, err := s.OrgStore.GetOrganizationByName(ctx, name)
	if err == orgdb.ErrNotFound {
		writeError(w, http.StatusNotFound, "organization not found")
		return
	} else if err != nil {
		log.Printf("api: failed to get organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to update organization")
		return
	}
	// Pending-deletion blocks a rename too — nothing about renaming an
	// organization on its way out makes sense. Checked here, ahead of both
	// the rename block below and the target-cluster-name lookup further
	// down, so either kind of request against an already-pending-deletion
	// or already-migrating organization reports *that* conflict (409/423)
	// first — migrateOrganizationToTarget re-checks both anyway, but only
	// after it already has a resolved targetClusterID to work with, and
	// only for the reconciling-cluster half of this request.
	if org.PendingDeletion {
		writeError(w, http.StatusConflict, fmt.Sprintf("organization %q is pending deletion", name))
		return
	}
	if org.ReconcilingClusterMigrationStatus != nil {
		writeError(w, http.StatusLocked, fmt.Sprintf("organization %q is already migrating", name))
		return
	}

	if req.Name != nil && *req.Name != org.Name {
		newName := *req.Name
		if err := validateOrganizationName(newName, s.Namespace); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		switch existing, err := s.OrgStore.GetOrganizationByName(ctx, newName); {
		case err == nil && existing.ID != org.ID:
			writeError(w, http.StatusConflict, fmt.Sprintf("an organization named %q already exists", newName))
			return
		case err != nil && err != orgdb.ErrNotFound:
			log.Printf("api: failed to check name %q availability: %v", newName, err)
			writeError(w, http.StatusInternalServerError, "failed to rename organization")
			return
		}
		if err := s.OrgStore.RenameOrganization(ctx, org.ID, newName); err != nil {
			log.Printf("api: failed to rename organization %q to %q: %v", org.Name, newName, err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to rename organization: %v", err))
			return
		}
		org.Name = newName
	}

	if req.ReconcilingCluster == nil {
		writeJSON(w, http.StatusOK, s.toOrganizationDTO(ctx, org))
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

	dto, ok := s.migrateOrganizationToTarget(w, r, org, targetClusterID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// migrateOrganizationToTarget moves org onto targetClusterID (nil for the
// control plane's own home cluster) — the shared core of
// handlePatchOrganization's superadmin-driven migration and
// handlePutOrgReconcilingCluster's org-self-service equivalent (an ordinary
// admin reaching this for their own organization only, via
// requireOrgAccess). Every check that used to live inline in
// handlePatchOrganization (pending-deletion, already-migrating,
// --require-reconciling-cluster, the already-there no-op) now lives here so
// both callers get the identical safety guarantees. Writes its own error
// response and returns ok=false on any failure, matching this file's other
// w-writing helpers (e.g. requireOrgAccess); returns the freshly reloaded
// organizationDTO on success.
func (s *Server) migrateOrganizationToTarget(w http.ResponseWriter, r *http.Request, org orgdb.Organization, targetClusterID *string) (organizationDTO, bool) {
	ctx := r.Context()
	name := org.Name

	if org.PendingDeletion {
		writeError(w, http.StatusConflict, fmt.Sprintf("organization %q is pending deletion", name))
		return organizationDTO{}, false
	}
	if org.ReconcilingClusterMigrationStatus != nil {
		writeError(w, http.StatusLocked, fmt.Sprintf("organization %q is already migrating", name))
		return organizationDTO{}, false
	}
	// RequireReconcilingCluster: migrating *back* to the home cluster
	// (targetClusterID == nil) is refused for every organization except the
	// control plane's own — see Server.RequireReconcilingCluster's own doc
	// comment. The control-plane exemption matters here in a way it doesn't
	// for handleCreateOrganization: Milestone 10 Part A/B deliberately made
	// hyve-system migratable "just like any tenant" (including back to its
	// own home cluster), and that's an operator infrastructure decision
	// about where the control plane itself runs, not a tenant ever touching
	// host-cluster resources — the exact thing this flag exists to prevent.
	if s.RequireReconcilingCluster && targetClusterID == nil && org.Namespace != s.Namespace {
		writeError(w, http.StatusBadRequest, "this install requires every organization to have an explicit reconcilingCluster (see --require-reconciling-cluster) — migrating back to the home cluster is not allowed")
		return organizationDTO{}, false
	}

	if reconcilingClusterIDsEqual(org.ReconcilingClusterID, targetClusterID) {
		// Already there — a no-op, matching this file's own
		// idempotent-by-design precedent elsewhere (handleCreateOrganization,
		// handleDeleteOrganization).
		return s.toOrganizationDTO(ctx, org), true
	}

	sourceClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve source reconciling cluster for organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to migrate organization")
		return organizationDTO{}, false
	}
	var destClient client.Client
	if targetClusterID == nil {
		if s.Client == nil {
			writeError(w, http.StatusBadRequest, "this install has no home cluster of its own — an empty reconcilingCluster (\"migrate back to home\") isn't possible here")
			return organizationDTO{}, false
		}
		destClient = s.Client
	} else {
		handle, err := s.reconcilingClusterClientHandle(ctx, *targetClusterID)
		if err != nil {
			log.Printf("api: failed to resolve destination reconciling cluster for organization %q: %v", name, err)
			writeError(w, http.StatusInternalServerError, "failed to migrate organization")
			return organizationDTO{}, false
		}
		destClient = handle.Client
	}

	migrating := "migrating"
	if err := s.OrgStore.SetOrganizationMigrationStatus(ctx, org.ID, &migrating); err != nil {
		log.Printf("api: failed to lock organization %q for migration: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to migrate organization")
		return organizationDTO{}, false
	}

	if err := s.runOrganizationMigration(ctx, org, sourceClient, destClient); err != nil {
		log.Printf("api: migration failed for organization %q: %v", name, err)
		if unlockErr := s.OrgStore.SetOrganizationMigrationStatus(ctx, org.ID, nil); unlockErr != nil {
			log.Printf("api: failed to clear migration lock for organization %q after a failed migration: %v", name, unlockErr)
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migration failed, organization left on its original reconciling cluster: %v", err))
		return organizationDTO{}, false
	}

	if err := s.OrgStore.SetOrganizationReconcilingCluster(ctx, org.ID, targetClusterID); err != nil {
		log.Printf("api: failed to finalize reconciling cluster for organization %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "migration data copied, but finalizing the organization's reconciling cluster failed — retry this same request to finish")
		return organizationDTO{}, false
	}

	updated, err := s.OrgStore.GetOrganization(ctx, org.ID)
	if err != nil {
		log.Printf("api: failed to reload organization %q after migration: %v", name, err)
		writeError(w, http.StatusInternalServerError, "migration completed, but reloading the organization record failed")
		return organizationDTO{}, false
	}
	return s.toOrganizationDTO(ctx, updated), true
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

// handleDeleteOrganization marks the row pending_deletion, then issues the
// Kubernetes namespace delete. Kubernetes' own cascade tears down
// everything inside the namespace and then the Namespace object itself —
// no hyve-specific finalizer gates that final removal anymore (see
// hyvev1alpha1.OrganizationNamespaceFinalizer's own doc comment for why
// the design that used to require an out-of-band controller confirmation
// was retired), so this behaves like any other Kubernetes deletion in this
// application. This handler never waits for that synchronously regardless
// (it can still take a real amount of time — real cloud teardown behind
// ClusterDefinitionFinalizer on anything still inside the namespace, most
// notably), it just kicks the process off and returns 202. The row
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
	// The control plane's own organization is not a tenant — nothing else
	// in this file has ever let it be created via this API
	// (validateOrganizationName), and it must never be destroyable through
	// it either: this Namespace is the one hyve-controller/hyve-api
	// themselves (or, if s.Client is nil, the process that still needs
	// this row to resolve identity/sessions) actually run in. Checked
	// ahead of the idempotent-by-design PendingDeletion branch below on
	// purpose — an already-"successfully" pending-deletion control-plane
	// organization is not a state that should ever exist, but this stays
	// a hard refuse either way, not a silent no-op.
	if org.Namespace == s.Namespace {
		writeError(w, http.StatusBadRequest, "the control plane's own organization cannot be deleted")
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
// nothing beyond the self-heal check below.
//
// Self-heal: a Namespace still carrying
// hyvev1alpha1.OrganizationNamespaceFinalizer — only possible for one
// created before that finalizer's own retirement — has its finalizer
// stripped right here, unblocking a namespace that would otherwise stay
// Terminating forever with nothing left to clear it (see that constant's
// own doc comment). Every call site of this function only ever reaches it
// for an organization already marked pending_deletion, so this is never
// reachable for a Namespace that isn't already on its way out.
func (s *Server) trySweepOrganizationDeletion(ctx context.Context, org orgdb.Organization) {
	targetClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster during organization-deletion sweep for %q: %v", org.Name, err)
		return
	}
	var ns corev1.Namespace
	err = targetClient.Get(ctx, types.NamespacedName{Name: org.Namespace}, &ns)
	if err == nil {
		if controllerutil.ContainsFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer) {
			controllerutil.RemoveFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer)
			if err := targetClient.Update(ctx, &ns); err != nil {
				log.Printf("api: failed to strip legacy finalizer from namespace %q during organization-deletion sweep: %v", org.Namespace, err)
			}
		}
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

// handleDeleteOrgEnvironment permanently removes one environment from an
// organization — refused when it's the organization's last remaining
// environment (resolveResourceEnvironment's own single-vs-ambiguous
// resolution stops meaning anything once an organization has zero), when
// it's specifically the reserved orgdb.DefaultEnvironmentName ("default")
// regardless of how many other environments remain (see
// effectiveEnvironmentLabel's own doc comment: "default" is the one
// designated home every unlabeled/legacy ClusterDefinition resolves to —
// deleting it while a peer like "staging" survives would silently
// reintroduce the exact ambiguity that backfill exists to avoid, for every
// still-unlabeled cluster in the namespace, not just newly created ones),
// or when any ClusterDefinition in the organization's namespace still
// carries this environment's own hyve.io/environment label (deleting the
// row out from under still-live clusters would silently orphan them from
// every environment-scoped list/filter in the console, with no way back
// short of hand-editing labels — see environmentInUse). A superadmin can
// target any organization by name; an ordinary admin only their own (see
// requireOrgAccess).
//
// Renaming is deliberately not offered alongside this: an environment's
// name is baked directly into every object's own metadata.name (see
// joinEnvironmentName), not just this label, so a rename would mean
// recreating every object under a new name rather than a simple row
// update — out of scope here.
func (s *Server) handleDeleteOrgEnvironment(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	ctx := r.Context()
	envName := r.PathValue("env")

	if envName == orgdb.DefaultEnvironmentName {
		writeError(w, http.StatusBadRequest, "the \"default\" environment cannot be deleted — every object created before environments existed, or with no environment specified, belongs to it")
		return
	}

	env, err := s.OrgStore.GetEnvironmentByName(ctx, org.ID, envName)
	if err == orgdb.ErrNotFound {
		writeError(w, http.StatusNotFound, "environment not found")
		return
	} else if err != nil {
		log.Printf("api: failed to get environment %q/%q: %v", org.Name, envName, err)
		writeError(w, http.StatusInternalServerError, "failed to delete environment")
		return
	}

	envs, err := s.OrgStore.ListEnvironments(ctx, org.ID)
	if err != nil {
		log.Printf("api: failed to list environments for %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete environment")
		return
	}
	if len(envs) <= 1 {
		writeError(w, http.StatusBadRequest, "cannot delete an organization's last remaining environment")
		return
	}

	rc, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete environment")
		return
	}
	inUse, err := s.environmentInUse(ctx, rc, org.Namespace, envName)
	if err != nil {
		log.Printf("api: failed to check environment %q/%q usage: %v", org.Name, envName, err)
		writeError(w, http.StatusInternalServerError, "failed to delete environment")
		return
	}
	if inUse {
		writeError(w, http.StatusConflict, fmt.Sprintf("environment %q still has clusters — delete them first", envName))
		return
	}

	if err := s.OrgStore.DeleteEnvironment(ctx, env.ID); err != nil {
		log.Printf("api: failed to delete environment %q/%q: %v", org.Name, envName, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete environment: %v", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// environmentInUse reports whether any ClusterDefinition in namespace still
// carries environment's own hyve.io/environment label. ClusterDefinition is
// the only one of the four resource types still environment-scoped at all
// (Template/Workflow/Resource are organization-wide reusable blueprints
// with no live per-environment state of their own — see templateDTO's own
// doc comment) — so it's the only one that can meaningfully be "in use" by
// a given environment.
func (s *Server) environmentInUse(ctx context.Context, rc client.Client, namespace, environment string) (bool, error) {
	var clusters hyvev1alpha1.ClusterDefinitionList
	if err := rc.List(ctx, &clusters, client.InNamespace(namespace), client.MatchingLabels{hyveEnvironmentLabel: environment}); err != nil {
		return false, fmt.Errorf("list cluster definitions: %w", err)
	}
	return len(clusters.Items) > 0, nil
}

// orgReconcilingClusterDTO is GET/PUT /organizations/{name}/reconciling-cluster's
// own response shape — deliberately not reconcilingClusterDTO itself: an
// org admin reaching this endpoint has no visibility into (and no business
// knowing about) the superadmin-managed shared registry GET
// /reconciling-clusters exposes, only their own organization's current
// placement, so OnHomeCluster stands in for "no reconciling cluster
// override" instead of an empty/omitted name being ambiguous with a lookup
// failure.
type orgReconcilingClusterDTO struct {
	OnHomeCluster     bool    `json:"onHomeCluster"`
	Name              string  `json:"name,omitempty"`
	Server            string  `json:"server,omitempty"`
	Reachable         *bool   `json:"reachable,omitempty"`
	LastCheckedAt     *string `json:"lastCheckedAt,omitempty"`
	LastError         *string `json:"lastError,omitempty"`
	KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
	RegisteredAt      string  `json:"registeredAt,omitempty"`
	Migrating         bool    `json:"migrating,omitempty"`
}

// handleGetOrgReconcilingCluster reports the named organization's current
// reconciling-cluster placement — a superadmin can target any organization
// by name; an ordinary admin only their own (see requireOrgAccess).
func (s *Server) handleGetOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	dto := orgReconcilingClusterDTO{Migrating: org.ReconcilingClusterMigrationStatus != nil}
	if org.ReconcilingClusterID == nil {
		dto.OnHomeCluster = true
		writeJSON(w, http.StatusOK, dto)
		return
	}
	rc, err := s.OrgStore.GetReconcilingCluster(r.Context(), *org.ReconcilingClusterID)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve organization's reconciling cluster")
		return
	}
	rcDTO := toReconcilingClusterDTO(rc)
	dto.Name = rcDTO.Name
	dto.Server = rcDTO.Server
	dto.Reachable = rcDTO.Reachable
	dto.LastCheckedAt = rcDTO.LastCheckedAt
	dto.LastError = rcDTO.LastError
	dto.KubernetesVersion = rcDTO.KubernetesVersion
	dto.RegisteredAt = rcDTO.RegisteredAt
	writeJSON(w, http.StatusOK, dto)
}

type putOrgReconcilingClusterRequest struct {
	// Kubeconfig is this organization's own dedicated reconciling cluster's
	// kubeconfig — set (or rotate) it here, scoped to exactly this
	// organization, never the shared superadmin-managed registry (POST
	// /reconciling-clusters) PATCH /organizations/{name} draws from. Empty
	// moves the organization back onto the control plane's own home
	// cluster (refused when --require-reconciling-cluster is set, except
	// for the control plane's own organization — see
	// migrateOrganizationToTarget).
	Kubeconfig string `json:"kubeconfig"`
}

// handlePutOrgReconcilingCluster is the organization-admin-facing
// counterpart to PATCH /organizations/{name}: rather than picking from the
// superadmin-managed shared registry (POST/GET /reconciling-clusters, still
// superadmin-only), an organization's own admin sets or rotates a
// reconciling cluster dedicated to exactly their organization here, by
// kubeconfig directly. The underlying orgdb.ReconcilingCluster row is named
// identically to the organization's own Namespace — a deliberate 1:1
// relationship: it keeps this cluster out of the shared-pool list an org
// admin has no visibility into, and avoids any naming collision with a
// same-named cluster a superadmin might separately register there. A
// superadmin can target any organization by name; an ordinary admin only
// their own (see requireOrgAccess).
func (s *Server) handlePutOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	var req putOrgReconcilingClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Kubeconfig == "" {
		dto, ok := s.migrateOrganizationToTarget(w, r, org, nil)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, dto)
		return
	}

	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(req.Kubeconfig)); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid kubeconfig: %v", err))
		return
	}

	ctx := r.Context()

	rc, err := s.OrgStore.GetReconcilingClusterByName(ctx, org.Namespace)
	switch {
	case err == nil:
		if err := s.OrgStore.SetReconcilingClusterKubeconfig(ctx, rc.ID, req.Kubeconfig); err != nil {
			log.Printf("api: failed to rotate kubeconfig for organization %q's reconciling cluster: %v", org.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to update reconciling cluster")
			return
		}
		// A rotated kubeconfig invalidates any cached client for this
		// cluster — see handleCreateReconcilingCluster's own identical
		// precedent for why.
		s.invalidateReconcilingClusterClientByName(ctx, org.Namespace)
	case err == orgdb.ErrNotFound:
		rc, err = s.OrgStore.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: org.Namespace, Kubeconfig: req.Kubeconfig})
		if err != nil {
			log.Printf("api: failed to register reconciling cluster for organization %q: %v", org.Name, err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to register reconciling cluster: %v", err))
			return
		}
	default:
		log.Printf("api: failed to check existing reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to update reconciling cluster")
		return
	}

	dto, ok := s.migrateOrganizationToTarget(w, r, org, &rc.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleDeleteOrgReconcilingCluster permanently removes the named
// organization's own dedicated reconciling cluster — the orgdb.ReconcilingCluster
// row named identically to its own namespace (see handlePutOrgReconcilingCluster's
// own doc comment for why that 1:1 naming convention exists), the "remove"
// half of the self-service register/rotate/remove trio an organization's
// own admin (or a superadmin "Viewing" it) has over their own reconciling
// cluster — see requireOrgAccess. If currently assigned, this migrates the
// organization back to the control plane's own home cluster first (reusing
// migrateOrganizationToTarget's own lock/pending-deletion/
// RequireReconcilingCluster guarantees) — never leaves the organization
// pointing at a row that's about to disappear. Refuses with 409 if some
// other organization is somehow also currently assigned to this same
// cluster (only reachable via a superadmin using the CLI directly against
// the shared registry — self-service registration here always follows the
// 1:1 naming convention, so this is a rare, defensive check, not the
// common case) — deleting it out from under that other organization would
// break it.
func (s *Server) handleDeleteOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	ctx := r.Context()

	rc, err := s.OrgStore.GetReconcilingClusterByName(ctx, org.Namespace)
	if err == orgdb.ErrNotFound {
		// A clearer message than a bare 404 when the organization IS
		// currently on some cluster, just not one this endpoint's own
		// naming convention recognizes as "its own" — e.g. a superadmin
		// assigned it a shared cluster directly via the CLI
		// (PATCH /organizations/{name}), which this endpoint deliberately
		// never touches (see this handler's own doc comment).
		if org.ReconcilingClusterID != nil {
			current, currentErr := s.OrgStore.GetReconcilingCluster(ctx, *org.ReconcilingClusterID)
			if currentErr == nil {
				writeError(w, http.StatusNotFound, fmt.Sprintf("this organization's current cluster (%q) isn't its own dedicated one — nothing to remove", current.Name))
				return
			}
		}
		writeError(w, http.StatusNotFound, "this organization has no reconciling cluster of its own registered")
		return
	} else if err != nil {
		log.Printf("api: failed to look up reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to remove reconciling cluster")
		return
	}

	if org.ReconcilingClusterID != nil && *org.ReconcilingClusterID == rc.ID {
		if _, ok := s.migrateOrganizationToTarget(w, r, org, nil); !ok {
			return
		}
	}

	others, err := s.OrgStore.ListOrganizationsByReconcilingCluster(ctx, &rc.ID)
	if err != nil {
		log.Printf("api: failed to check for other organizations on reconciling cluster %q: %v", rc.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to remove reconciling cluster")
		return
	}
	if len(others) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("reconciling cluster %q is still in use by another organization — cannot remove it", rc.Name))
		return
	}

	if err := s.OrgStore.DeleteReconcilingCluster(ctx, rc.ID); err != nil {
		log.Printf("api: failed to delete reconciling cluster %q: %v", rc.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to remove reconciling cluster")
		return
	}
	s.invalidateReconcilingClusterClientByName(ctx, org.Namespace)

	w.WriteHeader(http.StatusNoContent)
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
//     alongside real tenants. As of Milestone 10 Part A
//     (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs)
//     this namespace already has a real Organization row — seeded once,
//     at API startup (cmd/api/run.go), never through this endpoint — so
//     this check's job narrows slightly but doesn't go away: it still
//     stops a caller from re-creating or otherwise colliding with that
//     row through the ordinary tenant-creation path.
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

// ensureNamespace creates name's Namespace if it doesn't already exist —
// plainly, carrying no hyve-specific finalizer of its own. Deletion (see
// handleDeleteOrganization below) relies solely on Kubernetes' own native
// namespace-content cascade now, the same as every other deletion in this
// application; see hyvev1alpha1.OrganizationNamespaceFinalizer's own doc
// comment for why the finalizer-gated design this used to carry was
// retired (a hyve-controller instance was never guaranteed to actually be
// deployed against whatever physical cluster a self-service-registered
// organization's namespace lives on, so that gate could hang forever).
func (s *Server) ensureNamespace(ctx context.Context, c client.Client, name string) error {
	var ns corev1.Namespace
	err := c.Get(ctx, types.NamespacedName{Name: name}, &ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check existing namespace: %w", err)
	}
	ns = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
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
