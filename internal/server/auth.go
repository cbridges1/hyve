package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// ForwardAuthOptions configures ForwardAuthMiddleware.
type ForwardAuthOptions struct {
	ValidateURL string
	Timeout     time.Duration
}

// ForwardAuthMiddleware delegates the entire validity decision to an
// external HTTP endpoint: it relays the incoming Authorization header
// as-is to opts.ValidateURL and interprets the response. No JWT library, no
// key material, no claim parsing — just relay the header and check the
// status code.
//
//   - 2xx                              → valid, pass through to the handler
//   - anything else (401, 403, ...)     → reject with 401
//   - network error or timeout          → fail closed, reject with 401
//
// Hyve never inspects, decodes, or has any opinion about what the credential
// actually is.
func ForwardAuthMiddleware(opts ForwardAuthOptions) func(http.Handler) http.Handler {
	client := &http.Client{Timeout: opts.Timeout}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, opts.ValidateURL, nil)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "auth validator misconfigured")
				return
			}
			req.Header.Set("Authorization", authHeader)

			resp, err := client.Do(req)
			if err != nil {
				// Fail closed: an unreachable or slow validator is treated as
				// invalid, never as "let it through".
				writeError(w, http.StatusUnauthorized, "auth validator unreachable")
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// noAuthMiddleware is used when auth.mode is none — a pure passthrough.
func noAuthMiddleware(next http.Handler) http.Handler { return next }

// writeError writes a JSON {"error": message} body with the given status.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
