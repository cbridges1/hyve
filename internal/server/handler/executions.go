package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/cbridges1/hyve/internal/execution"
)

// ExecutionsHandlers backs the /executions polling routes (WebSocket
// streaming lives in ws.go).
type ExecutionsHandlers struct {
	*Deps
}

func NewExecutionsHandlers(deps *Deps) *ExecutionsHandlers { return &ExecutionsHandlers{deps} }

// executionSummary is the JSON shape for one execution — Execution's
// unexported fields (lines/subs/etc.) are never serialized directly.
type executionSummary struct {
	ID        string           `json:"id"`
	Kind      execution.Kind   `json:"kind"`
	Status    execution.Status `json:"status"`
	StartedAt string           `json:"startedAt"`
	EndedAt   string           `json:"endedAt,omitempty"`
	Error     string           `json:"error,omitempty"`
}

func summarize(e *execution.Execution) executionSummary {
	s := executionSummary{
		ID:        e.ID,
		Kind:      e.Kind,
		Status:    e.Status,
		StartedAt: e.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		Error:     e.Error,
	}
	if e.EndedAt != nil {
		s.EndedAt = e.EndedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return s
}

// List handles GET /executions — recent executions, most recent first.
func (h *ExecutionsHandlers) List(w http.ResponseWriter, r *http.Request) {
	execs := h.Registry.List()
	out := make([]executionSummary, 0, len(execs))
	for _, e := range execs {
		out = append(out, summarize(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /executions/:id — status + metadata.
func (h *ExecutionsHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, ok := h.Registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("execution %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, summarize(e))
}

// Logs handles GET /executions/:id/logs — paginated log lines. Pass
// ?since=<seq> to fetch only lines after the given sequence number (for
// incremental polling); ?limit=<n> caps the number of lines returned.
func (h *ExecutionsHandlers) Logs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, ok := h.Registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("execution %q not found", id))
		return
	}

	since := 0
	if s := r.URL.Query().Get("since"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			since = v
		}
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}

	all := e.Lines()
	var out []execution.LogLine
	for _, line := range all {
		if line.Seq > since {
			out = append(out, line)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"lines": out})
}
