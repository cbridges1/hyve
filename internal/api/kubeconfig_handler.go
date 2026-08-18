package api

import (
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// registerKubeconfigRoutes wires GET /kubeconfig — mounted under /api/
// (behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerKubeconfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /kubeconfig", s.handleKubeconfig)
}

// handleKubeconfig resolves ?cluster=<name> and dispatches to the right
// AccessProvider — PrimaryProvider when <name> is this API's own cluster
// (s.PrimaryClusterName), otherwise TunnelProvider or ModuleAuthProvider
// per the target ClusterDefinition's spec.access.method (module-auth when
// unset). See HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.5.
func (s *Server) handleKubeconfig(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("cluster")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster query parameter is required")
		return
	}

	if s.PrimaryClusterName != "" && name == s.PrimaryClusterName {
		s.respondKubeconfig(w, r, s.PrimaryProvider, &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}})
		return
	}

	var cd hyvev1alpha1.ClusterDefinition
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}

	provider := s.ModuleAuthProvider
	if cd.Spec.Access.Method == hyvev1alpha1.AccessMethodTunnel {
		provider = s.TunnelProvider
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
