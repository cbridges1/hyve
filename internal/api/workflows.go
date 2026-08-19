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

// workflowDTO is the response shape for GET /api/workflows and
// GET /api/workflows/<name> — like templateDTO, nothing here is sensitive,
// so this exposes the CRD's own Spec directly.
type workflowDTO struct {
	Name string                    `json:"name"`
	Spec hyvev1alpha1.WorkflowSpec `json:"spec"`
}

func toWorkflowDTO(cr *hyvev1alpha1.Workflow) workflowDTO {
	return workflowDTO{Name: cr.Name, Spec: cr.Spec}
}

// registerWorkflowRoutes wires the /workflows endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workflows", s.handleListWorkflows)
	mux.HandleFunc("GET /workflows/{name}", s.handleGetWorkflow)
	mux.HandleFunc("POST /workflows", s.handleCreateWorkflow)
	mux.HandleFunc("DELETE /workflows/{name}", s.handleDeleteWorkflow)
}

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.WorkflowList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list workflows: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	dtos := make([]workflowDTO, 0, len(list.Items))
	for i := range list.Items {
		dtos = append(dtos, toWorkflowDTO(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.Workflow
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		log.Printf("api: failed to get workflow %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get workflow")
		return
	}
	writeJSON(w, http.StatusOK, toWorkflowDTO(&cr))
}

// createWorkflowRequest reuses hyvev1alpha1.WorkflowSpec directly as the
// request body's spec shape, same precedent as createClusterRequest.
type createWorkflowRequest struct {
	Name string                    `json:"name"`
	Spec hyvev1alpha1.WorkflowSpec `json:"spec"`
}

func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	cr := &hyvev1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: s.Namespace},
		Spec:       req.Spec,
	}
	if err := s.Client.Create(r.Context(), cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "workflow already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create workflow: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toWorkflowDTO(cr))
}

func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	cr := &hyvev1alpha1.Workflow{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace}}
	if err := s.Client.Delete(r.Context(), cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		log.Printf("api: failed to delete workflow %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete workflow")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
