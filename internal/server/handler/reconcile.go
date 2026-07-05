package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cbridges1/hyve/internal/execution"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/types"
)

// ReconcileHandlers backs the /reconcile routes.
type ReconcileHandlers struct {
	*Deps
}

func NewReconcileHandlers(deps *Deps) *ReconcileHandlers { return &ReconcileHandlers{deps} }

type reconcileRequest struct {
	DryRun bool `json:"dryRun,omitempty"`
}

// All handles POST /reconcile — full reconcile, mirrors `hyve reconcile
// [--dry-run]`. Runs asynchronously, tracked by the execution registry.
func (h *ReconcileHandlers) All(w http.ResponseWriter, r *http.Request) {
	var req reconcileRequest
	if r.ContentLength != 0 {
		if !readJSON(w, r, &req) {
			return
		}
	}

	exec := h.Registry.New(execution.KindReconcile)
	go func() {
		exec.Finish(runReconcileAll(h.Deps, exec, req.DryRun))
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"executionId": exec.ID})
}

// Cluster handles POST /reconcile/clusters/:name — reconciles a single
// cluster. internal/reconcile.Reconciler only exposes ReconcileAll (no
// public single-cluster method), so this filters the loaded definitions down
// to the one requested before calling it — legitimate reuse of the same
// entry point, not new reconcile logic.
func (h *ReconcileHandlers) Cluster(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req reconcileRequest
	if r.ContentLength != 0 {
		if !readJSON(w, r, &req) {
			return
		}
	}

	defs, err := h.StateMgr.LoadClusterDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	found := false
	for _, d := range defs {
		if d.Metadata.Name == name {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("cluster %q not found", name))
		return
	}

	exec := h.Registry.New(execution.KindReconcile)
	go func() {
		defs, err := h.StateMgr.LoadClusterDefinitions()
		if err != nil {
			exec.Finish(err)
			return
		}
		var filtered []types.ClusterDefinition
		for _, d := range defs {
			if d.Metadata.Name == name {
				filtered = append(filtered, d)
			}
		}
		rec := reconcile.NewReconciler(h.StateMgr)
		rec.Logger = exec
		exec.Finish(rec.ReconcileAll(context.Background(), filtered, req.DryRun))
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"executionId": exec.ID})
}
