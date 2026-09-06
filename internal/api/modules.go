package api

import (
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// moduleDTO is the response shape for GET /api/modules and
// GET /api/modules/<name> — read-only, since the API never writes Module
// CRs (only the controller does, as a side effect of auto-resolving a
// driver module — see internal/controller/reconciler.go's
// resolveModuleIfNeeded). Nothing here is sensitive, so this exposes the
// CRD's own Spec/Status directly rather than a narrower hand-picked view,
// same precedent as templateDTO.
type moduleDTO struct {
	Name   string                    `json:"name"`
	Spec   hyvev1alpha1.ModuleSpec   `json:"spec"`
	Status hyvev1alpha1.ModuleStatus `json:"status"`
}

func toModuleDTO(cr *hyvev1alpha1.Module) moduleDTO {
	return moduleDTO{Name: cr.Name, Spec: cr.Spec, Status: cr.Status}
}

// registerModuleRoutes wires the /modules endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
// Read-only: no create/delete, since resolution is automatic, not
// user-triggered — see this session's design discussion on why cluster
// mode has no `hyve module install` equivalent.
func (s *Server) registerModuleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /modules", s.handleListModules)
	mux.HandleFunc("GET /modules/{name}", s.handleGetModule)
}

func (s *Server) handleListModules(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.ModuleList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.TenantNamespace(r))); err != nil {
		log.Printf("api: failed to list modules: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list modules")
		return
	}
	dtos := make([]moduleDTO, 0, len(list.Items))
	for i := range list.Items {
		dtos = append(dtos, toModuleDTO(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetModule(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.Module
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.TenantNamespace(r), Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "module not found")
			return
		}
		log.Printf("api: failed to get module %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get module")
		return
	}
	writeJSON(w, http.StatusOK, toModuleDTO(&cr))
}
