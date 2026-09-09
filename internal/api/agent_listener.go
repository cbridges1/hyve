package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"

	"golang.org/x/crypto/ssh"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// agentPrincipalExtensionKey is where the wrapped PublicKeyCallback below
// stashes the connecting agent's parsed (namespace, clusterName) —
// Permissions.Extensions is the documented mechanism for carrying data
// from SSH's authentication phase into the application code that runs
// after NewServerConn returns.
const agentPrincipalExtensionKey = "hyve-agent-principal"

// ServeAgentTunnel binds bindAddress and accepts hyve-agent connections on
// it until ctx is done — see cmd/api/run.go for where this is started, in
// its own goroutine, mirroring the relay listener's own "a fatal error
// here only kills this one feature, not the whole API" stance. A thin
// wrapper around ListenAgentTunnel + ServeAgentTunnelListener, split in
// two so a caller that needs to know the actual bound address (a test
// binding to bindAddress ":0" for a random free port, say) can do the
// bind and the serve loop as separate steps.
func (s *Server) ServeAgentTunnel(ctx context.Context, bindAddress string) error {
	listener, err := s.ListenAgentTunnel(bindAddress)
	if err != nil {
		return err
	}
	return s.ServeAgentTunnelListener(ctx, listener)
}

// ListenAgentTunnel validates AgentCA/AgentRegistry are configured and
// binds bindAddress — a raw TCP listener, not an http.Handler, so (unlike
// Routes()/RelayRoutes()) nothing here is handed to http.ListenAndServe.
// Callers that don't need the actual bound address back should generally
// call ServeAgentTunnel instead of this plus ServeAgentTunnelListener
// separately.
func (s *Server) ListenAgentTunnel(bindAddress string) (net.Listener, error) {
	if s.AgentCA == nil {
		return nil, fmt.Errorf("agent tunnel listener requires AgentCA to be configured")
	}
	if s.AgentRegistry == nil {
		return nil, fmt.Errorf("agent tunnel listener requires AgentRegistry to be configured")
	}
	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", bindAddress, err)
	}
	return listener, nil
}

// ServeAgentTunnelListener accepts and handles hyve-agent connections on
// listener until ctx is done.
func (s *Server) ServeAgentTunnelListener(ctx context.Context, listener net.Listener) error {
	config, err := s.buildAgentServerConfig()
	if err != nil {
		return fmt.Errorf("build agent SSH server config: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go s.handleAgentConnection(conn, config)
	}
}

// buildAgentServerConfig generates a fresh host keypair and gets it
// signed by AgentCA — fresh per listener startup, deliberately not
// persisted. Safe because an agent never pins a specific host key; it
// trusts any host certificate signed by the shared CA
// (ssh.CertChecker.IsHostAuthority checks the CA's public key, not any
// one host key), so a new host key on every hyve-api restart costs
// nothing and avoids needing a second persisted-Secret dance alongside
// the CA's own.
func (s *Server) buildAgentServerConfig() (*ssh.ServerConfig, error) {
	hostPub, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host keypair: %w", err)
	}
	hostSigner, err := ssh.NewSignerFromSigner(hostPriv)
	if err != nil {
		return nil, fmt.Errorf("build host signer: %w", err)
	}
	sshHostPub, err := ssh.NewPublicKey(hostPub)
	if err != nil {
		return nil, fmt.Errorf("wrap host public key: %w", err)
	}
	// An empty principals list means "valid for any host" per the SSH
	// certificate spec (confirmed directly against golang.org/x/crypto/ssh's
	// own CheckCert: "By default, certs are valid for all users/hosts" when
	// ValidPrincipals is empty) — the right call here, not a shortcut:
	// there's no one fixed hostname to bake in, since an agent might dial
	// this listener via an internal DNS name, an external load balancer
	// address, or a bare IP depending on how this install is deployed. The
	// thing actually being verified is "signed by our CA," not "is
	// specifically named X."
	hostCert, err := s.AgentCA.SignHostCertificate(sshHostPub, nil, 90*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("sign host certificate: %w", err)
	}
	certSigner, err := ssh.NewCertSigner(hostCert, hostSigner)
	if err != nil {
		return nil, fmt.Errorf("build host cert signer: %w", err)
	}

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(s.AgentCA.PublicKey().Marshal())
		},
	}

	config := &ssh.ServerConfig{
		// Wraps checker.Authenticate (the documented PublicKeyCallback
		// value for certificate-based auth) to additionally stash the
		// certificate's own principal into Permissions.Extensions —
		// checker.Authenticate alone verifies the certificate but has no
		// way to hand its principal back to the caller of NewServerConn.
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			perms, err := checker.Authenticate(conn, key)
			if err != nil {
				return nil, err
			}
			cert, ok := key.(*ssh.Certificate)
			if !ok {
				return nil, fmt.Errorf("agent presented a bare key, not a certificate")
			}
			if len(cert.ValidPrincipals) != 1 {
				return nil, fmt.Errorf("agent certificate must have exactly one principal, got %d", len(cert.ValidPrincipals))
			}
			if perms == nil {
				perms = &ssh.Permissions{}
			}
			if perms.Extensions == nil {
				perms.Extensions = map[string]string{}
			}
			perms.Extensions[agentPrincipalExtensionKey] = cert.ValidPrincipals[0]
			return perms, nil
		},
	}
	config.AddHostKey(certSigner)
	return config, nil
}

// handleAgentConnection runs the SSH handshake for one accepted TCP
// connection, and — once authenticated — the connection's full lifecycle:
// register in AgentRegistry and write ClusterDefinitionStatus.Agent
// (Connected: true) immediately, drain global requests (heartbeats) and
// reject any channel-open requests (milestone 5's job, not this one)
// until the connection drops, then remove from the registry and write
// status again (Connected: false). Runs in its own goroutine per
// connection — ServeAgentTunnel never blocks on this.
func (s *Server) handleAgentConnection(conn net.Conn, config *ssh.ServerConfig) {
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		log.Printf("api: agent tunnel handshake failed from %s: %v", conn.RemoteAddr(), err)
		return
	}

	principal := serverConn.Permissions.Extensions[agentPrincipalExtensionKey]
	namespace, clusterName, err := agentpki.ParseAgentPrincipal(principal)
	if err != nil {
		log.Printf("api: agent tunnel from %s presented an unparseable principal %q: %v", conn.RemoteAddr(), principal, err)
		_ = serverConn.Close()
		return
	}
	key := AgentConnectionKey{Namespace: namespace, ClusterName: clusterName}

	// Channels this connection will never use in this milestone still have
	// to be serviced per ssh.NewServerConn's own documented requirement
	// ("must be serviced, or the connection will hang") — reject every
	// channel-open outright (no proxy yet) rather than leaving it
	// unhandled.
	go func() {
		for newChannel := range chans {
			_ = newChannel.Reject(ssh.Prohibited, "hyve-agent proxying is not enabled on this connection yet")
		}
	}()

	agentConn := &AgentConnection{Conn: serverConn, ConnectedAt: time.Now()}
	go s.drainAgentRequests(reqs, agentConn)

	s.AgentRegistry.Register(key, agentConn)
	s.writeAgentStatus(key, true, agentConn.Version)
	log.Printf("api: hyve-agent connected — namespace=%s cluster=%s remote=%s", namespace, clusterName, conn.RemoteAddr())

	err = serverConn.Wait()
	s.AgentRegistry.RemoveIfCurrent(key, agentConn)
	s.writeAgentStatus(key, false, agentConn.Version)
	log.Printf("api: hyve-agent disconnected — namespace=%s cluster=%s (%v)", namespace, clusterName, err)
}

// drainAgentRequests services one connection's global SSH requests for as
// long as it's open — the heartbeat payload updates agentConn.Version
// in place (read by the next writeAgentStatus call on disconnect, and
// available to milestone 5's proxy authorization work immediately);
// anything else this listener doesn't recognize gets a plain negative
// reply when one is requested, never left unanswered.
func (s *Server) drainAgentRequests(reqs <-chan *ssh.Request, agentConn *AgentConnection) {
	for req := range reqs {
		if req.Type == agentpki.HeartbeatRequestType {
			var payload agentpki.HeartbeatPayload
			if err := json.Unmarshal(req.Payload, &payload); err == nil {
				agentConn.Version = payload.Version
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			continue
		}
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

// writeAgentStatus patches ClusterDefinitionStatus.Agent directly — a
// merge patch targeting only status.agent, not a Get-then-Status().Update
// round trip, deliberately: the controller's own reconcile loop writes
// other status fields (Conditions, LastCreateOutput, ...) on the same
// object, from a different process, at times this listener has no way to
// coordinate with — a full-object Update would race that on
// resourceVersion; a merge patch scoped to one subfield doesn't need to.
// Best-effort: a cluster that's been deleted out from under a still-
// connecting agent logs a warning rather than blocking the connection
// lifecycle on it.
func (s *Server) writeAgentStatus(key AgentConnectionKey, connected bool, version string) {
	now := time.Now().UTC().Format(time.RFC3339)
	agentStatus := hyvev1alpha1.AgentStatus{Connected: connected, Version: version}
	if connected {
		agentStatus.LastConnectedAt = now
	} else {
		agentStatus.LastDisconnectedAt = now
	}

	patch, err := json.Marshal(map[string]any{"status": map[string]any{"agent": agentStatus}})
	if err != nil {
		log.Printf("api: failed to marshal agent status patch for %s/%s: %v", key.Namespace, key.ClusterName, err)
		return
	}

	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: key.ClusterName, Namespace: key.Namespace},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Client.Status().Patch(ctx, cd, client.RawPatch(types.MergePatchType, patch)); err != nil {
		log.Printf("api: failed to patch agent status for %s/%s: %v", key.Namespace, key.ClusterName, err)
	}
}
