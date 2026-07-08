package handler

import (
	"context"
	"net/http"

	"github.com/cbridges1/hyve/internal/execution"
	mod "github.com/cbridges1/hyve/internal/module"
)

// ModulesHandlers backs the /modules and /lock routes.
type ModulesHandlers struct {
	*Deps
}

func NewModulesHandlers(deps *Deps) *ModulesHandlers { return &ModulesHandlers{deps} }

// List handles GET /modules — lists locked modules.
func (h *ModulesHandlers) List(w http.ResponseWriter, r *http.Request) {
	lf, err := mod.LoadLockFile(h.RepoPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lf.Modules)
}

// Lock handles GET /lock — the full parsed hyve.lock contents.
func (h *ModulesHandlers) Lock(w http.ResponseWriter, r *http.Request) {
	lf, err := mod.LoadLockFile(h.RepoPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lf)
}

// Info handles GET /modules/{source...} — mirrors `hyve module info <source>
// <version>`. Source can contain slashes (path-kind wildcard route); version
// is passed as a query param since the CLI command requires both and the
// doc's route only names source in the path.
func (h *ModulesHandlers) Info(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	version := r.URL.Query().Get("version")
	if version == "" {
		version = "latest"
	}
	manifest, resolvedDir, err := mod.ModuleInfo(h.RepoPath, source, version)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"manifest": manifest,
		"path":     resolvedDir,
	})
}

type moduleSourceVersionRequest struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
}

// Add handles POST /modules — body: { source, version } — locks a new
// module, mirroring `hyve module add <source>[@<version>]`.
func (h *ModulesHandlers) Add(w http.ResponseWriter, r *http.Request) {
	var req moduleSourceVersionRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}
	if req.Version == "" {
		req.Version = "latest"
	}

	lockVersion, entry, alreadyLocked, err := mod.AddModule(h.RepoPath, req.Source, req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var warning string
	if !alreadyLocked {
		warning = softCommit(r.Context(), h.Deps, "chore: add module "+req.Source+"@"+lockVersion)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"source":        req.Source,
		"version":       lockVersion,
		"module":        entry,
		"alreadyLocked": alreadyLocked,
		"commitWarning": warning,
	})
}

// Update handles POST /modules/update — body: { source, version } —
// re-resolves an already-locked source@version, refreshing its sha256.
// mirrors `hyve module update <source> <version>`.
func (h *ModulesHandlers) Update(w http.ResponseWriter, r *http.Request) {
	var req moduleSourceVersionRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source == "" || req.Version == "" {
		writeError(w, http.StatusBadRequest, "source and version are required")
		return
	}

	entry, err := mod.UpdateModule(h.RepoPath, req.Source, req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "chore: update module "+req.Source+"@"+req.Version)
	writeJSON(w, http.StatusOK, entry)
}

// Remove handles POST /modules/remove — body: { source, version }.
func (h *ModulesHandlers) Remove(w http.ResponseWriter, r *http.Request) {
	var req moduleSourceVersionRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source == "" || req.Version == "" {
		writeError(w, http.StatusBadRequest, "source and version are required")
		return
	}

	removed, err := mod.RemoveModule(h.RepoPath, req.Source, req.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "not locked: "+req.Source+"@"+req.Version)
		return
	}
	softCommit(r.Context(), h.Deps, "chore: remove module "+req.Source+"@"+req.Version)
	w.WriteHeader(http.StatusNoContent)
}

// Validate handles POST /modules/validate — body: { source, version }.
func (h *ModulesHandlers) Validate(w http.ResponseWriter, r *http.Request) {
	var req moduleSourceVersionRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source == "" || req.Version == "" {
		writeError(w, http.StatusBadRequest, "source and version are required")
		return
	}

	errs, err := mod.ValidateModule(h.RepoPath, req.Source, req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"errors": errs,
		"valid":  len(errs) == 0,
	})
}

// Install handles POST /modules/install — locks every module referenced by
// templates/clusters, mirroring `hyve module install`. Runs asynchronously.
func (h *ModulesHandlers) Install(w http.ResponseWriter, r *http.Request) {
	exec := h.Registry.New(execution.KindModuleInstall)
	go func() {
		refs, err := mod.GatherModuleRefs(h.StateMgr, h.RepoPath)
		if err != nil {
			exec.Finish(err)
			return
		}
		locked, alreadyLocked, resolveErrors, err := mod.InstallModules(h.RepoPath, refs)
		for _, ref := range alreadyLocked {
			exec.Write([]byte("already locked: " + ref.Source + "@" + ref.Version + "\n"))
		}
		for _, e := range resolveErrors {
			exec.Write([]byte("warning: failed to resolve " + e + "\n"))
		}
		for _, l := range locked {
			exec.Write([]byte("locked " + l.Source + "@" + l.Version + " (sha256: " + l.Entry.SHA256 + ")\n"))
		}
		if err != nil {
			exec.Finish(err)
			return
		}
		if len(locked) > 0 {
			softCommit(context.Background(), h.Deps, "chore: update hyve.lock")
		}
		exec.Finish(nil)
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"executionId": exec.ID})
}

// Init handles POST /modules/init — body: { name } — scaffolds a new module
// skeleton, mirroring `hyve module init <name>`.
func (h *ModulesHandlers) Init(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		AuthOnly bool   `json:"authOnly,omitempty"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	dir, err := mod.InitModuleSkeleton(h.RepoPath, req.Name, req.AuthOnly)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": dir})
}
