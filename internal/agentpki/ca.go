// Package agentpki is hyve's own small internal SSH certificate
// authority for hyve-agent — see docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md's
// "Agent identity / authentication". One Ed25519 keypair signs both the
// short-lived user certificates hyve-agent presents when it dials in and
// hyve-api's own host certificate, so agent and control plane verify each
// other off the exact same root — the SSH analogue of mutual TLS, native
// to the transport actually in use (see "Tunnel protocol" in that same
// doc for why SSH, not a TLS-carried transport).
package agentpki

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// caSecretName is the Secret this CA's keypair is persisted under, in the
// control-plane namespace (hyve-system by convention) — hyve-agent-ca,
// mirroring the naming convention hyve-agent-ca/hyve-agent-cert already
// established for its own agent-side counterpart in the implementation
// plan.
const caSecretName = "hyve-agent-ca"

const caSecretDataKey = "ca.key"

// CA is hyve's own internal SSH certificate authority. Loaded once at
// API/controller startup (mirrors how the API pod already reads its own
// in-cluster CA once at startup for /proxy — see cmd/api/run.go) and held
// for the process's lifetime — never regenerated automatically, since that
// would invalidate every already-issued certificate out from under
// whatever's still using it.
type CA struct {
	signer ssh.Signer
}

// PublicKey is this CA's own public key — what a verifier (CertChecker's
// IsUserAuthority/IsHostAuthority) checks a presented certificate's
// SignatureKey against.
func (c *CA) PublicKey() ssh.PublicKey {
	return c.signer.PublicKey()
}

// LoadOrCreateCA reads the CA keypair from a Secret in namespace
// (hyve-agent-ca), generating and persisting a fresh Ed25519 keypair if
// none exists yet. Safe to call concurrently from multiple pods racing to
// bootstrap the same install: a losing Create falls back to reading what
// the winner wrote, the same idiom internal/api/organizations.go's
// ensureNamespace/ensureAccessRoleScaffolding already use for their own
// check-then-create sequences.
func LoadOrCreateCA(ctx context.Context, clientset kubernetes.Interface, namespace string) (*CA, error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, caSecretName, metav1.GetOptions{})
	if err == nil {
		return caFromSecretData(secret.Data[caSecretDataKey])
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get %s/%s: %w", namespace, caSecretName, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA keypair: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "hyve-agent-ca")
	if err != nil {
		return nil, fmt.Errorf("marshal CA private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(block)

	created := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: caSecretName, Namespace: namespace},
		Data:       map[string][]byte{caSecretDataKey: pemBytes},
	}
	if _, err := clientset.CoreV1().Secrets(namespace).Create(ctx, created, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Lost the race to another pod bootstrapping the same install
			// at the same time — read back what it wrote instead of
			// erroring, same reasoning ensureNamespace's own doc comment
			// gives for its identical pattern.
			existing, getErr := clientset.CoreV1().Secrets(namespace).Get(ctx, caSecretName, metav1.GetOptions{})
			if getErr != nil {
				return nil, fmt.Errorf("get %s/%s after losing create race: %w", namespace, caSecretName, getErr)
			}
			return caFromSecretData(existing.Data[caSecretDataKey])
		}
		return nil, fmt.Errorf("create %s/%s: %w", namespace, caSecretName, err)
	}

	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		return nil, fmt.Errorf("build CA signer: %w", err)
	}
	return &CA{signer: signer}, nil
}

func caFromSecretData(pemBytes []byte) (*CA, error) {
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("secret %s has no %q key", caSecretName, caSecretDataKey)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA private key: %w", err)
	}
	return &CA{signer: signer}, nil
}

// signCertificate is the shared low-level primitive both SignHostCertificate
// and (in sign.go) SignAgentUserCertificate build on — fills in the fields
// every certificate this CA issues needs regardless of type, then signs.
func (c *CA) signCertificate(pub ssh.PublicKey, certType uint32, principals []string, keyID string, ttl time.Duration) (*ssh.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).SetUint64(^uint64(0)))
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	now := time.Now()
	cert := &ssh.Certificate{
		Key:             pub,
		Serial:          serial.Uint64(),
		CertType:        certType,
		KeyId:           keyID,
		ValidPrincipals: principals,
		// ValidAfter is backdated slightly to tolerate clock skew between
		// this process and whichever one first uses the certificate — the
		// same reasoning any short-lived-token system needs.
		ValidAfter:  uint64(now.Add(-5 * time.Minute).Unix()),
		ValidBefore: uint64(now.Add(ttl).Unix()),
	}
	if err := cert.SignCert(rand.Reader, c.signer); err != nil {
		return nil, fmt.Errorf("sign certificate: %w", err)
	}
	return cert, nil
}

// SignHostCertificate signs hostPublicKey (hyve-api's own tunnel-listener
// host key, generated by milestone 3) into a short-lived host certificate
// for the given addresses — what lets an agent's own ssh.Dial verify it's
// really talking to hyve-api via ssh.CertChecker.CheckHostKey, off this
// same CA, per "Agent identity / authentication"'s mutual-verification
// design. ttl is deliberately the caller's choice, not a package constant:
// a host certificate's rotation cadence is a control-plane operational
// concern (milestone 3's own listener startup), not something this
// low-level signing primitive should hardcode an opinion about.
func (c *CA) SignHostCertificate(hostPublicKey ssh.PublicKey, addresses []string, ttl time.Duration) (*ssh.Certificate, error) {
	return c.signCertificate(hostPublicKey, ssh.HostCert, addresses, "hyve-api", ttl)
}
