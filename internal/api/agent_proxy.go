// agent_proxy.go is milestone 5's payoff: turns spec.access.agent.proxy:
// true into something a caller can actually run kubectl against. The
// mechanism, end to end: a caller's own hyve session token (embedded by
// AgentProvider.Kubeconfig into the kubeconfig GET /api/kubeconfig mints)
// authenticates the incoming request exactly like any other /api/* call
// (requireAuth+requireRole); handleAgentProxy then resolves the target
// agent's live connection from AgentRegistry and builds a fresh
// httputil.ReverseProxy per request whose Transport dials out through a
// new SSH channel on that connection instead of a normal TCP dial (see
// dialAgentChannel) — the agent's own internal/agent/proxy.go relays raw
// bytes from there to its local apiserver, never touching HTTP/TLS at
// all. This process is therefore the one place a real Kubernetes bearer
// token is needed: the connected agent's own current ServiceAccount
// token (relayed over the tunnel via heartbeats — see AgentConnection.
// ServiceAccountToken), which only has the impersonate verb (see
// internal/reconcile/agent.go's hyve-agent-impersonator ClusterRole) — so
// this handler also sets Impersonate-User/Impersonate-Group from the
// caller's own resolved hyve role, letting the target cluster's own RBAC
// (the two ClusterRoleBindings that same reconcile step provisions when
// Proxy is true) make the real authorization decision, never this
// process. See docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Proxy
// authorization model".
package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"golang.org/x/crypto/ssh"
)

// registerAgentProxyRoutes wires /agent-proxy/{name}/{rest...} — mounted
// under /api/ (behind requireAuth+requireRole, via Server.Routes'
// existing apiMux wrapping) rather than as its own top-level mount the
// way /proxy/ is: unlike /proxy/ (whose caller presents a real Kubernetes
// token, not a hyve one — see proxy.go's own doc comment), this endpoint
// is authenticated exactly like every other /api/* route, so it belongs
// with them. No method restriction (unlike most routes in this file):
// kubectl issues GET/POST/PUT/PATCH/DELETE and upgrade requests against
// arbitrary Kubernetes API subpaths, all through this one handler.
func (s *Server) registerAgentProxyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/agent-proxy/{name}/{rest...}", s.requireOrganizationNotMigrating(s.handleAgentProxy))
}

func (s *Server) handleAgentProxy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ns := s.TenantNamespace(r)

	// Get-existence-check, then registry lookup — in that exact order,
	// per the resolved multi-tenancy design: ns always comes from
	// TenantNamespace(r) (the caller's own verified session/act-as
	// namespace), never a URL path segment — the same pattern
	// handleGetCluster already uses, which is what makes a namespace-B
	// caller's request against a namespace-A cluster 404 here rather
	// than ever reaching the registry lookup at all. Milestone 9
	// (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs):
	// resolves through resourceClient, not s.Client directly — an
	// organization on a Milestone 6 reconciling cluster has its
	// ClusterDefinition there too, not on this control plane's own home
	// cluster. name itself needs no separate environment disambiguation
	// here or in AgentConnectionKey: it's always the real Kubernetes
	// object name, already environment-addressed end to end (see
	// AgentProvider.Kubeconfig, which mints this same /agent-proxy/<name>
	// URL from cd.Name, not a short display name) — "dev-web" and
	// "staging-web" are simply different name values throughout this
	// entire path, never colliding.
	c, err := s.resourceClient(r.Context(), ns)
	if err != nil {
		log.Printf("api: agent-proxy: failed to resolve resource client for %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	var cd hyvev1alpha1.ClusterDefinition
	if err := c.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: agent-proxy: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	if cd.Spec.Access.Agent == nil || !cd.Spec.Access.Agent.Proxy {
		writeError(w, http.StatusForbidden, "cluster is not proxy-enabled (spec.access.agent.proxy)")
		return
	}

	if s.AgentRegistry == nil {
		writeError(w, http.StatusServiceUnavailable, "hyve-agent proxying is not configured on this API")
		return
	}
	agentConn, ok := s.AgentRegistry.Get(AgentConnectionKey{Namespace: ns, ClusterName: name})
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "cluster's hyve-agent is not currently connected")
		return
	}

	saToken := agentConn.ServiceAccountToken()
	if saToken == "" {
		writeError(w, http.StatusServiceUnavailable, "hyve-agent has not yet reported credentials for this cluster — try again shortly")
		return
	}

	role, _ := RoleFromContext(r.Context())
	group, err := agentImpersonationGroup(role)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	username, _ := UsernameFromContext(r.Context())

	s.emitAgentProxyEvent(r.Context(), &cd, username)

	rest := r.PathValue("rest")
	proxy := buildAgentReverseProxy(agentConn, rest, saToken, username, group)
	proxy.ServeHTTP(w, r)
}

// agentImpersonationGroup maps a caller's resolved hyve role onto the
// Impersonate-Group value that actually carries authorization on the
// target cluster — see agentpki.AgentProxyAdminGroup/AgentProxyReadOnlyGroup's
// own doc comment for why these two exact strings and nothing else.
// Superadmin maps to the same admin-level group as RoleAdmin, matching
// how RequireRole already treats superadmin as satisfying any
// admin-gated check elsewhere in this package.
func agentImpersonationGroup(role string) (string, error) {
	switch role {
	case hyvev1alpha1.RoleAdmin, hyvev1alpha1.RoleSuperadmin:
		return agentpki.AgentProxyAdminGroup, nil
	case hyvev1alpha1.RoleReadOnly:
		return agentpki.AgentProxyReadOnlyGroup, nil
	default:
		return "", fmt.Errorf("no hyve-agent proxy mapping for role %q", role)
	}
}

// buildAgentReverseProxy constructs a fresh, single-use ReverseProxy
// whose Transport dials out through agentConn's own SSH connection —
// cheap enough to build per request (a Transport with no persistent
// connections of its own until first use) that there's no reason to
// cache or share one across requests/agents, avoiding any shared-state
// lifecycle to manage as agents connect and disconnect.
func buildAgentReverseProxy(agentConn *AgentConnection, rest, saToken, username, group string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "https"
			req.URL.Host = agentpki.KubernetesAPIServerAddr
			req.URL.Path = "/" + rest
			req.URL.RawPath = ""
			req.Host = agentpki.KubernetesAPIServerAddr
			// The caller's own hyve session token (already verified by
			// requireAuth) is meaningless to a real Kubernetes apiserver
			// — replaced outright, not merely supplemented, with the
			// connected agent's own credential.
			req.Header.Set("Authorization", "Bearer "+saToken)
			// Impersonate-User carries identity for the target cluster's
			// own audit log even though no RBAC binding ever matches it
			// directly; Impersonate-Group is what actually authorizes
			// anything, via whichever of the two ClusterRoleBindings
			// internal/reconcile/agent.go provisioned for this group.
			req.Header.Set("Impersonate-User", "hyve:"+username)
			req.Header.Set("Impersonate-Group", group)
		},
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return dialAgentChannel(agentConn)
			},
			// Skipping verification here is specifically safe, not a
			// blanket shortcut: the SSH tunnel itself is the real trust
			// boundary — agentConn only exists because this exact agent
			// already proved its identity via a certificate signed by
			// this install's own CA (see internal/agentpki), and the raw
			// bytes traveling this channel never leave that
			// mutually-authenticated, encrypted tunnel before reaching
			// the target apiserver. hyve-api has no independent way to
			// obtain an arbitrary managed cluster's own serving CA to
			// verify against in the first place — unlike /proxy's own
			// BuildProxy, which trusts this pod's own known in-cluster CA
			// for the one cluster hyve-api itself runs on.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see comment above
		},
		// -1 flushes immediately on every write rather than batching —
		// required for kubectl logs -f/get --watch's own long-lived,
		// incrementally-written responses to actually stream rather than
		// arriving in delayed bursts. kubectl exec's own SPDY upgrade
		// doesn't go through this flush path at all (httputil.
		// ReverseProxy hijacks the connection for a genuine protocol
		// upgrade instead — confirmed against its own source), so this
		// only affects the plain-streaming cases.
		FlushInterval: -1,
	}
}

// channelConn adapts ssh.Channel (Read/Write/Close/CloseWrite, no address
// or deadline concept of its own) to the full net.Conn interface
// http.Transport.DialContext must return — the deadline/address methods
// are meaningless for a multiplexed SSH channel and are safe no-ops
// here: http.Transport itself manages request timeouts at a higher
// level, never by calling SetDeadline directly on a DialContext-returned
// conn.
type channelConn struct {
	ssh.Channel
}

func (c *channelConn) LocalAddr() net.Addr                { return channelAddr{} }
func (c *channelConn) RemoteAddr() net.Addr               { return channelAddr{} }
func (c *channelConn) SetDeadline(t time.Time) error      { return nil }
func (c *channelConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *channelConn) SetWriteDeadline(t time.Time) error { return nil }

type channelAddr struct{}

func (channelAddr) Network() string { return "ssh-channel" }
func (channelAddr) String() string  { return "ssh-channel" }

// dialAgentChannel opens a fresh ProxyChannelType channel on agentConn's
// own SSH connection — see internal/agentpki.ProxyChannelType's own doc
// comment for why this specific channel type/payload shape, and
// internal/agent/proxy.go for how the agent side handles it (a pure raw
// byte-relay to its own local apiserver, no HTTP/TLS logic of its own).
func dialAgentChannel(agentConn *AgentConnection) (net.Conn, error) {
	ch, reqs, err := agentConn.Conn.OpenChannel(agentpki.ProxyChannelType, agentpki.NewProxyChannelPayload())
	if err != nil {
		return nil, fmt.Errorf("open hyve-agent proxy channel: %w", err)
	}
	go ssh.DiscardRequests(reqs)
	return &channelConn{Channel: ch}, nil
}

// emitAgentProxyEvent records who proxied to which cluster and when —
// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's own resolved "Audit trail:
// connection-level for v1, not full command-level". Best-effort: a
// failure here is logged, never lets an audit-trail hiccup block the
// actual proxied request. See emitClusterEvent (server.go) for the
// shared implementation.
func (s *Server) emitAgentProxyEvent(ctx context.Context, cd *hyvev1alpha1.ClusterDefinition, username string) {
	emitClusterEvent(ctx, s.Clientset, cd.Namespace, cd.Name, "AgentProxyRequest",
		fmt.Sprintf("%s proxied a kubectl request to this cluster via hyve-agent", username))
}
