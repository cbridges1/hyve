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
// proxy handler opens new channels against Conn per proxied request; this
// milestone never does.
type AgentConnection struct {
	Conn        ssh.Conn
	Version     string
	ConnectedAt time.Time
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
func (r *AgentRegistry) RemoveIfCurrent(key AgentConnectionKey, conn *AgentConnection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[key] == conn {
		delete(r.conns, key)
	}
}

// Get returns key's current connection, if any.
func (r *AgentRegistry) Get(key AgentConnectionKey) (*AgentConnection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.conns[key]
	return c, ok
}
