package handler

import (
	"fmt"
	"net/http"
	"os"

	"github.com/cbridges1/hyve/internal/template"
)

// TemplatesHandlers backs the /templates routes.
type TemplatesHandlers struct {
	*Deps
}

func NewTemplatesHandlers(deps *Deps) *TemplatesHandlers { return &TemplatesHandlers{deps} }

func (h *TemplatesHandlers) manager() *template.Manager {
	return template.NewManager(h.RepoPath)
}

// List handles GET /templates.
func (h *TemplatesHandlers) List(w http.ResponseWriter, r *http.Request) {
	templates, err := h.manager().ListTemplates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if templates == nil {
		templates = []*template.Template{}
	}
	writeJSON(w, http.StatusOK, templates)
}

// Get handles GET /templates/:name, with YAML content negotiation
// round-tripping the exact on-disk bytes.
func (h *TemplatesHandlers) Get(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tmpl, err := h.manager().GetTemplate(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if wantsYAML(r) {
		data, err := os.ReadFile(h.manager().GetTemplatePath(name))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeResource(w, r, http.StatusOK, tmpl, data)
		return
	}
	writeJSON(w, http.StatusOK, tmpl)
}

// Create handles POST /templates — body is Template YAML or JSON.
func (h *TemplatesHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var tmpl template.Template
	if _, ok := decodeResource(w, r, &tmpl); !ok {
		return
	}
	if err := h.manager().CreateTemplate(&tmpl); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, fmt.Sprintf("Create template %s", tmpl.Metadata.Name))
	writeResource(w, r, http.StatusCreated, tmpl, nil)
}

// Put handles PUT /templates/:name — full replace of the template YAML.
// internal/template.Manager has no dedicated update method, so this deletes
// and recreates the file — the same net effect, since CreateTemplate and
// DeleteTemplate both operate by metadata.name.
func (h *TemplatesHandlers) Put(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var tmpl template.Template
	if _, ok := decodeResource(w, r, &tmpl); !ok {
		return
	}
	tmpl.Metadata.Name = name

	mgr := h.manager()
	if _, err := mgr.GetTemplate(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := mgr.DeleteTemplate(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := mgr.CreateTemplate(&tmpl); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "Update template "+name)
	writeResource(w, r, http.StatusOK, tmpl, nil)
}

// Delete handles DELETE /templates/:name.
func (h *TemplatesHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.manager().DeleteTemplate(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "Delete template "+name)
	w.WriteHeader(http.StatusNoContent)
}

// Validate handles POST /templates/:name/validate — mirrors `hyve template
// validate`, checking driver is set and referenced local workflows exist.
func (h *TemplatesHandlers) Validate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tmpl, err := h.manager().GetTemplate(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	errs, warnings := template.Validate(h.RepoPath, tmpl)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"errors":   errs,
		"warnings": warnings,
		"valid":    len(errs) == 0,
	})
}
