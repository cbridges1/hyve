package api

import (
	"log"
	"net/http"
	"sort"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
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

	// Organization is the name of the organization owning Namespace —
	// usually identical, but an organization can be renamed while its
	// namespace can't, and /organizations/{name}/... routes take the name.
	// Empty under the same conditions as ReconcilingCluster below.
	Organization string `json:"organization,omitempty"`

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

	// Organizations is every organization this login can act in, by name
	// — the caller's memberships, or for a superadmin every organization —
	// with the caller's role in each. Namespace/Organization above is the
	// one this request acted in (see resolveAccess); pick another with the
	// X-Hyve-Organization header.
	Organizations []whoamiOrganization `json:"organizations"`
}

type whoamiOrganization struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Role      string `json:"role"`
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
			resp.Organization = org.Name
			resp.ReconcilingCluster = dto.ReconcilingCluster
			resp.Migrating = dto.Migrating
		} else if err != orgdb.ErrNotFound {
			log.Printf("api: failed to resolve organization for whoami namespace %q: %v", namespace, err)
		}
	}
	resp.Organizations = s.accessibleOrganizations(r, username)
	writeJSON(w, http.StatusOK, resp)
}

// accessibleOrganizations lists the organizations username can act in, by
// name: each membership's organization (a namespace with no registered
// organization is listed under its namespace), plus — for a superadmin —
// every organization, as superadmin (which outranks any membership).
func (s *Server) accessibleOrganizations(r *http.Request, username string) []whoamiOrganization {
	out := []whoamiOrganization{}
	if s.OrgStore == nil {
		return out
	}
	ctx := r.Context()
	memberships, err := s.OrgStore.ListBindingsForIdentity(ctx, orgdb.SubjectTypeLocal, username)
	if err != nil {
		log.Printf("api: failed to list memberships for %q: %v", username, err)
		return out
	}
	byNamespace := map[string]string{}
	superadmin := false
	for _, b := range memberships {
		if roleRank(b.Role) > roleRank(byNamespace[b.Namespace]) {
			byNamespace[b.Namespace] = b.Role
		}
		if b.Namespace == s.Namespace && b.Role == hyvev1alpha1.RoleSuperadmin {
			superadmin = true
		}
	}
	orgs, err := s.OrgStore.ListOrganizations(ctx)
	if err != nil {
		log.Printf("api: failed to list organizations: %v", err)
	}
	names := map[string]string{}
	for _, org := range orgs {
		names[org.Namespace] = org.Name
		if superadmin {
			byNamespace[org.Namespace] = hyvev1alpha1.RoleSuperadmin
		}
	}
	for ns, role := range byNamespace {
		name := names[ns]
		if name == "" {
			name = ns
		}
		out = append(out, whoamiOrganization{Name: name, Namespace: ns, Role: role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
