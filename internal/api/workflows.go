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
// GET /api/workflows/<name>. Spec is populated for a real, hand-authored
// Workflow CR; RefStatus is populated instead for a git-referenced workflow
// that was never a Workflow CR at all — mirrored onto a WorkflowRefStatus CR
// by the controller purely for this listing (see that kind's own doc
// comment). The two are mutually exclusive on any one row.
//
// Deliberately not environment-scoped — see templateDTO's own doc comment
// for why: a Workflow is a reusable automation step definition, referenced
// by name from any Template/ClusterDefinition regardless of which
// environment it lands in, not a live per-environment object.
type workflowDTO struct {
	// Name is the short, user-facing name — see clusterDTO's own doc
	// comment on Name for the exact same convention. Never joined/split
	// for a RefStatus row (see toWorkflowRefStatusDTO's own doc comment on
	// why git-referenced workflows are outside this naming scheme).
	Name      string                     `json:"name"`
	Spec      *hyvev1alpha1.WorkflowSpec `json:"spec,omitempty"`
	RefStatus *workflowRefStatusDTO      `json:"refStatus,omitempty"`
}

// workflowRefStatusDTO mirrors WorkflowRefStatusStatus — nothing sensitive,
// exposed directly.
type workflowRefStatusDTO struct {
	Source          string `json:"source"`
	Resolved        bool   `json:"resolved"`
	RawVersion      string `json:"rawVersion,omitempty"`
	ResolvedVersion string `json:"resolvedVersion,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	Error           string `json:"error,omitempty"`
}

func toWorkflowDTO(cr *hyvev1alpha1.Workflow) workflowDTO {
	spec := cr.Spec
	return workflowDTO{Name: cr.Name, Spec: &spec}
}

// toWorkflowRefStatusDTO builds a workflowDTO row from a mirrored
// WorkflowRefStatus CR. Name comes from spec.Name (the declared ref's short
// name), not cr.Name — cr.Name is a derived, collision-safe slug (see
// module.CRName), never the human-facing identity.
func toWorkflowRefStatusDTO(cr *hyvev1alpha1.WorkflowRefStatus) workflowDTO {
	return workflowDTO{
		Name: cr.Spec.Name,
		RefStatus: &workflowRefStatusDTO{
			Source:          cr.Spec.Source,
			Resolved:        cr.Status.Resolved,
			RawVersion:      cr.Status.RawVersion,
			ResolvedVersion: cr.Status.ResolvedVersion,
			SHA256:          cr.Status.SHA256,
			Error:           cr.Status.Error,
		},
	}
}

// registerWorkflowRoutes wires the /workflows endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workflows", s.requireOrganizationNotMigrating(s.handleListWorkflows))
	mux.HandleFunc("GET /workflows/{name}", s.requireOrganizationNotMigrating(s.handleGetWorkflow))
	mux.HandleFunc("POST /workflows", s.requireOrganizationNotMigrating(s.handleCreateWorkflow))
	mux.HandleFunc("PATCH /workflows/{name}", s.requireOrganizationNotMigrating(s.handleUpdateWorkflow))
	mux.HandleFunc("DELETE /workflows/{name}", s.requireOrganizationNotMigrating(s.handleDeleteWorkflow))
}

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	var list hyvev1alpha1.WorkflowList
	if err := rc.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list workflows: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	var refStatusList hyvev1alpha1.WorkflowRefStatusList
	if err := rc.List(ctx, &refStatusList, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list workflow ref statuses: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	dtos := make([]workflowDTO, 0, len(list.Items)+len(refStatusList.Items))
	for i := range list.Items {
		dtos = append(dtos, toWorkflowDTO(&list.Items[i]))
	}
	for i := range refStatusList.Items {
		dtos = append(dtos, toWorkflowRefStatusDTO(&refStatusList.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := r.PathValue("name")
	rc, rcErr := s.resourceClient(ctx, namespace)
	if rcErr != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, rcErr)
		writeError(w, http.StatusInternalServerError, "failed to get workflow")
		return
	}
	var cr hyvev1alpha1.Workflow
	err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cr)
	if err == nil {
		writeJSON(w, http.StatusOK, toWorkflowDTO(&cr))
		return
	}
	if !apierrors.IsNotFound(err) {
		log.Printf("api: failed to get workflow %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get workflow")
		return
	}

	// Not a real Workflow CR — check whether it's a git-referenced one
	// instead, mirrored under a derived metadata.name (see
	// toWorkflowRefStatusDTO), so this has to be a filtered List, not a
	// direct Get. If more than one ref shares this short Name (see
	// workflowref.NameCollision), the first match wins — full
	// disambiguation isn't supported here yet.
	var refStatusList hyvev1alpha1.WorkflowRefStatusList
	if err := rc.List(ctx, &refStatusList, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list workflow ref statuses: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get workflow")
		return
	}
	for i := range refStatusList.Items {
		if refStatusList.Items[i].Spec.Name == name {
			writeJSON(w, http.StatusOK, toWorkflowRefStatusDTO(&refStatusList.Items[i]))
			return
		}
	}
	writeError(w, http.StatusNotFound, "workflow not found")
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

	namespace := s.TenantNamespace(r)
	rc, err := s.resourceClient(r.Context(), namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to create workflow")
		return
	}
	cr := &hyvev1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: namespace},
		Spec:       req.Spec,
	}
	if err := rc.Create(r.Context(), cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "workflow already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create workflow: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toWorkflowDTO(cr))
}

// updateWorkflowRequest mirrors createWorkflowRequest's Spec shape — a
// full-spec replace, not a merge-patch.
type updateWorkflowRequest struct {
	Spec hyvev1alpha1.WorkflowSpec `json:"spec"`
}

// handleUpdateWorkflow only ever finds a real Workflow CR named `name` —
// a git-ref-backed workflow lives under a derived name as a
// WorkflowRefStatus, never as a Workflow CR itself (see toWorkflowRefStatusDTO's
// doc comment), so this naturally 404s for that case with no separate
// detection needed, the same way handleDeleteWorkflow's identical Get-by-name
// already does today.
func (s *Server) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := r.PathValue("name")
	var req updateWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to update workflow")
		return
	}
	var cr hyvev1alpha1.Workflow
	if err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		log.Printf("api: failed to get workflow %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get workflow")
		return
	}
	cr.Spec = req.Spec
	if err := rc.Update(ctx, &cr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to update workflow: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, toWorkflowDTO(&cr))
}

func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := r.PathValue("name")
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to delete workflow")
		return
	}
	cr := &hyvev1alpha1.Workflow{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := rc.Delete(ctx, cr); err != nil {
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
