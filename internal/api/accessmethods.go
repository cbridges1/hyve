package api

import (
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// accessMethodDTO is the response shape for GET /api/access-methods and
// GET /api/access-methods/<name> — read-only, since admins own writes via
// kubectl apply directly (see AccessMethod's own doc comment). Nothing
// here is sensitive (an AccessMethod never holds a credential, only where
// to reach a provider — see HYVE-ACCESS-METHOD-DESIGN.md), so this exposes
// spec directly rather than a narrower hand-picked view, same precedent as
// moduleDTO/templateDTO.
type accessMethodDTO struct {
	Name string                        `json:"name"`
	Spec hyvev1alpha1.AccessMethodSpec `json:"spec"`
}

func toAccessMethodDTO(cr *hyvev1alpha1.AccessMethod) accessMethodDTO {
	return accessMethodDTO{Name: cr.Name, Spec: cr.Spec}
}

// registerAccessMethodRoutes wires the /access-methods endpoints onto mux —
// mounted under /api/ (and behind requireAuth+requireRole) by
// Server.Routes. Read-only: `hyve cluster auth` resolves a
// ClusterDefinition's accessMethodRef through this lookup in cluster mode
// (the local-mode equivalent is internal/accessmethod.Manager reading a
// local file directly) — no create/delete, since admins manage these
// directly via kubectl, the same stance HyveConfig already takes.
func (s *Server) registerAccessMethodRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /access-methods", s.handleListAccessMethods)
	mux.HandleFunc("GET /access-methods/{name}", s.handleGetAccessMethod)
}

func (s *Server) handleListAccessMethods(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.AccessMethodList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list access methods: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list access methods")
		return
	}
	dtos := make([]accessMethodDTO, 0, len(list.Items))
	for i := range list.Items {
		dtos = append(dtos, toAccessMethodDTO(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetAccessMethod(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.AccessMethod
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "access method not found")
			return
		}
		log.Printf("api: failed to get access method %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get access method")
		return
	}
	writeJSON(w, http.StatusOK, toAccessMethodDTO(&cr))
}
