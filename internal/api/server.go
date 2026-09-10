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
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Server holds the API's shared dependencies, constructed once at startup
// and referenced by every handler.
type Server struct {
	Client     client.Client
	Namespace  string // hyve-system by convention — where credentials Secrets/ClusterDefinitions live
	SigningKey []byte

	ModuleAuthProvider AccessProvider
	TunnelProvider     AccessProvider

	// AgentProvider serves GET /api/kubeconfig for any ClusterDefinition
	// with spec.access.agent.proxy: true — see access.go's AgentProvider
	// and handleKubeconfig's own dispatch, which checks this ahead of
	// spec.access.method entirely (Agent/Proxy is orthogonal to Method).
	// nil is fine for a deployment that never configures agent proxying;
	// that case 500s with a clear message, same stance as the other
	// providers' own nil handling.
	AgentProvider AccessProvider

	// ModulesDir is the same baked-in modules root ModuleAuthProvider's own
	// ModulesDir points at (see its doc comment) — used directly by
	// handleAuthContext to resolve a driver module and read its auth
	// operation file for delivery to the client, since that's a read, not
	// an AccessProvider.Kubeconfig-shaped execution.
	ModulesDir string

	// Clientset is a plain client-go clientset (as distinct from Client,
	// the controller-runtime client used for CRDs) — needed for agent
	// bootstrap token validation (agent_bootstrap.go), raw Events()
	// reads/writes (clusters.go, emitClusterEvent). nil is fine for a
	// deployment that never calls a handler needing it (every other
	// handler uses Client instead).
	Clientset kubernetes.Interface

	// Proxy backs /proxy/* — see proxy.go. Left nil, /proxy/* 503s rather
	// than panicking.
	Proxy http.Handler

	// AgentCA backs POST /agent/bootstrap (agent_bootstrap.go) — see
	// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Agent identity /
	// authentication". Left nil, that endpoint 500s with a clear message
	// rather than panicking, same stance Proxy above takes for its own
	// not-yet-configured case.
	AgentCA *agentpki.CA

	// AgentRegistry backs ServeAgentTunnel (agent_listener.go) — the
	// in-memory (namespace, clusterName)-keyed map of currently live agent
	// connections. Left nil, ServeAgentTunnel refuses to start (returns an
	// error) rather than accepting connections it can't track, same
	// fail-fast stance as the AgentCA check right above it.
	AgentRegistry *AgentRegistry
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
	s.registerAgentBootstrapRoutes(mux)
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
	s.registerWhoamiRoute(apiMux)
	s.registerAccountRoutes(apiMux)
	s.registerEnvironmentRoutes(apiMux)
	s.registerAgentProxyRoutes(apiMux)

	mux.Handle("/api/", http.StripPrefix("/api", s.requireAuth(s.requireRole(apiMux))))
	mux.Handle("/proxy/", http.StripPrefix("/proxy", http.HandlerFunc(s.handleProxy)))
	return corsMiddleware(mux)
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
	contextKeyUsername  contextKey = "hyve-api-username"
	contextKeyNamespace contextKey = "hyve-api-namespace"
	contextKeyRole      contextKey = "hyve-api-role"
	contextKeyToken     contextKey = "hyve-api-token"
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

// tokenFromContext returns the caller's own raw, already-verified hyve
// session token — set by requireAuth. Unexported (unlike the other
// FromContext accessors above, all used by AccessProvider implementations
// across their own concerns): AgentProvider (access.go) is the only
// consumer, embedding this same token into the kubeconfig it mints so
// that when kubectl later presents it back to /api/agent-proxy/..., that
// route's own requireAuth re-verifies it exactly like any other /api/*
// call — see agent_proxy.go's own doc comment for the full round trip.
func tokenFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyToken).(string)
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
		ctx = context.WithValue(ctx, contextKeyToken, token)
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
		// resources/secrets/workflow-runs) rejected a
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

// emitClusterEvent creates a plain corev1.Event referencing the named
// ClusterDefinition — shared by agent_proxy.go's audit trail and
// agent_listener.go's connect/disconnect events, factored out once a
// second call site needed the identical boilerplate. Not a full
// record.EventRecorder (this package has no broadcaster/scheme machinery
// standing one up would need, and both call sites already have a
// clientset in hand) — a direct Events().Create call, matching
// GET /clusters/{name}/events' own raw-client-go read path (see
// clusters.go's handleGetClusterEvents) rather than the cached
// controller-runtime client used elsewhere in this package. UID is
// deliberately never set on InvolvedObject: that read path matches purely
// on involvedObject.name (a field selector, not UID-aware), and neither
// call site otherwise has the object's UID in hand without an extra Get —
// best-effort audit/status events don't need object-identity precision
// that fine. Best-effort throughout: a failure here is logged, never
// returned to the caller, since neither call site's own real work should
// ever be blocked by an event-emission hiccup. No-ops (logs nothing, not
// even a warning) when clientset is nil — an install that never
// configured one shouldn't get log spam for a feature it isn't using.
func emitClusterEvent(ctx context.Context, clientset kubernetes.Interface, namespace, name, reason, message string) {
	if clientset == nil {
		return
	}
	now := time.Now()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "hyve-" + strings.ToLower(reason) + "-", Namespace: namespace},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: hyvev1alpha1.GroupVersion.String(),
			Kind:       "ClusterDefinition",
			Namespace:  namespace,
			Name:       name,
		},
		Reason:         reason,
		Message:        message,
		Type:           corev1.EventTypeNormal,
		Source:         corev1.EventSource{Component: "hyve-api"},
		FirstTimestamp: metav1.NewTime(now),
		LastTimestamp:  metav1.NewTime(now),
		Count:          1,
	}
	if _, err := clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		log.Printf("api: failed to emit %s event for %s/%s: %v", reason, namespace, name, err)
	}
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
