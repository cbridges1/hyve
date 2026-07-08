// Package handler implements hyve-server's REST+WebSocket route handlers.
// Every handler is a thin wrapper around the same internal/* packages the
// CLI already calls — it parses the request, calls the internal function,
// and writes JSON (or YAML, via the content-negotiation helpers in yaml.go)
// instead of formatted log lines. No new business logic lives here.
package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/cbridges1/hyve/internal/execution"
	"github.com/cbridges1/hyve/internal/state"
)

// Deps are the dependencies every handler group needs: the single bound repo
// path, a state.Manager for that repo, and the shared execution registry
// backing polling/WebSocket streaming.
type Deps struct {
	RepoPath string
	StateMgr *state.Manager
	Registry *execution.Registry
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON {"error": message} response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// readJSON decodes the request body into v, writing a 400 error response and
// returning false on failure.
func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body required")
		return false
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// softCommit commits and pushes, matching cmd/shared.CommitStateChanges'
// soft-failure convention exactly: a failed commit/push never aborts the
// operation that triggered it (the resource is already written to disk and
// usable locally) — it's only logged, with a non-empty warning string
// returned for the caller to surface to the client if useful.
func softCommit(ctx context.Context, deps *Deps, message string) (warning string) {
	if err := deps.StateMgr.CommitAndPush(ctx, message); err != nil {
		log.Printf("Warning: failed to commit and push: %v", err)
		return err.Error()
	}
	return ""
}
