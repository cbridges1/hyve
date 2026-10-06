package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cbridges1/hyve/internal/agentpki"
	"github.com/cbridges1/hyve/internal/api"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// TestConnectOnce_WebSocketTunnel runs the real agent dial path against the
// real hyve-api routes over a WebSocket: the agent registers, its status is
// written, a payload far beyond a WebSocket's default 32 KiB message limit
// crosses the tunnel, and closing the connection deregisters it.
func TestConnectOnce_WebSocketTunnel(t *testing.T) {
	ctx := context.Background()
	ca, err := agentpki.LoadOrCreateCA(ctx, k8sfake.NewClientset(), "hyve-system")
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	require.NoError(t, hyvev1alpha1.AddToScheme(scheme))
	cd := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme"}}
	fakeClient := clientfake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{}).WithObjects(cd).Build()

	s := &api.Server{Client: fakeClient, AgentCA: ca, AgentRegistry: api.NewAgentRegistry()}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()

	cfg := Config{
		TunnelAddress: "ws" + strings.TrimPrefix(ts.URL, "http") + agentpki.TunnelWebSocketPath,
		Identity:      newTestIdentity(t, ca, "acme", "web"),
		Version:       "test",
	}
	conn, err := connectOnce(cfg)
	require.NoError(t, err)

	key := api.AgentConnectionKey{Namespace: "acme", ClusterName: "web"}
	require.Eventually(t, func() bool { _, ok := s.AgentRegistry.Get(key); return ok },
		2*time.Second, 10*time.Millisecond, "agent must register over the WebSocket tunnel")
	require.Eventually(t, func() bool {
		var fresh hyvev1alpha1.ClusterDefinition
		return fakeClient.Get(ctx, types.NamespacedName{Namespace: "acme", Name: "web"}, &fresh) == nil && fresh.Status.Agent.Connected
	}, 2*time.Second, 10*time.Millisecond, "status.agent.connected must be set")

	// An unknown global request gets a plain negative reply — getting any
	// reply proves the 200 KB message crossed the tunnel intact.
	ok, _, err := conn.SendRequest("large-test@hyve.io", true, make([]byte, 200*1024))
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, conn.Close())
	require.Eventually(t, func() bool { _, ok := s.AgentRegistry.Get(key); return !ok },
		2*time.Second, 10*time.Millisecond, "closing the tunnel must deregister the agent")
}

func TestConnectOnce_WebSocketTunnelRejectsForeignCertificate(t *testing.T) {
	ctx := context.Background()
	ca, err := agentpki.LoadOrCreateCA(ctx, k8sfake.NewClientset(), "hyve-system")
	require.NoError(t, err)
	otherCA, err := agentpki.LoadOrCreateCA(ctx, k8sfake.NewClientset(), "elsewhere")
	require.NoError(t, err)

	s := &api.Server{Client: fakeClientFor(t), AgentCA: ca, AgentRegistry: api.NewAgentRegistry()}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()

	identity := newTestIdentity(t, otherCA, "acme", "web")
	identity.CAPublicKey = ca.PublicKey() // trusts the server, but its cert is from another CA
	_, err = connectOnce(Config{
		TunnelAddress: "ws" + strings.TrimPrefix(ts.URL, "http") + agentpki.TunnelWebSocketPath,
		Identity:      identity,
	})
	require.Error(t, err, "a certificate the control plane's CA didn't sign must be refused")
}

func newTestIdentity(t *testing.T, ca *agentpki.CA, namespace, clusterName string) *Identity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromSigner(priv)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	cert, err := ca.SignAgentUserCertificate(ssh.MarshalAuthorizedKey(sshPub), namespace, clusterName)
	require.NoError(t, err)
	return &Identity{Signer: signer, Certificate: cert, CAPublicKey: ca.PublicKey()}
}

func fakeClientFor(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, hyvev1alpha1.AddToScheme(scheme))
	return clientfake.NewClientBuilder().WithScheme(scheme).Build()
}

func TestSSHAddress(t *testing.T) {
	for in, want := range map[string]string{
		"hyve-api.example.com:8092":                   "hyve-api.example.com:8092",
		"wss://hyve.hosted.matlass.com/agent/tunnel":  "hyve.hosted.matlass.com:443",
		"ws://127.0.0.1:52694/agent/tunnel":           "127.0.0.1:52694",
		"ws://hyve-api.127.0.0.1.nip.io/agent/tunnel": "hyve-api.127.0.0.1.nip.io:80",
	} {
		assert.Equal(t, want, sshAddress(in), in)
	}
}
