package api

import "net/http"

// corsMiddleware allows any browser-based frontend (a different origin —
// e.g. a Vite dev server, or a statically-hosted build — talking to this
// API) to call it. This API is deliberately consumer-agnostic — it has no
// fixed set of allowed origins to enumerate, so this reflects the request's
// Origin verbatim rather than a static allowlist. This is safe only because
// the API uses Authorization-header bearer tokens, never cookies (see
// requireAuth) — reflecting Origin becomes a real risk once combined with
// credentialed (cookie-based) requests, which this server must continue to
// never issue or accept. Do not add Access-Control-Allow-Credentials here
// without re-deriving this safety argument from scratch.
//
// Applied outermost in Routes, before requireAuth/requireRole: a CORS
// preflight (OPTIONS) request never carries the Authorization header the
// real request will, so it must never be gated by auth — the browser
// wouldn't get far enough to send the real request at all otherwise.
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
