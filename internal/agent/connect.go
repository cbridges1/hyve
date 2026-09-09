package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/ssh"
)

// Config bundles everything Run needs to maintain a tunnel connection to
// the control plane.
type Config struct {
	// TunnelAddress is the control plane's own SSH tunnel listener
	// (internal/api.Server.ServeAgentTunnel), host:port.
	TunnelAddress string
	// Identity carries CAPublicKey alongside the agent's own certificate
	// (see Identity's own doc comment) — both came from the same
	// bootstrap exchange, so Run verifies the control plane's host
	// certificate against Identity.CAPublicKey rather than taking a
	// separate value that could drift from it.
	Identity *Identity
	// Version is this agent's own build version, reported with every
	// heartbeat — see docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's "Agent
	// image/version" for why a mismatch against HyveConfig.spec.
	// defaultAgentImage is meant to be directly visible, not inferred.
	Version string
}

// Run dials the control plane and maintains the connection until ctx is
// canceled, reconnecting with exponential backoff (capped at 30s) on any
// drop — never gives up on its own, since a managed cluster's own network
// path to the control plane can be down for reasons entirely outside
// hyve-agent's control and unrelated to whether it should keep trying.
// Blocks; call from its own goroutine or as cmd/agent's main loop.
func Run(ctx context.Context, cfg Config) error {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		conn, err := connectOnce(cfg)
		if err != nil {
			log.Printf("agent: connect to %s failed, retrying in %s: %v", cfg.TunnelAddress, backoff, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		backoff = time.Second // reset only after an actual successful connection
		log.Printf("agent: connected to %s", cfg.TunnelAddress)

		heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
		go runHeartbeat(heartbeatCtx, conn, cfg.Version)

		// conn.Wait() only ever returns when the connection itself drops —
		// it has no idea ctx exists. Left alone, a canceled ctx (e.g. a
		// pod's normal SIGTERM grace period) would leave this goroutine
		// blocked here, the tunnel still fully connected and still
		// reporting ClusterDefinitionStatus.Agent.Connected: true from
		// hyve-api's own point of view, until something eventually force-
		// kills the process — so ctx.Done() closes conn itself to
		// actually unblock Wait() and let a graceful shutdown look like
		// one.
		waitDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-waitDone:
			}
		}()

		waitErr := conn.Wait()
		close(waitDone)
		stopHeartbeat()
		log.Printf("agent: disconnected from %s: %v", cfg.TunnelAddress, waitErr)
	}
}

func connectOnce(cfg Config) (ssh.Conn, error) {
	authMethod, err := cfg.Identity.AuthMethod()
	if err != nil {
		return nil, fmt.Errorf("build auth method: %w", err)
	}
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, address string) bool {
			return string(auth.Marshal()) == string(cfg.Identity.CAPublicKey.Marshal())
		},
	}
	// ssh.CertChecker.Authenticate (run server-side, see
	// internal/api/agent_listener.go's PublicKeyCallback) checks the SSH
	// connection's own username against the certificate's ValidPrincipals
	// — a fixed "hyve-agent" username would never match the
	// namespace/clusterName principal agentpki.SignAgentUserCertificate
	// actually signed, so this has to be that same principal, read back
	// off the certificate itself rather than duplicated as a separate
	// value that could drift from it.
	if len(cfg.Identity.Certificate.ValidPrincipals) != 1 {
		return nil, fmt.Errorf("agent certificate must have exactly one principal, got %d", len(cfg.Identity.Certificate.ValidPrincipals))
	}
	clientConfig := &ssh.ClientConfig{
		User:            cfg.Identity.Certificate.ValidPrincipals[0],
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: checker.CheckHostKey,
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", cfg.TunnelAddress, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.TunnelAddress, err)
	}
	return client, nil
}
