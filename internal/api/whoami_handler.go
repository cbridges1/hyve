package api

import (
	"log"
	"net/http"

	"github.com/cbridges1/hyve/internal/orgdb"
)

type whoamiResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`

	// Namespace is the session's own tenant namespace — s.Namespace (the
	// control-plane namespace) for a superadmin, who has no tenant of
	// their own (see RoleSuperadmin's doc comment), or the org resolved at
	// login for anyone else. Confirmed live: with no namespace surfaced
	// anywhere, the UI gave a logged-in caller no way to tell which tenant
	// (or the control plane) their session was actually scoped to.
	Namespace string `json:"namespace"`

	// ReconcilingCluster/Migrating mirror organizationDTO's own fields for
	// this caller's own namespace — an ordinary admin has no route to
	// GET /organizations (superadmin-only, genuinely cross-tenant by
	// design), so this is the one place they can see which cluster their
	// own organization's resources actually live on without needing
	// superadmin access at all. Empty/omitted for a namespace with no
	// registered organization (the control plane's own default view, or a
	// legacy pre-Milestone-2 namespace) — the same graceful "nothing to
	// show" fallback every other organization-aware lookup in this
	// package already uses.
	ReconcilingCluster string `json:"reconcilingCluster,omitempty"`
	Migrating          bool   `json:"migrating,omitempty"`
}

// registerWhoamiRoute wires GET /whoami — mounted under /api/ (behind
// requireAuth+requireRole) by Server.Routes, so simply reaching this
// handler at all already proves the caller's session and role resolved
// successfully; it has nothing further to check.
func (s *Server) registerWhoamiRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /whoami", s.handleWhoami)
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	username, _ := UsernameFromContext(r.Context())
	role, _ := RoleFromContext(r.Context())
	namespace := s.TenantNamespace(r)

	resp := whoamiResponse{Username: username, Role: role, Namespace: namespace}
	if s.OrgStore != nil {
		if org, err := s.OrgStore.GetOrganizationByName(r.Context(), namespace); err == nil {
			dto := s.toOrganizationDTO(r.Context(), org)
			resp.ReconcilingCluster = dto.ReconcilingCluster
			resp.Migrating = dto.Migrating
		} else if err != orgdb.ErrNotFound {
			log.Printf("api: failed to resolve organization for whoami namespace %q: %v", namespace, err)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
