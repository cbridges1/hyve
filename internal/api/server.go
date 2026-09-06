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
	"sync"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Server holds the API's shared dependencies, constructed once at startup
// and referenced by every handler.
type Server struct {
	Client     client.Client
	Namespace  string // hyve-system by convention — where credentials Secrets/ClusterDefinitions live
	SigningKey []byte

	// PrimaryProvider serves any ClusterDefinition whose access.method is
	// AccessMethodPrimary (see kubeconfig_handler.go's switch) — nil is
	// fine for a deployment with no host-cluster access story configured
	// yet, that case just 500s with a clear message rather than being
	// unreachable by construction like the old name-matching shortcut was.
	PrimaryProvider    AccessProvider
	ModuleAuthProvider AccessProvider
	TunnelProvider     AccessProvider

	// ModulesDir is the same baked-in modules root ModuleAuthProvider's own
	// ModulesDir points at (see its doc comment) — used directly by
	// handleAuthContext to resolve a driver module and read its auth
	// operation file for delivery to the client, since that's a read, not
	// an AccessProvider.Kubeconfig-shaped execution.
	ModulesDir string

	// Clientset is a plain client-go clientset (as distinct from Client,
	// the controller-runtime client used for CRDs) — needed for
	// Job/Secret dispatch, which internal/k8sjob's kubernetes.Interface-
	// based API expects. Used by the access-method mint handler
	// (accessmethod_mint.go); nil is fine for a deployment that never
	// calls it (every other handler uses Client instead).
	Clientset kubernetes.Interface

	// RelayBaseURL is this API's own in-cluster address for the mint
	// relay listener (see RelayRoutes) — e.g.
	// "http://hyve-api-internal.hyve-system.svc.cluster.local:8091". Never
	// the same as the public-facing address: the relay listener has no
	// Ingress at all, reachable only from inside the cluster's own pod
	// network. Required for POST /api/access-methods/<name>/mint to work
	// at all; that handler 500s with a clear message if this is unset.
	RelayBaseURL string

	// MintTimeout overrides how long handleAccessMethodMint waits for its
	// dispatched Job to push a result before giving up — see
	// mintTimeout's own doc comment for the production default this falls
	// back to when zero. Exists as a Server field (not just the constant)
	// so tests can shorten it well below 90s instead of actually waiting.
	MintTimeout time.Duration

	// mintPending tracks in-flight access-method mint requests awaiting a
	// push from their dispatched Job — see accessmethod_mint.go.
	mintPending sync.Map

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
	s.registerWorkflowRunRoutes(apiMux)
	s.registerResourceRoutes(apiMux)
	s.registerSecretsRoutes(apiMux)
	s.registerModuleRoutes(apiMux)
	s.registerAccessMethodRoutes(apiMux)
	s.registerAccessMethodMintRoutes(apiMux)
	s.registerWhoamiRoute(apiMux)
	s.registerAccountRoutes(apiMux)
	s.registerEnvironmentRoutes(apiMux)

	mux.Handle("/api/", http.StripPrefix("/api", s.requireAuth(s.requireRole(apiMux))))
	mux.Handle("/proxy/", http.StripPrefix("/proxy", http.HandlerFunc(s.handleProxy)))
	return corsMiddleware(mux)
}

// RelayRoutes returns the internal-only handler an access-method mint
// Job's push callback calls — see accessmethod_mint.go. Deliberately not
// mounted under Routes()/"/api/": this must be served on a separate
// listener with no Ingress at all (see cmd/api/run.go), since it carries
// no hyve session concept whatsoever — its own one-shot per-request bearer
// token (checked inside handleAccessMethodMintRelay itself) is the only
// authorization it has, and it must never be reachable from outside the
// cluster's own pod network.
func (s *Server) RelayRoutes() http.Handler {
	mux := http.NewServeMux()
	s.registerAccessMethodMintRelayRoutes(mux)
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
	contextKeyNamespace         contextKey = "hyve-api-namespace"
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

// NamespaceFromContext returns the tenant namespace the caller's access
// token was issued for (set by requireAuth) — empty string is a valid,
// meaningful value (the control-plane/superadmin namespace), not "unset";
// ok is false only when no token was ever verified at all (shouldn't
// happen for anything behind requireAuth).
func NamespaceFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyNamespace).(string)
	return v, ok
}

// actAsNamespaceHeader lets a superadmin caller view/act within a chosen
// tenant namespace without a separate HyveAccessBinding of their own there
// — a superadmin's session otherwise has no tenant namespace at all (see
// RoleSuperadmin's doc comment), and re-login with --org doesn't work for
// them either (their identity binding only exists in the control-plane
// namespace). Honored ONLY when the caller's already-resolved role is
// RoleSuperadmin — see TenantNamespace below for why this is safe even
// though requireRole itself calls TenantNamespace before role is known.
const actAsNamespaceHeader = "X-Hyve-Act-As-Namespace"

// TenantNamespace resolves the namespace a request's own CRUD should be
// scoped to — the token's own namespace when the request carries one
// (the normal case, threaded by requireAuth from the verified access
// token), falling back to s.Namespace (this install's control-plane
// namespace) only for requests with no verified token in context at all,
// which shouldn't occur for anything mounted behind requireAuth. Handlers
// that manage tenant-scoped objects (ClusterDefinition, Template,
// Workflow, Resource, HyveAccessBinding, credentials Secrets, etc.) call
// this instead of reading s.Namespace directly — see
// HYVE-MULTI-TENANCY-PLAN.md's "Phase 2" section for why: s.Namespace is
// now fixed per-install control-plane bookkeeping only (HyveConfig, the
// primary ClusterDefinition, HyveEnvironment, HyveSession storage), not a
// tenant's own namespace, which varies per login.
//
// A superadmin caller may override this via actAsNamespaceHeader — checked
// only when RoleFromContext already resolves to RoleSuperadmin, which is
// what makes this safe to check unconditionally here rather than gating it
// per call site: requireRole's own internal call to TenantNamespace (to
// look up the caller's own binding, before role is known) runs before
// contextKeyRole is ever set, so RoleFromContext returns ok=false there and
// the header is correctly ignored for that call — a non-superadmin, or a
// not-yet-role-resolved request, can never have this header honored, under
// any circumstance.
func (s *Server) TenantNamespace(r *http.Request) string {
	if role, ok := RoleFromContext(r.Context()); ok && role == hyvev1alpha1.RoleSuperadmin {
		if actAs := r.Header.Get(actAsNamespaceHeader); actAs != "" {
			return actAs
		}
	}
	if ns, ok := NamespaceFromContext(r.Context()); ok && ns != "" {
		return ns
	}
	// Empty (a superadmin's own login) means "the control-plane
	// namespace" — the same value s.Namespace already holds, not a
	// distinct third namespace.
	return s.Namespace
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
		username, namespace, err := VerifyToken(s.SigningKey, token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		ctx := context.WithValue(r.Context(), contextKeyUsername, username)
		ctx = context.WithValue(ctx, contextKeyNamespace, namespace)
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
		binding, err := FindBindingBySubject(r.Context(), s.Client, s.TenantNamespace(r), hyvev1alpha1.SubjectTypeLocal, username)
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
		// A superadmin can do anything an admin can, everywhere an admin
		// call site checks RoleAdmin — this is what makes the "act as"
		// environment switcher (TenantNamespace's X-Hyve-Act-As-Namespace
		// handling) actually usable: without it, every one of the ~20
		// RoleAdmin-only mutation endpoints (clusters/templates/workflows/
		// resources/accessmethods/secrets/workflow-runs) rejected a
		// superadmin outright before namespace resolution ever mattered,
		// confirmed live. Centralized here rather than listing
		// RoleSuperadmin at every call site individually, so a future
		// RoleAdmin-gated endpoint gets this for free instead of silently
		// missing it. The reverse never holds — an admin passing
		// RequireRole(..., RoleSuperadmin) alone is not granted anything;
		// superadmin-exclusive endpoints (POST/GET /environments, the
		// host-cluster kubeconfig path) never list RoleAdmin at all, so
		// this rule never fires for them.
		if a == hyvev1alpha1.RoleAdmin && role == hyvev1alpha1.RoleSuperadmin {
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
