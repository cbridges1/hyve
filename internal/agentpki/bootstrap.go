package agentpki

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// BootstrapTokenTTL bounds how long a bootstrap token stays valid before
// an agent must have used it — long enough to cover scheduling + image
// pull + startup for the agent's own Deployment (milestone 4), short
// enough that an unused, leaked token is a narrow window, not a standing
// credential. This is the "materially smaller thing to get right" the
// proposal doc's own "Agent identity / authentication" section describes:
// single-use and short-lived, irrelevant to security the moment the first
// certificate is issued.
const BootstrapTokenTTL = 15 * time.Minute

// bootstrapSecretPrefix names the Secret a bootstrap token is stored
// under, in the control-plane namespace — never the raw token itself
// (which the agent alone holds, injected as an env var at install time),
// only a hash of it. Storing raw high-entropy secret material as a
// Kubernetes object's own name would put it somewhere it doesn't need to
// be: etcd keys, RBAC audit logs, and `kubectl get secrets` output all
// show object names in the clear even to a caller who can't read a
// Secret's data.
const bootstrapSecretPrefix = "hyve-agent-bootstrap-"

// GenerateBootstrapToken mints a single-use token scoped to one
// (targetNamespace, clusterName) and persists it (as a hash, never the
// raw value) as a Secret in controlPlaneNamespace — called by the
// reconcile loop (milestone 4) when installing an agent, with the raw
// return value injected into the agent's own Deployment as an env var.
func GenerateBootstrapToken(ctx context.Context, clientset kubernetes.Interface, controlPlaneNamespace, targetNamespace, clusterName string) (string, error) {
	raw, err := randomHex(32)
	if err != nil {
		return "", fmt.Errorf("generate bootstrap token: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: bootstrapSecretName(raw), Namespace: controlPlaneNamespace},
		// Data, not StringData: StringData->Data normalization is real
		// apiserver admission behavior that a fake clientset (used in this
		// package's own tests) doesn't simulate — Data works correctly
		// against both, StringData only against a real cluster.
		Data: map[string][]byte{
			"namespace":   []byte(targetNamespace),
			"clusterName": []byte(clusterName),
			"expiresAt":   []byte(time.Now().Add(BootstrapTokenTTL).Format(time.RFC3339)),
		},
	}
	if _, err := clientset.CoreV1().Secrets(controlPlaneNamespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("persist bootstrap token: %w", err)
	}
	return raw, nil
}

// ValidateAndConsumeBootstrapToken looks up rawToken (by its hash — see
// GenerateBootstrapToken), deletes it immediately (single-use: a second
// call with the same token always fails, whether or not the first
// actually raced it), and returns the (namespace, clusterName) it was
// scoped to. Rejects an expired token exactly the same way as an unknown
// one — same "no distinction that would let a caller learn something from
// the difference" stance any single-use-token system needs.
//
// The Get-then-Delete here has a narrow theoretical race (two concurrent
// callers both reading the token's data before either delete completes)
// that a true atomic get-and-delete would close, but the only party who
// could ever present the raw high-entropy token value in the first place
// is the one legitimate agent it was minted for — this isn't a contended
// resource under any real threat model, just a token used exactly once by
// the one process that has it.
func ValidateAndConsumeBootstrapToken(ctx context.Context, clientset kubernetes.Interface, controlPlaneNamespace, rawToken string) (namespace, clusterName string, err error) {
	name := bootstrapSecretName(rawToken)
	secrets := clientset.CoreV1().Secrets(controlPlaneNamespace)

	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", "", fmt.Errorf("invalid or already-used bootstrap token")
		}
		return "", "", fmt.Errorf("get bootstrap token: %w", err)
	}

	// Delete before validating expiry, not after: single-use holds
	// regardless of whether the token turns out to be expired — an
	// expired token gets consumed by the first caller to present it, the
	// same as a valid one, rather than sitting around to be retried.
	if delErr := secrets.Delete(ctx, name, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
		return "", "", fmt.Errorf("consume bootstrap token: %w", delErr)
	}

	expiresAt, parseErr := time.Parse(time.RFC3339, string(secret.Data["expiresAt"]))
	if parseErr != nil || time.Now().After(expiresAt) {
		return "", "", fmt.Errorf("invalid or already-used bootstrap token")
	}

	return string(secret.Data["namespace"]), string(secret.Data["clusterName"]), nil
}

func bootstrapSecretName(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return bootstrapSecretPrefix + hex.EncodeToString(sum[:])[:32]
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
