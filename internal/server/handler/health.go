package handler

import "net/http"

// Version is the hyve-server API version reported by GET /health. Set at
// build time via -ldflags if a real version string is wired up later.
var Version = "0.1.0"

// HealthHandlers backs GET /health.
type HealthHandlers struct{}

func NewHealthHandlers() *HealthHandlers { return &HealthHandlers{} }

// Health handles GET /health — always unauthenticated, never behind
// forward-auth middleware.
func (h *HealthHandlers) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
}

// AuthCheck handles GET /auth/check. It is registered behind the same
// forward-auth middleware as every other protected route — reaching this
// handler at all already means the middleware accepted the request (or
// auth.mode is none), so it has nothing left to do but return 200.
func (h *HealthHandlers) AuthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
