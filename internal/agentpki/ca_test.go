package agentpki

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

const testNamespace = "hyve-system"

func TestLoadOrCreateCA_GeneratesAndPersists(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ctx := context.Background()

	ca, err := LoadOrCreateCA(ctx, clientset, testNamespace)
	require.NoError(t, err)
	require.NotNil(t, ca.PublicKey())

	secret, err := clientset.CoreV1().Secrets(testNamespace).Get(ctx, caSecretName, metav1.GetOptions{})
	require.NoError(t, err, "the generated CA keypair must be persisted as a Secret")
	assert.NotEmpty(t, secret.Data[caSecretDataKey])
}

func TestLoadOrCreateCA_LoadsExisting(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ctx := context.Background()

	first, err := LoadOrCreateCA(ctx, clientset, testNamespace)
	require.NoError(t, err)

	second, err := LoadOrCreateCA(ctx, clientset, testNamespace)
	require.NoError(t, err)

	assert.Equal(t, first.PublicKey().Marshal(), second.PublicKey().Marshal(),
		"a second load must return the same CA identity, not generate a fresh one")
}

func TestSignHostCertificate_ProducesVerifiableCert(t *testing.T) {
	ca, err := LoadOrCreateCA(context.Background(), k8sfake.NewClientset(), testNamespace)
	require.NoError(t, err)

	hostPub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshHostPub, err := ssh.NewPublicKey(hostPub)
	require.NoError(t, err)

	// ValidPrincipals holds the bare hostname; ssh.CertChecker.CheckHostKey
	// itself requires its own addr argument in host:port form
	// (net.SplitHostPort internally) but checks the certificate's
	// principals against just the host part — matches how it'll actually
	// be called against milestone 3's real listener address.
	cert, err := ca.SignHostCertificate(sshHostPub, []string{"hyve-api"}, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, uint32(ssh.HostCert), cert.CertType)

	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, address string) bool {
			return string(auth.Marshal()) == string(ca.PublicKey().Marshal())
		},
	}
	require.NoError(t, checker.CheckHostKey("hyve-api:2222", nil, cert))
}
