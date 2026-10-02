package api

import (
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/cbridges1/hyve/internal/agentpki"
)

// registerAgentTunnelWebSocketRoute serves the agent tunnel over a
// WebSocket on the main HTTP listener (agentpki.TunnelWebSocketPath) —
// the same SSH session ServeAgentTunnel's raw TCP listener serves, so an
// agent can reach hyve-api through any HTTPS reverse proxy (an Ingress,
// Pangolin, a cloud load balancer) at the control-plane URL it already
// uses for bootstrap, instead of needing a raw TCP port exposed. Mounted
// outside /api's requireAuth, like /agent/bootstrap: the SSH handshake
// inside the WebSocket is the authentication (an agent certificate signed
// by AgentCA — see buildAgentServerConfig).
func (s *Server) registerAgentTunnelWebSocketRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET "+agentpki.TunnelWebSocketPath, s.handleAgentTunnelWebSocket)
}

func (s *Server) handleAgentTunnelWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.AgentCA == nil || s.AgentRegistry == nil {
		writeError(w, http.StatusServiceUnavailable, "agent tunnel is not configured on this hyve-api")
		return
	}
	config, err := s.buildAgentServerConfig()
	if err != nil {
		log.Printf("api: agent tunnel (websocket): build SSH server config: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to start agent tunnel")
		return
	}
	// Default AcceptOptions: the Origin check only applies when an Origin
	// header is present (a browser); hyve-agent sends none.
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept has already written the HTTP error response.
		log.Printf("api: agent tunnel (websocket): upgrade from %s failed: %v", agentClientAddr(r), err)
		return
	}
	// Bounded by the request: this handler blocks for the tunnel's whole
	// lifetime (handleAgentConnection returns once the SSH session ends),
	// so the request context only ends with it. NetConn also lifts the
	// default 32 KiB message limit, which SSH packets can exceed.
	conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
	defer conn.Close()
	s.handleAgentConnection(&addrConn{Conn: conn, remote: tunnelAddr(agentClientAddr(r))}, config)
}

// agentClientAddr is the agent's address for logging: the first
// X-Forwarded-For hop when hyve-api sits behind a proxy, else the
// connection's own remote address.
func agentClientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	return r.RemoteAddr
}

// addrConn overrides RemoteAddr so handleAgentConnection's logs show the
// agent rather than the proxy in front of hyve-api.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c *addrConn) RemoteAddr() net.Addr { return c.remote }

type tunnelAddr string

func (a tunnelAddr) Network() string { return "websocket" }
func (a tunnelAddr) String() string  { return string(a) }
