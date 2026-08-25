package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// resourceDTO is the response shape for GET /api/resources and
// GET /api/resources/<name> — mirrors workflowDTO exactly, including the
// Spec-vs-RefStatus split (see that type's doc comment).
type resourceDTO struct {
	Name      string                     `json:"name"`
	Spec      *hyvev1alpha1.ResourceSpec `json:"spec,omitempty"`
	RefStatus *resourceRefStatusDTO      `json:"refStatus,omitempty"`
}

// resourceRefStatusDTO mirrors workflowRefStatusDTO, minus
// ResolvedVersion — ResourceRefStatusStatus has no such field.
type resourceRefStatusDTO struct {
	Source     string `json:"source"`
	Resolved   bool   `json:"resolved"`
	RawVersion string `json:"rawVersion,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Error      string `json:"error,omitempty"`
}

func toResourceDTO(cr *hyvev1alpha1.Resource) resourceDTO {
	spec := cr.Spec
	return resourceDTO{Name: cr.Name, Spec: &spec}
}

// toResourceRefStatusDTO mirrors toWorkflowRefStatusDTO — see its doc
// comment for why Name comes from spec.Name, not cr.Name.
func toResourceRefStatusDTO(cr *hyvev1alpha1.ResourceRefStatus) resourceDTO {
	return resourceDTO{
		Name: cr.Spec.Name,
		RefStatus: &resourceRefStatusDTO{
			Source:     cr.Spec.Source,
			Resolved:   cr.Status.Resolved,
			RawVersion: cr.Status.RawVersion,
			SHA256:     cr.Status.SHA256,
			Error:      cr.Status.Error,
		},
	}
}

// registerResourceRoutes wires the /resources endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerResourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /resources", s.handleListResources)
	mux.HandleFunc("GET /resources/{name}", s.handleGetResource)
	mux.HandleFunc("POST /resources", s.handleCreateResource)
	mux.HandleFunc("DELETE /resources/{name}", s.handleDeleteResource)
}

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.ResourceList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list resources: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	var refStatusList hyvev1alpha1.ResourceRefStatusList
	if err := s.Client.List(r.Context(), &refStatusList, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list resource ref statuses: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	dtos := make([]resourceDTO, 0, len(list.Items)+len(refStatusList.Items))
	for i := range list.Items {
		dtos = append(dtos, toResourceDTO(&list.Items[i]))
	}
	for i := range refStatusList.Items {
		dtos = append(dtos, toResourceRefStatusDTO(&refStatusList.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.Resource
	err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cr)
	if err == nil {
		writeJSON(w, http.StatusOK, toResourceDTO(&cr))
		return
	}
	if !apierrors.IsNotFound(err) {
		log.Printf("api: failed to get resource %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get resource")
		return
	}

	// Not a real Resource CR — check whether it's a git-referenced one
	// instead, mirrored under a derived metadata.name (see
	// toResourceRefStatusDTO). If more than one ref shares this short
	// Name (see resourceref.NameCollision), the first match wins — full
	// disambiguation isn't supported here yet.
	var refStatusList hyvev1alpha1.ResourceRefStatusList
	if err := s.Client.List(r.Context(), &refStatusList, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list resource ref statuses: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get resource")
		return
	}
	for i := range refStatusList.Items {
		if refStatusList.Items[i].Spec.Name == name {
			writeJSON(w, http.StatusOK, toResourceRefStatusDTO(&refStatusList.Items[i]))
			return
		}
	}
	writeError(w, http.StatusNotFound, "resource not found")
}

// createResourceRequest reuses hyvev1alpha1.ResourceSpec directly as the
// request body's spec shape, same precedent as createWorkflowRequest.
type createResourceRequest struct {
	Name string                    `json:"name"`
	Spec hyvev1alpha1.ResourceSpec `json:"spec"`
}

func (s *Server) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	cr := &hyvev1alpha1.Resource{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: s.Namespace},
		Spec:       req.Spec,
	}
	if err := s.Client.Create(r.Context(), cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "resource already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create resource: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toResourceDTO(cr))
}

func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	cr := &hyvev1alpha1.Resource{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace}}
	if err := s.Client.Delete(r.Context(), cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		log.Printf("api: failed to delete resource %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete resource")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
