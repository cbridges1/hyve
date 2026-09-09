package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAgentRegistry_RegisterAndGet(t *testing.T) {
	r := NewAgentRegistry()
	key := AgentConnectionKey{Namespace: "acme", ClusterName: "web"}
	conn := &AgentConnection{ConnectedAt: time.Now()}

	_, ok := r.Get(key)
	assert.False(t, ok, "an unregistered key must not be found")

	r.Register(key, conn)
	got, ok := r.Get(key)
	assert.True(t, ok)
	assert.Same(t, conn, got)
}

func TestAgentRegistry_DifferentNamespacesDontCollide(t *testing.T) {
	r := NewAgentRegistry()
	connA := &AgentConnection{}
	connB := &AgentConnection{}

	// Same cluster name, different namespaces — must not collide, per the
	// registry's own (namespace, clusterName) key shape.
	r.Register(AgentConnectionKey{Namespace: "acme", ClusterName: "web"}, connA)
	r.Register(AgentConnectionKey{Namespace: "other", ClusterName: "web"}, connB)

	gotA, ok := r.Get(AgentConnectionKey{Namespace: "acme", ClusterName: "web"})
	assert.True(t, ok)
	assert.Same(t, connA, gotA)

	gotB, ok := r.Get(AgentConnectionKey{Namespace: "other", ClusterName: "web"})
	assert.True(t, ok)
	assert.Same(t, connB, gotB)
}

func TestAgentRegistry_RemoveIfCurrent_RemovesMatchingConnection(t *testing.T) {
	r := NewAgentRegistry()
	key := AgentConnectionKey{Namespace: "acme", ClusterName: "web"}
	conn := &AgentConnection{}

	r.Register(key, conn)
	r.RemoveIfCurrent(key, conn)

	_, ok := r.Get(key)
	assert.False(t, ok)
}

// TestAgentRegistry_RemoveIfCurrent_IgnoresStaleConnection is the actual
// race RemoveIfCurrent exists to guard: an old connection's own deferred
// cleanup must not evict a newer connection that already reconnected and
// re-registered under the same key.
func TestAgentRegistry_RemoveIfCurrent_IgnoresStaleConnection(t *testing.T) {
	r := NewAgentRegistry()
	key := AgentConnectionKey{Namespace: "acme", ClusterName: "web"}
	oldConn := &AgentConnection{}
	newConn := &AgentConnection{}

	r.Register(key, oldConn)
	r.Register(key, newConn) // simulates a reconnect racing the old connection's own cleanup

	r.RemoveIfCurrent(key, oldConn) // the stale connection's cleanup, arriving late

	got, ok := r.Get(key)
	assert.True(t, ok, "the newer connection must survive a stale RemoveIfCurrent call")
	assert.Same(t, newConn, got)
}
