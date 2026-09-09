package agent

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"

	"golang.org/x/crypto/ssh"
)

const heartbeatInterval = 30 * time.Second

// runHeartbeat sends a lightweight liveness/version signal over conn
// every heartbeatInterval until ctx is done or conn itself stops
// accepting requests — folded into the existing tunnel connection, not a
// second connection or a separate polling mechanism, per the proposal
// doc's own "Agent responsibilities".
func runHeartbeat(ctx context.Context, conn ssh.Conn, version string) {
	payload, err := json.Marshal(agentpki.HeartbeatPayload{Version: version})
	if err != nil {
		log.Printf("agent: failed to marshal heartbeat payload: %v", err)
		return
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := conn.SendRequest(agentpki.HeartbeatRequestType, true, payload); err != nil {
				// Run's own conn.Wait() will notice the same underlying
				// failure and drive the reconnect — nothing more to do
				// here than stop trying on a connection that's already
				// dead.
				return
			}
		}
	}
}
