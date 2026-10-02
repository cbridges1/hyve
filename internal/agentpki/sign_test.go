package agentpki

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestAgentPrincipal_RoundTrips(t *testing.T) {
	cases := []struct{ namespace, clusterName string }{
		{"acme", "web"},
		{"hyve-system", "local"},
		{"acme", "web-2"}, // hyphen in the cluster name must not confuse the split
	}
	for _, c := range cases {
		principal := AgentPrincipal(c.namespace, c.clusterName)
		gotNS, gotName, err := ParseAgentPrincipal(principal)
		require.NoError(t, err)
		assert.Equal(t, c.namespace, gotNS)
		assert.Equal(t, c.clusterName, gotName)
	}
}

func TestParseAgentPrincipal_RejectsMalformed(t *testing.T) {
	_, _, err := ParseAgentPrincipal("no-separator-here")
	assert.Error(t, err)
}

func testAuthorizedKey(t *testing.T) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return ssh.MarshalAuthorizedKey(sshPub)
}

func TestSignAgentUserCertificate_EncodesPrincipalAndVerifies(t *testing.T) {
	ca, err := LoadOrCreateCA(context.Background(), k8sfake.NewClientset(), testNamespace)
	require.NoError(t, err)

	cert, err := ca.SignAgentUserCertificate(testAuthorizedKey(t), "acme", "web")
	require.NoError(t, err)
	assert.Equal(t, uint32(ssh.UserCert), cert.CertType)
	require.Equal(t, []string{"acme/web"}, cert.ValidPrincipals)

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(ca.PublicKey().Marshal())
		},
	}
	require.NoError(t, checker.CheckCert("acme/web", cert))

	gotNS, gotName, err := ParseAgentPrincipal(cert.ValidPrincipals[0])
	require.NoError(t, err)
	assert.Equal(t, "acme", gotNS)
	assert.Equal(t, "web", gotName)
}

func TestSignAgentUserCertificate_RejectsGarbagePublicKey(t *testing.T) {
	ca, err := LoadOrCreateCA(context.Background(), k8sfake.NewClientset(), testNamespace)
	require.NoError(t, err)

	_, err = ca.SignAgentUserCertificate([]byte("not a public key"), "acme", "web")
	assert.Error(t, err)
}
