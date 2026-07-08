package handler

import (
	"net/http"

	"github.com/cbridges1/hyve/internal/state"
)

// ConfigHandlers backs the /config routes.
type ConfigHandlers struct {
	*Deps
}

func NewConfigHandlers(deps *Deps) *ConfigHandlers { return &ConfigHandlers{deps} }

// Get handles GET /config — returns the parsed hyve.yaml.
func (h *ConfigHandlers) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.StateMgr.LoadRepoConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeResource(w, r, http.StatusOK, cfg, nil)
}

// patchConfigRequest mirrors state.RepoConfig with every field optional, so a
// caller can update just reconcile.mode without needing to resend the whole
// document.
type patchConfigRequest struct {
	Reconcile *struct {
		Mode                 *string `json:"mode,omitempty"`
		StrictDelete         *bool   `json:"strictDelete,omitempty"`
		StrictResourceDelete *bool   `json:"strictResourceDelete,omitempty"`
	} `json:"reconcile,omitempty"`
	Server *struct {
		Port        *int    `json:"port,omitempty"`
		FrontendUrl *string `json:"frontendUrl,omitempty"`
		Auth        *struct {
			Mode    *string `json:"mode,omitempty"`
			Forward *struct {
				ValidateURL *string `json:"validateUrl,omitempty"`
				Timeout     *string `json:"timeout,omitempty"`
			} `json:"forward,omitempty"`
		} `json:"auth,omitempty"`
	} `json:"server,omitempty"`
}

// Patch handles PATCH /config — writes through to hyve.yaml and commits.
// Does not restart the server; a change to server.* only takes effect on the
// next `hyve serve` invocation.
func (h *ConfigHandlers) Patch(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.StateMgr.LoadRepoConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var patch patchConfigRequest
	if !readJSON(w, r, &patch) {
		return
	}

	if patch.Reconcile != nil {
		if patch.Reconcile.Mode != nil {
			cfg.Reconcile.Mode = state.ReconcileMode(*patch.Reconcile.Mode)
		}
		if patch.Reconcile.StrictDelete != nil {
			cfg.Reconcile.StrictDelete = *patch.Reconcile.StrictDelete
		}
		if patch.Reconcile.StrictResourceDelete != nil {
			cfg.Reconcile.StrictResourceDelete = *patch.Reconcile.StrictResourceDelete
		}
	}
	if patch.Server != nil {
		if patch.Server.Port != nil {
			cfg.Server.Port = *patch.Server.Port
		}
		if patch.Server.FrontendUrl != nil {
			cfg.Server.FrontendUrl = *patch.Server.FrontendUrl
		}
		if patch.Server.Auth != nil {
			if patch.Server.Auth.Mode != nil {
				cfg.Server.Auth.Mode = state.ServerAuthMode(*patch.Server.Auth.Mode)
			}
			if patch.Server.Auth.Forward != nil {
				if patch.Server.Auth.Forward.ValidateURL != nil {
					cfg.Server.Auth.Forward.ValidateURL = *patch.Server.Auth.Forward.ValidateURL
				}
				if patch.Server.Auth.Forward.Timeout != nil {
					cfg.Server.Auth.Forward.Timeout = *patch.Server.Auth.Forward.Timeout
				}
			}
		}
	}

	if err := h.StateMgr.SaveRepoConfig(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	softCommit(r.Context(), h.Deps, "chore: update hyve.yaml")
	writeResource(w, r, http.StatusOK, cfg, nil)
}
