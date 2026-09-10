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
	mux.HandleFunc("GET /kubeconfig", s.handleKubeconfig)
}

// handleKubeconfig resolves ?cluster=<name> and dispatches to the right
// AccessProvider — TunnelProvider or ModuleAuthProvider — per the target
// ClusterDefinition's spec.access.method. Unset (the default) isn't served
// here at all — see ClusterDefinitionSpec.Access's doc comment: that case
// is client-side auth, served by GET /api/clusters/<name>/auth-context
// instead. access.method: primary (hyvev1alpha1.AccessMethodPrimary) is
// deliberately not one of this switch's cases either: it's a pure
// host-identification marker now, not a dispatch key — a primary-marked
// cluster has a real driver module and gets its kubeconfig the same
// client-side way any other default-auth cluster does (handleAuthContext
// carries the carve-out that lets Method: primary through that path — see
// its own doc comment). See HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// Phase 6.5.
func (s *Server) handleKubeconfig(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("cluster")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster query parameter is required")
		return
	}

	var cd hyvev1alpha1.ClusterDefinition
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.TenantNamespace(r), Name: name}, &cd); err != nil {
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
