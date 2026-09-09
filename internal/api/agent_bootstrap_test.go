package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cbridges1/hyve/internal/agentpki"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/kubernetes/fake"
)

func newAgentBootstrapTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAgentBootstrapRoutes(mux)
	return mux
}

func doAgentBootstrapRequest(t *testing.T, s *Server, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/agent/bootstrap", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	newAgentBootstrapTestMux(s).ServeHTTP(rec, req)
	return rec
}

// testAgentAuthorizedKey generates a throwaway Ed25519 keypair and returns
// its public half in OpenSSH authorized_keys format — what a real agent
// would submit as PublicKey in a bootstrap request.
func testAgentAuthorizedKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return string(ssh.MarshalAuthorizedKey(sshPub))
}

func TestHandleAgentBootstrap_NoAgentCA_500(t *testing.T) {
	s := &Server{Namespace: testNamespace, Clientset: fake.NewClientset()}
	rec := doAgentBootstrapRequest(t, s, agentBootstrapRequest{Token: "x", PublicKey: "x"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleAgentBootstrap_MissingFields_400(t *testing.T) {
	clientset := fake.NewClientset()
	ca, err := agentpki.LoadOrCreateCA(context.Background(), clientset, testNamespace)
	require.NoError(t, err)
	s := &Server{Namespace: testNamespace, Clientset: clientset, AgentCA: ca}

	rec := doAgentBootstrapRequest(t, s, agentBootstrapRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleAgentBootstrap_InvalidToken_401(t *testing.T) {
	clientset := fake.NewClientset()
	ca, err := agentpki.LoadOrCreateCA(context.Background(), clientset, testNamespace)
	require.NoError(t, err)
	s := &Server{Namespace: testNamespace, Clientset: clientset, AgentCA: ca}

	rec := doAgentBootstrapRequest(t, s, agentBootstrapRequest{Token: "never-issued", PublicKey: testAgentAuthorizedKey(t)})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleAgentBootstrap_GarbagePublicKey_400(t *testing.T) {
	clientset := fake.NewClientset()
	ca, err := agentpki.LoadOrCreateCA(context.Background(), clientset, testNamespace)
	require.NoError(t, err)
	s := &Server{Namespace: testNamespace, Clientset: clientset, AgentCA: ca}

	token, err := agentpki.GenerateBootstrapToken(context.Background(), clientset, testNamespace, "acme", "web")
	require.NoError(t, err)

	rec := doAgentBootstrapRequest(t, s, agentBootstrapRequest{Token: token, PublicKey: "not a real key"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleAgentBootstrap_ValidTokenIssuesCertificateExactlyOnce is the
// manual-verification scenario from the implementation plan, automated: a
// valid token + public key issues a certificate; a second attempt with
// the same (already-consumed) token fails.
func TestHandleAgentBootstrap_ValidTokenIssuesCertificateExactlyOnce(t *testing.T) {
	clientset := fake.NewClientset()
	ca, err := agentpki.LoadOrCreateCA(context.Background(), clientset, testNamespace)
	require.NoError(t, err)
	s := &Server{Namespace: testNamespace, Clientset: clientset, AgentCA: ca}

	token, err := agentpki.GenerateBootstrapToken(context.Background(), clientset, testNamespace, "acme", "web")
	require.NoError(t, err)
	pubKey := testAgentAuthorizedKey(t)

	rec := doAgentBootstrapRequest(t, s, agentBootstrapRequest{Token: token, PublicKey: pubKey})
	require.Equal(t, http.StatusOK, rec.Code)

	var resp agentBootstrapResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Certificate)

	certPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(resp.Certificate))
	require.NoError(t, err, "response must be a valid authorized_keys line")
	cert, ok := certPub.(*ssh.Certificate)
	require.True(t, ok, "response must actually be a certificate, not a bare key")
	assert.Equal(t, uint32(ssh.UserCert), cert.CertType)
	assert.Equal(t, []string{"acme/web"}, cert.ValidPrincipals)

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(ca.PublicKey().Marshal())
		},
	}
	require.NoError(t, checker.CheckCert("acme/web", cert), "issued certificate must verify against the CA's own public key")

	// Second attempt with the same token must fail — single-use.
	rec2 := doAgentBootstrapRequest(t, s, agentBootstrapRequest{Token: token, PublicKey: pubKey})
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}
