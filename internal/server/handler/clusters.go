package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/execution"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
)

// ClustersHandlers backs the /clusters routes.
type ClustersHandlers struct {
	*Deps
}

func NewClustersHandlers(deps *Deps) *ClustersHandlers { return &ClustersHandlers{deps} }

func (h *ClustersHandlers) clustersDir() string {
	return filepath.Join(h.RepoPath, "clusters")
}

func (h *ClustersHandlers) clusterPath(name string) string {
	return filepath.Join(h.clustersDir(), name+".yaml")
}

// List handles GET /clusters — mirrors `hyve cluster list`'s filtering
// (Kind == "Cluster" and not marked for deletion).
func (h *ClustersHandlers) List(w http.ResponseWriter, r *http.Request) {
	defs, err := h.StateMgr.LoadClusterDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]types.ClusterDefinition, 0, len(defs))
	for _, c := range defs {
		if c.Kind == "Cluster" && !c.Spec.Delete {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /clusters/:name — reads the cluster's YAML file directly
// (same read-only pattern as `hyve cluster show`), supporting YAML content
// negotiation with byte-for-byte round-tripping.
func (h *ClustersHandlers) Get(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data, err := os.ReadFile(h.clusterPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("cluster %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var def types.ClusterDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse cluster definition: "+err.Error())
		return
	}
	writeResource(w, r, http.StatusOK, def, data)
}

type createClusterRequest struct {
	Name     string            `json:"name" yaml:"name"`
	Template string            `json:"template" yaml:"template"`
	Region   string            `json:"region,omitempty" yaml:"region,omitempty"`
	Params   map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
}

type createClusterResponse struct {
	types.ClusterDefinition `yaml:",inline"`
	ExecutionID             string `json:"executionId,omitempty" yaml:"executionId,omitempty"`
	// CommitWarning is set when the cluster was created and saved locally but
	// the git commit/push failed — non-fatal, matching cmd/shared.CommitStateChanges.
	CommitWarning string `json:"commitWarning,omitempty" yaml:"commitWarning,omitempty"`
}

// Create handles POST /clusters — mirrors `hyve cluster create <name>
// --template <template>`: resolves the template, generates the cluster
// definition, saves + commits it, then triggers a reconcile in the
// background (tracked by the execution registry, same as every other
// long-running operation) rather than blocking the request on the full
// provisioning cycle the CLI command waits for synchronously.
func (h *ClustersHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req createClusterRequest
	if _, ok := decodeResource(w, r, &req); !ok {
		return
	}
	if req.Name == "" || req.Template == "" {
		writeError(w, http.StatusBadRequest, "name and template are required")
		return
	}

	tmplMgr := template.NewManager(h.RepoPath)
	tmpl, clusterDef, err := tmplMgr.ExecuteTemplate(r.Context(), req.Template, req.Name, req.Region, req.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to create cluster from template: "+err.Error())
		return
	}

	if tmpl.Spec.Schedule != "" {
		next, err := template.CronNextOccurrence(tmpl.Spec.Schedule, time.Now())
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to evaluate schedule %q: %v", tmpl.Spec.Schedule, err))
			return
		}
		clusterDef.Spec.ExpiresAt = next.Format(time.RFC3339)
	}

	if err := os.MkdirAll(h.clustersDir(), 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create clusters directory: "+err.Error())
		return
	}
	if err := h.StateMgr.SaveClusterDefinition(clusterDef); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save cluster definition: "+err.Error())
		return
	}
	warning := softCommit(r.Context(), h.Deps, fmt.Sprintf("Create cluster %s from template %s", req.Name, req.Template))

	exec := h.Registry.New(execution.KindReconcile)
	go func() {
		defer exec.Finish(runReconcileAll(h.Deps, exec, false))
	}()

	writeResource(w, r, http.StatusCreated, createClusterResponse{ClusterDefinition: *clusterDef, ExecutionID: exec.ID, CommitWarning: warning}, nil)
}

// Put handles PUT /clusters/:name — full replace of the cluster YAML.
func (h *ClustersHandlers) Put(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var def types.ClusterDefinition
	if _, ok := decodeResource(w, r, &def); !ok {
		return
	}
	def.Metadata.Name = name

	if err := os.MkdirAll(h.clustersDir(), 0755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.StateMgr.SaveClusterDefinition(&def); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "Update cluster "+name)
	writeResource(w, r, http.StatusOK, def, nil)
}

type patchClusterRequest struct {
	Pause     *bool             `json:"pause,omitempty"`
	Delete    *bool             `json:"delete,omitempty"`
	Params    map[string]string `json:"params,omitempty"`
	ExpiresAt *string           `json:"expiresAt,omitempty"`
}

// Patch handles PATCH /clusters/:name — partial update of pause/delete/params/expiresAt.
func (h *ClustersHandlers) Patch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := h.clusterPath(name)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("cluster %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var def types.ClusterDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var patch patchClusterRequest
	if !readJSON(w, r, &patch) {
		return
	}
	if patch.Pause != nil {
		def.Spec.Pause = *patch.Pause
	}
	if patch.Delete != nil {
		def.Spec.Delete = *patch.Delete
	}
	if patch.ExpiresAt != nil {
		def.Spec.ExpiresAt = *patch.ExpiresAt
	}
	for k, v := range patch.Params {
		if def.Spec.Params == nil {
			def.Spec.Params = map[string]string{}
		}
		def.Spec.Params[k] = v
	}

	if err := h.StateMgr.SaveClusterDefinition(&def); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "Update cluster "+name)
	writeResource(w, r, http.StatusOK, def, nil)
}

// Delete handles DELETE /clusters/:name — sets spec.delete: true and
// triggers a reconcile, mirroring `hyve cluster delete <name>`.
func (h *ClustersHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := h.clusterPath(name)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("cluster %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var def types.ClusterDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	def.Spec.Delete = true
	if err := h.StateMgr.SaveClusterDefinition(&def); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, fmt.Sprintf("Mark cluster %s for deletion", name))

	exec := h.Registry.New(execution.KindReconcile)
	go func() {
		defer exec.Finish(runReconcileAll(h.Deps, exec, false))
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"executionId": exec.ID})
}

// Resources handles GET /clusters/:name/resources — read-only, no live
// cluster calls, mirrors `hyve cluster resources <name>`.
func (h *ClustersHandlers) Resources(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data, err := os.ReadFile(h.clusterPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("cluster %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var def types.ClusterDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"resources":        def.Spec.Resources,
		"appliedResources": def.Spec.AppliedResources,
	})
}

// runReconcileAll loads all cluster definitions and runs a full reconcile,
// streaming progress into exec. Shared by every handler that triggers a
// reconcile as a side effect (cluster create/delete, POST /reconcile). Runs
// with a fresh background context rather than the triggering request's
// context, since the reconcile continues after the HTTP response is sent.
func runReconcileAll(deps *Deps, exec *execution.Execution, dryRun bool) error {
	defs, err := deps.StateMgr.LoadClusterDefinitions()
	if err != nil {
		return err
	}
	rec := reconcile.NewReconciler(deps.StateMgr)
	rec.Logger = exec
	return rec.ReconcileAll(context.Background(), defs, dryRun)
}
