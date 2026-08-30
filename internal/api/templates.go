package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/template"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// templateDTO is the response shape for GET /api/templates and
// GET /api/templates/<name> — unlike clusterDTO, nothing here is
// sensitive (no driverOutputs/kubeconfig-equivalent), so this exposes the
// CRD's own Spec directly rather than a narrower hand-picked view.
type templateDTO struct {
	Name string                    `json:"name"`
	Spec hyvev1alpha1.TemplateSpec `json:"spec"`
}

func toTemplateDTO(cr *hyvev1alpha1.Template) templateDTO {
	return templateDTO{Name: cr.Name, Spec: cr.Spec}
}

// registerTemplateRoutes wires the /templates endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerTemplateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /templates", s.handleListTemplates)
	mux.HandleFunc("GET /templates/{name}", s.handleGetTemplate)
	mux.HandleFunc("POST /templates", s.handleCreateTemplate)
	mux.HandleFunc("DELETE /templates/{name}", s.handleDeleteTemplate)
	mux.HandleFunc("POST /templates/{name}/render", s.handleRenderTemplate)
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.TemplateList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list templates: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list templates")
		return
	}
	dtos := make([]templateDTO, 0, len(list.Items))
	for i := range list.Items {
		dtos = append(dtos, toTemplateDTO(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.Template
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		log.Printf("api: failed to get template %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get template")
		return
	}
	writeJSON(w, http.StatusOK, toTemplateDTO(&cr))
}

// createTemplateRequest reuses hyvev1alpha1.TemplateSpec directly as the
// request body's spec shape, same precedent as createClusterRequest.
type createTemplateRequest struct {
	Name string                    `json:"name"`
	Spec hyvev1alpha1.TemplateSpec `json:"spec"`
}

func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	cr := &hyvev1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: s.Namespace},
		Spec:       req.Spec,
	}
	if err := s.Client.Create(r.Context(), cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "template already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create template: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toTemplateDTO(cr))
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	cr := &hyvev1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace}}
	if err := s.Client.Delete(r.Context(), cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		log.Printf("api: failed to delete template %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete template")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renderTemplateRequest is POST /templates/<name>/render's body — a
// preview-only render, no cluster is created.
type renderTemplateRequest struct {
	Region string            `json:"region,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

// handleRenderTemplate resolves the named Template CR and renders it into a
// ClusterDefinitionSpec via the same hyvev1alpha1.RenderClusterDefinitionSpec
// function local mode's `hyve cluster create --template` uses — no cluster
// object is created here, this is purely a preview. Gated RoleAdmin, same
// as create/delete: only an admin can actually act on the result anyway
// (POST /api/clusters is admin-only), so there's little value in a looser
// gate here.
func (s *Server) handleRenderTemplate(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	var cr hyvev1alpha1.Template
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		log.Printf("api: failed to get template %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get template")
		return
	}

	var req renderTemplateRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	spec := hyvev1alpha1.RenderClusterDefinitionSpec(cr.Spec, req.Region, req.Params)

	// Same schedule -> expiresAt computation handleCreateCluster applies —
	// see its own comment for why this can't live inside
	// RenderClusterDefinitionSpec itself (import cycle). Kept here too so
	// this preview actually reflects what POST /clusters with the same
	// template would produce, not a stale spec missing expiresAt.
	if cr.Spec.Schedule != "" {
		next, err := template.CronNextOccurrence(cr.Spec.Schedule, time.Now())
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid schedule %q on template %q: %v", cr.Spec.Schedule, name, err))
			return
		}
		spec.ExpiresAt = next.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, spec)
}
