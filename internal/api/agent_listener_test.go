package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// dialTestAgent connects to the listener at addr as an agent would —
// generates its own keypair, gets it signed by ca for (namespace,
// clusterName), and dials with the resulting certificate — constructed
// directly here rather than via internal/agent's own Run/Identity (which
// carries a reconnect/backoff loop this test has no use for), per the
// implementation plan's own "a test agent constructed directly with a
// locally-signed test cert, not via the full CLI binary."
func dialTestAgent(t *testing.T, addr string, ca *agentpki.CA, namespace, clusterName string) ssh.Conn {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromSigner(priv)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)

	cert, err := ca.SignAgentUserCertificate(ssh.MarshalAuthorizedKey(sshPub), namespace, clusterName)
	require.NoError(t, err)
	certSigner, err := ssh.NewCertSigner(cert, signer)
	require.NoError(t, err)

	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, address string) bool {
			return string(auth.Marshal()) == string(ca.PublicKey().Marshal())
		},
	}
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		// Must match the certificate's own ValidPrincipals — see
		// internal/agent/connect.go's connectOnce for why this can't be a
		// fixed placeholder username.
		User:            agentpki.AgentPrincipal(namespace, clusterName),
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(certSigner)},
		HostKeyCallback: checker.CheckHostKey,
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	return client
}

// TestAgentTunnel_ConnectDisconnectCycle is the integration test the
// implementation plan calls for: stand up a real listener, connect a real
// test agent, confirm the registry (and ClusterDefinitionStatus.Agent)
// reflect a real connect/disconnect cycle.
func TestAgentTunnel_ConnectDisconnectCycle(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ca, err := agentpki.LoadOrCreateCA(context.Background(), clientset, testNamespace)
	require.NoError(t, err)

	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme"},
	}
	fakeClient := newFakeClient(t, cd)

	s := &Server{
		Client:        fakeClient,
		AgentCA:       ca,
		AgentRegistry: NewAgentRegistry(),
	}

	listener, err := s.ListenAgentTunnel("127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.ServeAgentTunnelListener(ctx, listener) }()

	conn := dialTestAgent(t, addr, ca, "acme", "web")

	key := AgentConnectionKey{Namespace: "acme", ClusterName: "web"}
	require.Eventually(t, func() bool {
		_, ok := s.AgentRegistry.Get(key)
		return ok
	}, 2*time.Second, 10*time.Millisecond, "registry must reflect the connection")

	require.Eventually(t, func() bool {
		var fresh hyvev1alpha1.ClusterDefinition
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "acme", Name: "web"}, &fresh); err != nil {
			return false
		}
		return fresh.Status.Agent.Connected && fresh.Status.Agent.LastConnectedAt != ""
	}, 2*time.Second, 10*time.Millisecond, "ClusterDefinitionStatus.Agent.Connected must be set true on connect")

	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		_, ok := s.AgentRegistry.Get(key)
		return !ok
	}, 2*time.Second, 10*time.Millisecond, "registry must reflect the disconnection")

	require.Eventually(t, func() bool {
		var fresh hyvev1alpha1.ClusterDefinition
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "acme", Name: "web"}, &fresh); err != nil {
			return false
		}
		return !fresh.Status.Agent.Connected && fresh.Status.Agent.LastDisconnectedAt != ""
	}, 2*time.Second, 10*time.Millisecond, "ClusterDefinitionStatus.Agent.Connected must flip to false on disconnect")
}

// TestAgentTunnel_RejectsUnsignedKey confirms a bare (uncertified) key —
// even a perfectly valid Ed25519 keypair — is refused, since
// IsUserAuthority only ever recognizes certificates signed by this
// install's own CA, never a raw key.
func TestAgentTunnel_RejectsUnsignedKey(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ca, err := agentpki.LoadOrCreateCA(context.Background(), clientset, testNamespace)
	require.NoError(t, err)

	s := &Server{
		Client:        newFakeClient(t),
		AgentCA:       ca,
		AgentRegistry: NewAgentRegistry(),
	}
	listener, err := s.ListenAgentTunnel("127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.ServeAgentTunnelListener(ctx, listener) }()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromSigner(priv)
	require.NoError(t, err)

	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, address string) bool {
			return string(auth.Marshal()) == string(ca.PublicKey().Marshal())
		},
	}
	_, err = ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "hyve-agent",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)}, // bare key, no certificate
		HostKeyCallback: checker.CheckHostKey,
		Timeout:         5 * time.Second,
	})
	assert.Error(t, err, "a connection presenting a bare key instead of a CA-signed certificate must be rejected")
}
