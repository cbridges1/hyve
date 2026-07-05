package handler

import (
	"context"
	"net/http"

	"github.com/cbridges1/hyve/internal/execution"
	"github.com/cbridges1/hyve/internal/workflow"
	"github.com/cbridges1/hyve/internal/workflowref"
)

// WorkflowsHandlers backs the /workflows routes.
type WorkflowsHandlers struct {
	*Deps
}

func NewWorkflowsHandlers(deps *Deps) *WorkflowsHandlers { return &WorkflowsHandlers{deps} }

func (h *WorkflowsHandlers) manager() (*workflow.Manager, error) {
	return workflow.NewManager(h.RepoPath)
}

// List handles GET /workflows.
func (h *WorkflowsHandlers) List(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	workflows, err := mgr.ListWorkflows()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if workflows == nil {
		workflows = []*workflow.Workflow{}
	}
	writeJSON(w, http.StatusOK, workflows)
}

// Get handles GET /workflows/:name.
func (h *WorkflowsHandlers) Get(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	wf, err := mgr.GetWorkflow(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeResource(w, r, http.StatusOK, wf, nil)
}

// Create handles POST /workflows — body is Workflow YAML or JSON.
func (h *WorkflowsHandlers) Create(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var wf workflow.Workflow
	if _, ok := decodeResource(w, r, &wf); !ok {
		return
	}
	if err := mgr.CreateWorkflow(&wf); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResource(w, r, http.StatusCreated, wf, nil)
}

// Put handles PUT /workflows/:name — full replace.
func (h *WorkflowsHandlers) Put(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := r.PathValue("name")
	var wf workflow.Workflow
	if _, ok := decodeResource(w, r, &wf); !ok {
		return
	}
	wf.Metadata.Name = name
	if err := mgr.UpdateWorkflow(&wf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeResource(w, r, http.StatusOK, wf, nil)
}

// Delete handles DELETE /workflows/:name.
func (h *WorkflowsHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := mgr.DeleteWorkflow(r.PathValue("name")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Validate handles POST /workflows/:name/validate.
func (h *WorkflowsHandlers) Validate(w http.ResponseWriter, r *http.Request) {
	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	wf, err := mgr.GetWorkflow(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	errs, warnings := workflow.Validate(wf)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"errors":   errs,
		"warnings": warnings,
		"valid":    len(errs) == 0,
	})
}

type runWorkflowRequest struct {
	Cluster string            `json:"cluster,omitempty"`
	Inputs  map[string]string `json:"inputs,omitempty"`
}

// Run handles POST /workflows/:name/run — launches the workflow
// asynchronously (tracked by the execution registry) and returns
// immediately, mirroring `hyve workflow run <name>`.
func (h *WorkflowsHandlers) Run(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req runWorkflowRequest
	if r.ContentLength != 0 {
		if !readJSON(w, r, &req) {
			return
		}
	}

	mgr, err := h.manager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	executor, err := workflow.NewExecutor(mgr, req.Cluster)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(req.Inputs) > 0 {
		executor.InjectVars(req.Inputs)
	}

	exec := h.Registry.New(execution.KindWorkflow)
	executor.Output = exec
	go func() {
		defer executor.Close()
		_, runErr := executor.RunWorkflow(context.Background(), name, req.Cluster)
		exec.Finish(runErr)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"executionId": exec.ID})
}

// Install handles POST /workflows/install — resolves every remote workflow
// reference found in templates/clusters into hyve.lock, mirroring `hyve
// workflow install`. Runs asynchronously like Run/reconcile since resolving
// many remote sources can take a while.
func (h *WorkflowsHandlers) Install(w http.ResponseWriter, r *http.Request) {
	exec := h.Registry.New(execution.KindWorkflowInstall)
	go func() {
		refs, err := workflowref.GatherWorkflowRefs(h.StateMgr, h.RepoPath)
		if err != nil {
			exec.Finish(err)
			return
		}
		locked, collisions, resolveErrors, changed, err := workflowref.Install(h.RepoPath, refs)
		for _, c := range collisions {
			exec.Write([]byte("warning: workflow name \"" + c.Name + "\" is provided by both " + c.FirstSource + " and " + c.CollidedSource + "\n"))
		}
		for _, e := range resolveErrors {
			exec.Write([]byte("warning: failed to resolve " + e + "\n"))
		}
		for _, l := range locked {
			exec.Write([]byte("locked " + l.CanonicalSource + "@" + l.RawVersion + " (name=" + l.Name + ", sha256=" + l.SHA256 + ")\n"))
		}
		if err != nil {
			exec.Finish(err)
			return
		}
		if changed {
			softCommit(context.Background(), h.Deps, "chore: update hyve.lock (workflows)")
		}
		exec.Finish(nil)
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"executionId": exec.ID})
}

type updateWorkflowRefRequest struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

// UpdateRef handles POST /workflows/refs/update — re-resolves one remote
// workflow reference to latest, mirroring `hyve workflow update <source>`.
func (h *WorkflowsHandlers) UpdateRef(w http.ResponseWriter, r *http.Request) {
	var req updateWorkflowRefRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	updated, err := workflowref.Update(h.RepoPath, req.Source, req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "chore: update workflow "+req.Source)
	writeJSON(w, http.StatusOK, updated)
}

// VerifyRefs handles GET /workflows/refs/verify — mirrors `hyve workflow
// verify`.
func (h *WorkflowsHandlers) VerifyRefs(w http.ResponseWriter, r *http.Request) {
	results, failed, err := workflowref.Verify(h.RepoPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
		"failed":  failed,
		"ok":      failed == 0,
	})
}
