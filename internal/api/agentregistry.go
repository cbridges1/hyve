package api

import (
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// AgentConnectionKey identifies one connected agent — (namespace,
// clusterName), the same pair internal/agentpki.AgentPrincipal encodes
// into a connecting agent's certificate. Never constructed from anything
// but that certificate's own principal (see agent_listener.go) — this is
// what makes the registry itself part of "Multi-tenancy scoping of the
// connection registry"'s cryptographic-identity story, not a caller-
// suppliable value.
type AgentConnectionKey struct {
	Namespace   string
	ClusterName string
}

// AgentConnection is one live agent's SSH connection, held in
// Server.AgentRegistry for as long as it stays connected. Milestone 5's
// proxy handler (agent_proxy.go) opens new channels against Conn per
// proxied request, and reads ServiceAccountToken (via Heartbeat) for the
// bearer token it needs to actually authenticate that request to the
// target cluster's own apiserver.
type AgentConnection struct {
	Conn        ssh.Conn
	ConnectedAt time.Time

	// mu guards version/serviceAccountToken below — both are written by
	// drainAgentRequests's own goroutine (agent_listener.go, once per
	// heartbeat) and, since milestone 5, read concurrently by
	// agent_proxy.go on every proxied request from a different goroutine
	// entirely. Version was previously read/written with no
	// synchronization at all (harmless in practice before milestone 5,
	// since nothing read it from a different goroutine than the one
	// disconnect-time read in writeAgentStatus) — fixed here alongside
	// adding ServiceAccountToken rather than leaving one field safe and
	// the other not.
	mu                  sync.RWMutex
	version             string
	serviceAccountToken string
}

// SetHeartbeat records this connection's latest self-reported version and
// current ServiceAccount token — called once per heartbeat received (see
// drainAgentRequests).
func (c *AgentConnection) SetHeartbeat(version, serviceAccountToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version = version
	c.serviceAccountToken = serviceAccountToken
}

// Version returns the most recently heartbeat-reported version, "" if
// none has arrived yet.
func (c *AgentConnection) Version() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// ServiceAccountToken returns the most recently heartbeat-reported
// token, "" if none has arrived yet — agent_proxy.go treats an empty
// value as "this agent hasn't reported credentials yet", not as an
// invalid/expired token to try anyway.
func (c *AgentConnection) ServiceAccountToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serviceAccountToken
}

// AgentRegistry is the in-memory, (namespace, clusterName)-keyed map of
// currently live agent connections — see
// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Control-plane side". This is
// deliberately not the durable record of agent status:
// ClusterDefinitionStatus.Agent (written by agent_listener.go on every
// connect/disconnect) is "was it connected as of the last time we
// checked"; this registry only ever answers "is it connected right now,
// in this one process" — it holds no state across a pod restart, and
// isn't meant to.
type AgentRegistry struct {
	mu    sync.RWMutex
	conns map[AgentConnectionKey]*AgentConnection
}

// NewAgentRegistry returns an empty registry, ready to use.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{conns: make(map[AgentConnectionKey]*AgentConnection)}
}

// Register records conn as the current live connection for key,
// overwriting whatever was there — a reconnect naturally replaces a
// stale entry this way, without needing to distinguish "first connect"
// from "reconnect after a drop."
func (r *AgentRegistry) Register(key AgentConnectionKey, conn *AgentConnection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[key] = conn
}

// RemoveIfCurrent deletes key's entry only if it's still exactly conn —
// not a plain delete-by-key. Guards a real race: if an agent reconnects
// (Register-ing a new *AgentConnection under the same key) in the narrow
// window between its old connection's Wait() returning and that old
// connection's own deferred cleanup calling Remove, an unconditional
// delete-by-key would wrongly evict the new, valid connection instead of
// the stale one that actually died. Comparing the stored pointer against
// the one the caller believes it owns closes that window.
//
// Returns whether conn actually was the current entry (and so was
// removed) — handleAgentConnection's own caller uses this to decide
// whether a Connected: false status write is warranted at all. Confirmed
// live: several near-simultaneous connections during a hyve-api restart
// (the agent retrying while the old TCP connection hadn't yet been
// reported closed) left the registry correctly holding the one that's
// actually still alive, but every one of the older connections' own
// disconnect handler still unconditionally wrote Connected: false
// afterward — the last one to finish (which can easily be well after the
// real, current connection registered) stomped a correct "connected: true"
// back to false, even though the tunnel itself kept working the whole
// time (RemoveIfCurrent's own guard already protected the registry these
// proxied requests actually use).
func (r *AgentRegistry) RemoveIfCurrent(key AgentConnectionKey, conn *AgentConnection) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[key] == conn {
		delete(r.conns, key)
		return true
	}
	return false
}

// Get returns key's current connection, if any.
func (r *AgentRegistry) Get(key AgentConnectionKey) (*AgentConnection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.conns[key]
	return c, ok
}
