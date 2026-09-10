package agent

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"

	"golang.org/x/crypto/ssh"
)

const heartbeatInterval = 30 * time.Second

// serviceAccountTokenPath is where kubelet mounts (and keeps rotating in
// place, roughly hourly for a projected token) this pod's own
// ServiceAccount token — the standard, well-known path any in-cluster pod
// can read regardless of Kubernetes version or whether the cluster uses
// the legacy or projected token style, no RBAC of its own required (it's
// a file mount, not an API call).
const serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// runHeartbeat sends a lightweight liveness/version/credential signal
// over conn every heartbeatInterval (plus once immediately on connect, so
// milestone 5's proxy path has a token to work with right away rather
// than waiting out the first full interval) until ctx is done or conn
// itself stops accepting requests — folded into the existing tunnel
// connection, not a second connection or a separate polling mechanism,
// per the proposal doc's own "Agent responsibilities".
func runHeartbeat(ctx context.Context, conn ssh.Conn, version string) {
	send := func() bool {
		payload, err := json.Marshal(agentpki.HeartbeatPayload{
			Version:             version,
			ServiceAccountToken: readServiceAccountToken(),
		})
		if err != nil {
			log.Printf("agent: failed to marshal heartbeat payload: %v", err)
			return true
		}
		if _, _, err := conn.SendRequest(agentpki.HeartbeatRequestType, true, payload); err != nil {
			// Run's own conn.Wait() will notice the same underlying
			// failure and drive the reconnect — nothing more to do here
			// than stop trying on a connection that's already dead.
			return false
		}
		return true
	}

	if !send() {
		return
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

// readServiceAccountToken reads this pod's own current token fresh on
// every call, deliberately never cached — kubelet rotates the file's
// contents in place, and a heartbeat's whole job is to keep hyve-api
// holding a token that's actually still valid. A read failure (e.g. this
// binary running outside a pod entirely, for local testing against a
// port-forwarded listener — see milestone 3's own manual verification)
// silently returns "" rather than logging: hyve-api's own proxy handler
// already treats an empty ServiceAccountToken as "not ready yet" (see
// internal/api/agent_proxy.go), the correct behavior for a
// connect-status-only agent that was never going to serve proxy traffic
// in the first place; logging a warning on every 30s tick forever would
// just be noise for that entirely normal case.
func readServiceAccountToken() string {
	data, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return ""
	}
	return string(data)
}
