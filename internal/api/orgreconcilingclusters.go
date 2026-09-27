package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/cbridges1/hyve/internal/orgdb"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/clientcmd"
)

// Reconciling-cluster ownership as an organization sees it — the
// orgReconcilingCluster*DTO types' Ownership field.
const (
	// ownershipOrganization is one of the organization's own clusters,
	// registered with its own kubeconfig.
	ownershipOrganization = "organization"
	// ownershipPool is a superadmin pool cluster assigned to this
	// organization directly (PATCH /organizations/{name}) — visible to the
	// organization only while it's active.
	ownershipPool = "pool"
)

func ownershipOf(rc orgdb.ReconcilingCluster) string {
	if rc.OrganizationID != nil {
		return ownershipOrganization
	}
	return ownershipPool
}

// orgReconcilingClusterDTO is GET/PUT /organizations/{name}/reconciling-cluster's
// own response shape — the organization's current placement. Deliberately
// not reconcilingClusterDTO itself: an org admin has no visibility into the
// superadmin-managed pool beyond the one cluster (if any) they've been
// assigned, so OnHomeCluster stands in for "no reconciling cluster" instead
// of an empty name being ambiguous with a lookup failure.
type orgReconcilingClusterDTO struct {
	OnHomeCluster     bool    `json:"onHomeCluster"`
	Name              string  `json:"name,omitempty"`
	Ownership         string  `json:"ownership,omitempty"`
	Server            string  `json:"server,omitempty"`
	Reachable         *bool   `json:"reachable,omitempty"`
	LastCheckedAt     *string `json:"lastCheckedAt,omitempty"`
	LastError         *string `json:"lastError,omitempty"`
	KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
	RegisteredAt      string  `json:"registeredAt,omitempty"`
	Migrating         bool    `json:"migrating,omitempty"`
}

// orgReconcilingClusterEntryDTO is one row of GET
// /organizations/{name}/reconciling-clusters — every cluster this
// organization could switch to, with Active marking the current one.
type orgReconcilingClusterEntryDTO struct {
	Name              string  `json:"name"`
	Ownership         string  `json:"ownership"`
	Active            bool    `json:"active"`
	Server            string  `json:"server,omitempty"`
	Reachable         *bool   `json:"reachable,omitempty"`
	LastCheckedAt     *string `json:"lastCheckedAt,omitempty"`
	LastError         *string `json:"lastError,omitempty"`
	KubernetesVersion *string `json:"kubernetesVersion,omitempty"`
	RegisteredAt      string  `json:"registeredAt"`
}

func toOrgReconcilingClusterEntryDTO(rc orgdb.ReconcilingCluster, active bool) orgReconcilingClusterEntryDTO {
	full := toReconcilingClusterDTO(rc)
	return orgReconcilingClusterEntryDTO{
		Name:              rc.Name,
		Ownership:         ownershipOf(rc),
		Active:            active,
		Server:            full.Server,
		Reachable:         full.Reachable,
		LastCheckedAt:     full.LastCheckedAt,
		LastError:         full.LastError,
		KubernetesVersion: full.KubernetesVersion,
		RegisteredAt:      full.RegisteredAt,
	}
}

// handleGetOrgReconcilingCluster reports the named organization's current
// reconciling-cluster placement — a superadmin can target any organization
// by name; an ordinary admin only their own (see requireOrgAccess).
func (s *Server) handleGetOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	dto, err := s.orgPlacementDTO(r.Context(), org)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve organization's reconciling cluster")
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) orgPlacementDTO(ctx context.Context, org orgdb.Organization) (orgReconcilingClusterDTO, error) {
	dto := orgReconcilingClusterDTO{Migrating: org.ReconcilingClusterMigrationStatus != nil}
	if org.ReconcilingClusterID == nil {
		dto.OnHomeCluster = true
		return dto, nil
	}
	rc, err := s.OrgStore.GetReconcilingCluster(ctx, *org.ReconcilingClusterID)
	if err != nil {
		return orgReconcilingClusterDTO{}, err
	}
	entry := toOrgReconcilingClusterEntryDTO(rc, true)
	dto.Name = entry.Name
	dto.Ownership = entry.Ownership
	dto.Server = entry.Server
	dto.Reachable = entry.Reachable
	dto.LastCheckedAt = entry.LastCheckedAt
	dto.LastError = entry.LastError
	dto.KubernetesVersion = entry.KubernetesVersion
	dto.RegisteredAt = entry.RegisteredAt
	return dto, nil
}

// handleListOrgReconcilingClusters lists every cluster the organization can
// switch to — its own — plus whichever one it's on right now if that isn't
// one of them (a pool cluster a superadmin assigned directly), so the
// active cluster is always in the list.
func (s *Server) handleListOrgReconcilingClusters(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	ctx := r.Context()
	owned, err := s.OrgStore.ListOrgReconcilingClusters(ctx, org.ID)
	if err != nil {
		log.Printf("api: failed to list reconciling clusters for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to list reconciling clusters")
		return
	}

	out := make([]orgReconcilingClusterEntryDTO, 0, len(owned)+1)
	activeListed := false
	for _, rc := range owned {
		active := org.ReconcilingClusterID != nil && *org.ReconcilingClusterID == rc.ID
		activeListed = activeListed || active
		out = append(out, toOrgReconcilingClusterEntryDTO(rc, active))
	}
	if org.ReconcilingClusterID != nil && !activeListed {
		rc, err := s.OrgStore.GetReconcilingCluster(ctx, *org.ReconcilingClusterID)
		if err != nil {
			log.Printf("api: failed to resolve active reconciling cluster for organization %q: %v", org.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to list reconciling clusters")
			return
		}
		out = append(out, toOrgReconcilingClusterEntryDTO(rc, true))
	}
	writeJSON(w, http.StatusOK, out)
}

type orgReconcilingClusterRequest struct {
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
}

// validateReconcilingClusterName keeps cluster names to DNS labels — they're
// typed on the command line and shown next to organization/namespace names,
// which follow the same rule.
func validateReconcilingClusterName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("invalid reconciling cluster name %q: %s", name, errs[0])
	}
	return nil
}

// upsertOrgReconcilingCluster registers, or rotates the kubeconfig of, one
// of org's own clusters. created reports which. Writes nothing to w — the
// caller decides the response.
func (s *Server) upsertOrgReconcilingCluster(ctx context.Context, org orgdb.Organization, name, kubeconfig string) (rc orgdb.ReconcilingCluster, created bool, err error) {
	rc, err = s.OrgStore.GetOrgReconcilingClusterByName(ctx, org.ID, name)
	switch {
	case err == nil:
		if err := s.OrgStore.SetReconcilingClusterKubeconfig(ctx, rc.ID, kubeconfig); err != nil {
			return orgdb.ReconcilingCluster{}, false, err
		}
		// See handleCreateReconcilingCluster's identical invalidation.
		s.invalidateReconcilingClusterClient(rc.ID)
		rc, err = s.OrgStore.GetReconcilingCluster(ctx, rc.ID)
		return rc, false, err
	case err == orgdb.ErrNotFound:
		rc, err = s.OrgStore.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: name, OrganizationID: &org.ID, Kubeconfig: kubeconfig})
		return rc, true, err
	default:
		return orgdb.ReconcilingCluster{}, false, err
	}
}

// handleAddOrgReconcilingCluster stores one of the organization's own
// reconciling clusters by name, without switching to it — so an admin can
// keep several registered and move between them with PUT
// /organizations/{name}/reconciling-cluster {"name": ...} rather than
// re-entering a kubeconfig each time. Idempotent by name: re-posting an
// existing name rotates its kubeconfig (kubeconfigs expire/get
// regenerated), taking effect immediately if it's the active one.
func (s *Server) handleAddOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	var req orgReconcilingClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateReconcilingClusterName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Kubeconfig == "" {
		writeError(w, http.StatusBadRequest, "kubeconfig is required")
		return
	}
	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(req.Kubeconfig)); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid kubeconfig: %v", err))
		return
	}

	rc, created, err := s.upsertOrgReconcilingCluster(r.Context(), org, req.Name, req.Kubeconfig)
	if err != nil {
		log.Printf("api: failed to store reconciling cluster %q for organization %q: %v", req.Name, org.Name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store reconciling cluster: %v", err))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	active := org.ReconcilingClusterID != nil && *org.ReconcilingClusterID == rc.ID
	writeJSON(w, status, toOrgReconcilingClusterEntryDTO(rc, active))
}

// handleRemoveOrgReconcilingCluster permanently deletes one of the
// organization's own stored clusters, kubeconfig included. Refuses the
// active one with 409 — switch elsewhere first (PUT
// /organizations/{name}/reconciling-cluster), since removing it out from
// under the organization would strand every resource on it, and quietly
// migrating to some other cluster instead isn't this call's decision to
// make.
func (s *Server) handleRemoveOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	ctx := r.Context()
	name := r.PathValue("cluster")

	rc, err := s.OrgStore.GetOrgReconcilingClusterByName(ctx, org.ID, name)
	if err == orgdb.ErrNotFound {
		writeError(w, http.StatusNotFound, fmt.Sprintf("organization %q has no reconciling cluster of its own named %q", org.Name, name))
		return
	} else if err != nil {
		log.Printf("api: failed to look up reconciling cluster %q for organization %q: %v", name, org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to remove reconciling cluster")
		return
	}
	if org.ReconcilingClusterID != nil && *org.ReconcilingClusterID == rc.ID {
		writeError(w, http.StatusConflict, fmt.Sprintf("reconciling cluster %q is the organization's active one — switch to another cluster first", name))
		return
	}
	if err := s.OrgStore.DeleteReconcilingCluster(ctx, rc.ID); err != nil {
		log.Printf("api: failed to delete reconciling cluster %q for organization %q: %v", name, org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to remove reconciling cluster")
		return
	}
	s.invalidateReconcilingClusterClient(rc.ID)
	w.WriteHeader(http.StatusNoContent)
}

// handlePutOrgReconcilingCluster switches the organization's active
// reconciling cluster — the organization-admin-facing counterpart to PATCH
// /organizations/{name} (which picks from the superadmin pool). Switching
// runs the same copy-then-flip migration either way (see
// migrateOrganizationToTarget). The request selects the target:
//
//   - {"name": "x"}: one of the organization's own stored clusters. The
//     organization's current cluster also matches by name even if it's a
//     pool cluster a superadmin assigned (a no-op).
//   - {"name": "x", "kubeconfig": "..."}: store (or rotate) its own cluster
//     x, then switch to it — one call for a first-time setup.
//   - {"kubeconfig": "..."}: the same, named after the organization's
//     namespace — the one-cluster-per-organization request shape this
//     endpoint originally took, kept working unchanged.
//   - {}: back to the control plane's own home cluster.
//
// A superadmin can target any organization by name; an ordinary admin only
// their own (see requireOrgAccess).
func (s *Server) handlePutOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	var req orgReconcilingClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()

	var target *string
	switch {
	case req.Kubeconfig != "":
		name := req.Name
		if name == "" {
			name = org.Namespace
		}
		if err := validateReconcilingClusterName(name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(req.Kubeconfig)); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid kubeconfig: %v", err))
			return
		}
		rc, _, err := s.upsertOrgReconcilingCluster(ctx, org, name, req.Kubeconfig)
		if err != nil {
			log.Printf("api: failed to store reconciling cluster %q for organization %q: %v", name, org.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to update reconciling cluster")
			return
		}
		target = &rc.ID
	case req.Name != "":
		rc, err := s.resolveOrgSelectableCluster(ctx, org, req.Name)
		if err == orgdb.ErrNotFound {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no reconciling cluster named %q is available to organization %q", req.Name, org.Name))
			return
		} else if err != nil {
			log.Printf("api: failed to resolve reconciling cluster %q for organization %q: %v", req.Name, org.Name, err)
			writeError(w, http.StatusInternalServerError, "failed to update reconciling cluster")
			return
		}
		target = &rc.ID
	}

	if _, ok := s.migrateOrganizationToTarget(w, r, org, target); !ok {
		return
	}
	s.writeOrgPlacement(w, r, org.ID)
}

// resolveOrgSelectableCluster finds the cluster name refers to for org, as
// handlePutOrgReconcilingCluster documents.
func (s *Server) resolveOrgSelectableCluster(ctx context.Context, org orgdb.Organization, name string) (orgdb.ReconcilingCluster, error) {
	rc, err := s.OrgStore.GetOrgReconcilingClusterByName(ctx, org.ID, name)
	if err != orgdb.ErrNotFound {
		return rc, err
	}
	if org.ReconcilingClusterID != nil {
		current, err := s.OrgStore.GetReconcilingCluster(ctx, *org.ReconcilingClusterID)
		if err != nil {
			return orgdb.ReconcilingCluster{}, err
		}
		if current.Name == name {
			return current, nil
		}
	}
	return orgdb.ReconcilingCluster{}, orgdb.ErrNotFound
}

// handleDeleteOrgReconcilingCluster moves the organization back onto the
// control plane's own home cluster — the same as PUT with an empty body.
// Its stored clusters are kept; removing one is DELETE
// /organizations/{name}/reconciling-clusters/{cluster}.
func (s *Server) handleDeleteOrgReconcilingCluster(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgAccess(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	if _, ok := s.migrateOrganizationToTarget(w, r, org, nil); !ok {
		return
	}
	s.writeOrgPlacement(w, r, org.ID)
}

// writeOrgPlacement reloads the organization after a switch and writes its
// placement — the one response shape every reconciling-cluster switch
// returns, so a client never needs a second GET to see where it landed.
func (s *Server) writeOrgPlacement(w http.ResponseWriter, r *http.Request, orgID string) {
	ctx := r.Context()
	org, err := s.OrgStore.GetOrganization(ctx, orgID)
	if err != nil {
		log.Printf("api: failed to reload organization %q after switching reconciling cluster: %v", orgID, err)
		writeError(w, http.StatusInternalServerError, "reconciling cluster switched, but reloading the organization failed")
		return
	}
	dto, err := s.orgPlacementDTO(ctx, org)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "reconciling cluster switched, but resolving it for the response failed")
		return
	}
	writeJSON(w, http.StatusOK, dto)
}
