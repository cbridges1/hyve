package server

import "net/http"

// corsMiddleware allows any browser-based frontend (a different origin —
// e.g. a Vite dev server on :5173 talking to hyve-server on :8080) to call
// this API. hyve-server is deliberately consumer-agnostic (see "Relationship
// to other tools" in the design doc) — it has no fixed set of allowed
// origins to enumerate, so this reflects the request's Origin verbatim
// rather than a static allowlist. This is safe here because the API uses
// Authorization-header bearer tokens, never cookies — reflecting Origin only
// becomes a real risk when combined with credentialed (cookie-based)
// requests, which this server never issues or accepts.
//
// Applied outermost, before the forward-auth middleware: a CORS preflight
// (OPTIONS) request never carries the Authorization header the real request
// will, so it must never be gated by auth — the browser wouldn't get far
// enough to send the real request at all otherwise.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		reqHeaders := r.Header.Get("Access-Control-Request-Headers")
		if reqHeaders == "" {
			reqHeaders = "Content-Type, Authorization"
		}
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
