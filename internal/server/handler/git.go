package handler

import (
	"net/http"

	internalgit "github.com/cbridges1/hyve/internal/git"
)

// GitHandlers backs the /git routes — scoped to the single repository the
// server is bound to (--path). This is not the CLI's multi-repo management
// surface (add/list/use/remove/reset/branch management/credentials), which
// has no meaning for a server already pointed at one repo.
type GitHandlers struct {
	*Deps
	Backend func() (*internalgit.SystemBackend, error)
}

func NewGitHandlers(deps *Deps, backend func() (*internalgit.SystemBackend, error)) *GitHandlers {
	return &GitHandlers{Deps: deps, Backend: backend}
}

// Status handles GET /git/status — mirrors `hyve git status` for the bound repo.
func (h *GitHandlers) Status(w http.ResponseWriter, r *http.Request) {
	backend, err := h.Backend()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	report := internalgit.Status(r.Context(), backend)
	writeJSON(w, http.StatusOK, report)
}

type gitSyncRequest struct {
	Message string `json:"message,omitempty"`
}

// Sync handles POST /git/sync — mirrors `hyve git sync`.
func (h *GitHandlers) Sync(w http.ResponseWriter, r *http.Request) {
	var req gitSyncRequest
	if r.ContentLength != 0 {
		if !readJSON(w, r, &req) {
			return
		}
	}

	backend, err := h.Backend()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	report, err := internalgit.Sync(r.Context(), backend, req.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}
