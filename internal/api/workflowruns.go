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
)

// registerWorkflowRunRoutes wires the /workflow-runs endpoints onto mux —
// mounted under /api/ (behind requireAuth+requireRole) by Server.Routes.
// This is cluster mode's execution surface for `hyve workflow run` (see
// cmd/workflow/cmd.go): the API only ever creates/reads a WorkflowRun CR —
// WorkflowRunReconciler (internal/controller) does the actual work, exactly
// like POST /clusters only ever creates a ClusterDefinition CR.
func (s *Server) registerWorkflowRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /workflow-runs", s.handleCreateWorkflowRun)
	mux.HandleFunc("GET /workflow-runs/{name}", s.handleGetWorkflowRun)
}

// createWorkflowRunRequest mirrors WorkflowRef's Name/Source/Path shape —
// the caller supplies exactly one of Workflow (a local name) or Source (a
// remote ref string, optionally with Path), matching what local-mode
// `hyve workflow run` already accepts (see cmd/workflow/run_remote.go's
// looksLikeRemoteSource).
type createWorkflowRunRequest struct {
	Workflow string            `json:"workflow,omitempty"`
	Source   string            `json:"source,omitempty"`
	Path     string            `json:"path,omitempty"`
	Cluster  string            `json:"cluster"`
	Params   map[string]string `json:"params,omitempty"`
}

type createWorkflowRunResponse struct {
	Name string `json:"name"`
}

// handleCreateWorkflowRun creates a WorkflowRun CR — admin-only, same
// stance handleCreateWorkflow already takes: this dispatches arbitrary
// shell against a real cluster, not a read.
func (s *Server) handleCreateWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createWorkflowRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster is required")
		return
	}
	if (req.Workflow == "") == (req.Source == "") {
		writeError(w, http.StatusBadRequest, "exactly one of workflow or source is required")
		return
	}

	name, err := randomHex(8)
	if err != nil {
		log.Printf("api: failed to generate workflow run name: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create workflow run")
		return
	}
	base := req.Workflow
	if base == "" {
		base = "run"
	}
	cr := &hyvev1alpha1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: base + "-" + name, Namespace: s.TenantNamespace(r)},
		Spec: hyvev1alpha1.WorkflowRunSpec{
			WorkflowRef: hyvev1alpha1.WorkflowRef{Name: req.Workflow, Source: req.Source, Path: req.Path},
			ClusterRef:  req.Cluster,
			Params:      req.Params,
		},
	}
	if err := s.Client.Create(r.Context(), cr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create workflow run: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, createWorkflowRunResponse{Name: cr.Name})
}

// workflowRunStatusDTO mirrors WorkflowRunStatus — nothing sensitive,
// exposed directly.
type workflowRunStatusDTO struct {
	Phase       string       `json:"phase"`
	Message     string       `json:"message,omitempty"`
	Output      string       `json:"output,omitempty"`
	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// handleGetWorkflowRun polls one WorkflowRun's status — no role
// restriction beyond requireRole's own authenticated+bound check, matching
// handleGetWorkflow's read stance.
func (s *Server) handleGetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.WorkflowRun
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.TenantNamespace(r), Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "workflow run not found")
			return
		}
		log.Printf("api: failed to get workflow run %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get workflow run")
		return
	}
	writeJSON(w, http.StatusOK, workflowRunStatusDTO{
		Phase:       cr.Status.Phase,
		Message:     cr.Status.Message,
		Output:      cr.Status.Output,
		StartedAt:   cr.Status.StartedAt,
		CompletedAt: cr.Status.CompletedAt,
	})
}
