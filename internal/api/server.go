// Package api implements hyve's HTTP API + auth layer (see
// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6) — a thin, authorized
// front door onto the ClusterDefinition/HyveAccessBinding CRDs the
// controller already reconciles, not a second implementation of hyve's
// logic. Cluster mode never requires this API: plain kubectl against the
// CRDs always works. Local mode is entirely unaffected.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Server holds the API's shared dependencies, constructed once at startup
// and referenced by every handler.
type Server struct {
	Client     client.Client
	Namespace  string // hyve-system by convention — where credentials Secrets/ClusterDefinitions live
	SigningKey []byte

	// PrimaryClusterName, when set, is the name handleKubeconfig matches
	// against ?cluster=<name> to dispatch to PrimaryProvider instead of
	// looking up a ClusterDefinition. Left empty, the primary-cluster path
	// is simply never reachable (every request falls through to
	// ModuleAuthProvider/TunnelProvider) — safe to leave unset in a
	// deployment with no primary-cluster access story configured yet.
	PrimaryClusterName string
	PrimaryProvider    AccessProvider
	ModuleAuthProvider AccessProvider
	TunnelProvider     AccessProvider

	// Proxy backs /proxy/* — see proxy.go. Left nil, /proxy/* 503s rather
	// than panicking.
	Proxy http.Handler
}

// Routes returns the API's full handler: /auth/*, /healthz, /docs, and
// /openapi.yaml are all unauthenticated (login/refresh/logout are
// themselves the auth mechanism — none can require a currently-valid
// access token, refresh's whole point is to work after one's expired; docs
// are a development aid with no sensitive data), everything under /api/
// requires a valid access token (requireAuth) resolved to a role
// (requireRole).
//
// /proxy/* deliberately does NOT require a hyve session: a client using it
// (typically `kubectl --kubeconfig <file minted by GET /api/kubeconfig>`)
// authenticates with a real Kubernetes ServiceAccount token in its
// Authorization header, not a hyve one — the two credentials share the
// same header and can't both be checked there. The forwarded token is what
// the real kube-apiserver's own RBAC authorizes against; this handler is
// pure transport, per HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.6
// ("do not re-implement authorization here").
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /auth/login", s.handleLogin)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("POST /auth/refresh", s.handleRefresh)
	s.registerDocsRoutes(mux)

	apiMux := http.NewServeMux()
	s.registerClusterRoutes(apiMux)
	s.registerKubeconfigRoutes(apiMux)
	s.registerAuthContextRoutes(apiMux)
	s.registerTemplateRoutes(apiMux)
	s.registerWorkflowRoutes(apiMux)
	s.registerSecretsRoutes(apiMux)
	s.registerModuleRoutes(apiMux)
	s.registerWhoamiRoute(apiMux)

	mux.Handle("/api/", http.StripPrefix("/api", s.requireAuth(s.requireRole(apiMux))))
	mux.Handle("/proxy/", http.StripPrefix("/proxy", http.HandlerFunc(s.handleProxy)))
	return mux
}

// handleProxy forwards to s.Proxy (see proxy.go's BuildProxy) — a thin
// indirection so Routes doesn't need a nil-Proxy special case wired
// through every middleware layer.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if s.Proxy == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy not configured")
		return
	}
	s.Proxy.ServeHTTP(w, r)
}

// contextKey namespaces this package's request-context keys so they never
// collide with another package's.
type contextKey string

const (
	contextKeyUsername          contextKey = "hyve-api-username"
	contextKeyRole              contextKey = "hyve-api-role"
	contextKeyServiceAccountRef contextKey = "hyve-api-service-account-ref"
)

// UsernameFromContext returns the authenticated caller's username, set by
// requireAuth.
func UsernameFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyUsername).(string)
	return v, ok
}

// RoleFromContext returns the caller's resolved role, set by requireRole.
func RoleFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyRole).(string)
	return v, ok
}

// ServiceAccountRefFromContext returns the caller's matched binding's
// ServiceAccountRef, set by requireRole — used by PrimaryClusterProvider's
// TokenRequest call (see access.go).
func ServiceAccountRefFromContext(ctx context.Context) (hyvev1alpha1.ServiceAccountRef, bool) {
	v, ok := ctx.Value(contextKeyServiceAccountRef).(hyvev1alpha1.ServiceAccountRef)
	return v, ok
}

// requireAuth verifies the "Authorization: Bearer <token>" header and puts
// the authenticated username on the request context. A missing, malformed,
// or expired token gets 401 — never a default identity.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, prefix) {
			writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		token := strings.TrimPrefix(authHeader, prefix)
		username, err := VerifyToken(s.SigningKey, token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		ctx := context.WithValue(r.Context(), contextKeyUsername, username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRole resolves the authenticated username (set by requireAuth,
// which must run first in the chain) to a role via its HyveAccessBinding
// and puts both the role and the binding's ServiceAccountRef on the
// request context. An authenticated identity with no matching binding gets
// a hard 403 — never a silent default role.
func (s *Server) requireRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, ok := UsernameFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		binding, err := FindBindingBySubject(r.Context(), s.Client, hyvev1alpha1.SubjectTypeLocal, username)
		if err != nil {
			writeError(w, http.StatusForbidden, "no access binding for this identity")
			return
		}
		ctx := context.WithValue(r.Context(), contextKeyRole, binding.Spec.Role)
		ctx = context.WithValue(ctx, contextKeyServiceAccountRef, binding.Spec.ServiceAccountRef)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole 403s the request unless its resolved role (set by
// requireRole) is one of allowed. Call from a handler that needs
// finer-grained authz than "any authenticated+bound caller" — e.g. 6.4's
// admin-only POST/DELETE on /api/clusters.
func RequireRole(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	role, ok := RoleFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "no resolved role")
		return false
	}
	for _, a := range allowed {
		if role == a {
			return true
		}
	}
	writeError(w, http.StatusForbidden, fmt.Sprintf("role %q is not permitted to perform this action", role))
	return false
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: failed to encode response: %v", err)
	}
}

// writeError writes a {"error": message} JSON body with the given status.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
