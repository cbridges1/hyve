// Package agent is hyve-agent's own client-side logic (cmd/agent is just
// the thin entrypoint) — see docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md.
// Bootstraps its own SSH identity once (this file), then dials and
// maintains the tunnel to hyve-api (connect.go), sending periodic
// heartbeats over it (heartbeat.go).
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// certSecretName is the Secret hyve-agent persists its bootstrapped
// identity into, in its own namespace — hyve-agent-cert, per the
// implementation plan's own naming (mirrors the control plane's own
// hyve-agent-ca).
const certSecretName = "hyve-agent-cert"

const (
	certSecretKeyPrivate = "private-key"
	certSecretKeyCert    = "certificate"
	certSecretKeyCAKey   = "ca-public-key"
)

// Identity is hyve-agent's own bootstrapped SSH identity — a keypair plus
// the short-lived certificate signed for it — everything connect.go needs
// to dial the control plane. CAPublicKey travels alongside it (persisted
// in the same Secret) since both come from the exact same bootstrap
// exchange and are needed together on every connection: the certificate
// proves the agent's own identity to hyve-api, CAPublicKey is what lets
// the agent verify hyve-api's own host certificate in return.
type Identity struct {
	Signer      ssh.Signer
	Certificate *ssh.Certificate
	CAPublicKey ssh.PublicKey
}

// AuthMethod returns the ssh.AuthMethod connect.go's ClientConfig uses —
// a certificate-carrying signer, not the bare key.
func (id *Identity) AuthMethod() (ssh.AuthMethod, error) {
	certSigner, err := ssh.NewCertSigner(id.Certificate, id.Signer)
	if err != nil {
		return nil, fmt.Errorf("build certificate signer: %w", err)
	}
	return ssh.PublicKeys(certSigner), nil
}

// LoadOrBootstrapIdentity loads a previously-persisted identity from
// namespace's hyve-agent-cert Secret if one exists; otherwise it
// generates a fresh keypair, exchanges bootstrapToken for a signed
// certificate against controlPlaneURL's own POST /agent/bootstrap (see
// internal/api/agent_bootstrap.go), and persists the result so a pod
// restart doesn't need to re-bootstrap (and, since the token is single-
// use, couldn't anyway).
func LoadOrBootstrapIdentity(ctx context.Context, clientset kubernetes.Interface, namespace, controlPlaneURL, bootstrapToken string) (*Identity, error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, certSecretName, metav1.GetOptions{})
	if err == nil {
		return identityFromSecretData(secret.Data)
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get %s/%s: %w", namespace, certSecretName, err)
	}

	if bootstrapToken == "" {
		return nil, fmt.Errorf("no persisted identity found in %s/%s and no bootstrap token was provided", namespace, certSecretName)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate agent keypair: %w", err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		return nil, fmt.Errorf("build agent signer: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("wrap agent public key: %w", err)
	}

	certLine, caKeyLine, err := requestCertificate(ctx, controlPlaneURL, bootstrapToken, ssh.MarshalAuthorizedKey(sshPub))
	if err != nil {
		return nil, err
	}
	certPub, _, _, _, err := ssh.ParseAuthorizedKey(certLine)
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}
	cert, ok := certPub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("control plane returned a bare key, not a certificate")
	}
	caPub, _, _, _, err := ssh.ParseAuthorizedKey(caKeyLine)
	if err != nil {
		return nil, fmt.Errorf("parse CA public key: %w", err)
	}

	privPEM, err := marshalPrivateKeyPEM(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal agent private key: %w", err)
	}

	toCreate := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: certSecretName, Namespace: namespace},
		Data: map[string][]byte{
			certSecretKeyPrivate: privPEM,
			certSecretKeyCert:    certLine,
			certSecretKeyCAKey:   caKeyLine,
		},
	}
	if _, err := clientset.CoreV1().Secrets(namespace).Create(ctx, toCreate, metav1.CreateOptions{}); err != nil {
		// Not a losing-a-race case the way agentpki.LoadOrCreateCA's
		// equivalent is: hyve-agent runs as a single replica per cluster
		// (unlike hyve-api), so an AlreadyExists here means a previous
		// bootstrap actually succeeded and this run's own bootstrap token
		// has already been consumed for nothing — surface that plainly
		// rather than silently reading back a possibly-unrelated cert.
		return nil, fmt.Errorf("persist bootstrapped identity: %w", err)
	}

	return &Identity{Signer: signer, Certificate: cert, CAPublicKey: caPub}, nil
}

func identityFromSecretData(data map[string][]byte) (*Identity, error) {
	privPEM := data[certSecretKeyPrivate]
	certLine := data[certSecretKeyCert]
	caKeyLine := data[certSecretKeyCAKey]
	if len(privPEM) == 0 || len(certLine) == 0 || len(caKeyLine) == 0 {
		return nil, fmt.Errorf("secret %s is missing %q, %q, or %q", certSecretName, certSecretKeyPrivate, certSecretKeyCert, certSecretKeyCAKey)
	}
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		return nil, fmt.Errorf("parse persisted private key: %w", err)
	}
	certPub, _, _, _, err := ssh.ParseAuthorizedKey(certLine)
	if err != nil {
		return nil, fmt.Errorf("parse persisted certificate: %w", err)
	}
	cert, ok := certPub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("persisted %q is a bare key, not a certificate", certSecretKeyCert)
	}
	caPub, _, _, _, err := ssh.ParseAuthorizedKey(caKeyLine)
	if err != nil {
		return nil, fmt.Errorf("parse persisted CA public key: %w", err)
	}
	return &Identity{Signer: signer, Certificate: cert, CAPublicKey: caPub}, nil
}

// requestCertificate is the one HTTP call this package makes — everything
// else is SSH. Deliberately doesn't retry: a bootstrap token is single-use,
// so a retry after any response (success or failure) would either resend
// an already-consumed token pointlessly or duplicate a successful signing
// request the caller already got a certificate back from. Returns the CA's
// own public key alongside the certificate — see agentBootstrapResponse's
// own doc comment in internal/api/agent_bootstrap.go for why folding it
// into this same response doesn't need a second, separate distribution
// mechanism.
func requestCertificate(ctx context.Context, controlPlaneURL, bootstrapToken string, publicKeyAuthorized []byte) (certificate, caPublicKey []byte, err error) {
	body, err := json.Marshal(map[string]string{
		"token":     bootstrapToken,
		"publicKey": string(publicKeyAuthorized),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal bootstrap request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlPlaneURL+"/agent/bootstrap", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build bootstrap request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("call %s/agent/bootstrap: %w", controlPlaneURL, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, nil, fmt.Errorf("read bootstrap response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("bootstrap request failed: %s: %s", resp.Status, string(respBody))
	}

	var parsed struct {
		Certificate string `json:"certificate"`
		CAPublicKey string `json:"caPublicKey"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, nil, fmt.Errorf("parse bootstrap response: %w", err)
	}
	return []byte(parsed.Certificate), []byte(parsed.CAPublicKey), nil
}

func marshalPrivateKeyPEM(priv ed25519.PrivateKey) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(priv, "hyve-agent")
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}
