package api

import (
	"fmt"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// registerKubeconfigRoutes wires GET /kubeconfig — mounted under /api/
// (behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerKubeconfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /kubeconfig", s.requireOrganizationNotMigrating(s.handleKubeconfig))
}

// handleKubeconfig resolves ?cluster=<name> and dispatches to the right
// AccessProvider — HostProvider, TunnelProvider, or ModuleAuthProvider —
// per the target ClusterDefinition's spec.access.method. Unset (the
// default) isn't served here at all — see ClusterDefinitionSpec.Access's
// doc comment: that case is client-side auth, served by
// GET /api/clusters/<name>/auth-context instead. See
// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5.
//
// The host ClusterDefinition (access.method: primary) always lives in
// s.Namespace (the install's control-plane namespace), never a tenant
// namespace. Looked up here via s.TenantNamespace(r), same as any other
// cluster — which already resolves to s.Namespace for a superadmin caller
// (they have no tenant namespace of their own, see RoleSuperadmin's doc
// comment) and to the caller's own tenant namespace otherwise, so an
// ordinary tenant admin's lookup simply never finds it: invisible by
// construction, not merely by the role check HostProvider itself also
// enforces.
func (s *Server) handleKubeconfig(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("cluster")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster query parameter is required")
		return
	}

	ns := s.TenantNamespace(r)
	c, err := s.resourceClient(r.Context(), ns)
	if err != nil {
		log.Printf("api: failed to resolve resource client for cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	var cd hyvev1alpha1.ClusterDefinition
	if err := c.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}

	var provider AccessProvider
	switch {
	// Agent/Proxy is deliberately orthogonal to Access.Method (see
	// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Relationship to
	// existing access paths") — checked ahead of the Method switch below
	// entirely, not folded into it, so a cluster can have Proxy enabled
	// regardless of whatever Method happens to be set (or unset) for
	// whatever other access path it may also support.
	case cd.Spec.Access.Agent != nil && cd.Spec.Access.Agent.Proxy:
		provider = s.AgentProvider
	// A primary-marked cluster with no real spec.driver (the common,
	// zero-config case — see HostProvider's own doc comment) is served
	// here, automatically, with no module involved at all. One WITH a
	// real driver (an admin's deliberate opt-out of the automatic path)
	// falls through this switch entirely — Method stays "primary" either
	// way, since cmd/migrate_resolve.go depends on it as a host-
	// identification marker regardless of how auth actually works — and
	// is served by handleAuthContext's own matching carve-out instead
	// (client-side, like any other driver-having cluster).
	case cd.Spec.Access.Method == hyvev1alpha1.AccessMethodPrimary && cd.Spec.Driver.Source == "":
		provider = s.HostProvider
	case cd.Spec.Access.Method == hyvev1alpha1.AccessMethodTunnel:
		provider = s.TunnelProvider
	case cd.Spec.Access.Method == hyvev1alpha1.AccessMethodModuleAuth:
		provider = s.ModuleAuthProvider
	default:
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"cluster %q uses client-side auth (the default) — run `hyve cluster auth %s` to fetch driver info via GET /api/clusters/%s/auth-context and run the module locally, or set spec.access.method: module-auth to override to server-side auth",
			name, name, name))
		return
	}
	if provider == nil {
		writeError(w, http.StatusInternalServerError, "no access provider configured for this cluster's access method")
		return
	}
	s.respondKubeconfig(w, r, provider, &cd)
}

func (s *Server) respondKubeconfig(w http.ResponseWriter, r *http.Request, provider AccessProvider, cd *hyvev1alpha1.ClusterDefinition) {
	kc, err := provider.Kubeconfig(r.Context(), cd)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(kc)
}
