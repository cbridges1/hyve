package api

import "net/http"

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
	writeJSON(w, http.StatusOK, whoamiResponse{Username: username, Role: role, Namespace: s.TenantNamespace(r)})
}
