package agentpki

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestBootstrapToken_RoundTrip(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ctx := context.Background()

	token, err := GenerateBootstrapToken(ctx, clientset, testNamespace, "acme", "web")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	ns, name, err := ValidateAndConsumeBootstrapToken(ctx, clientset, testNamespace, token)
	require.NoError(t, err)
	assert.Equal(t, "acme", ns)
	assert.Equal(t, "web", name)
}

func TestBootstrapToken_SingleUse(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ctx := context.Background()

	token, err := GenerateBootstrapToken(ctx, clientset, testNamespace, "acme", "web")
	require.NoError(t, err)

	_, _, err = ValidateAndConsumeBootstrapToken(ctx, clientset, testNamespace, token)
	require.NoError(t, err)

	_, _, err = ValidateAndConsumeBootstrapToken(ctx, clientset, testNamespace, token)
	assert.Error(t, err, "a second use of the same token must fail")
}

func TestBootstrapToken_UnknownTokenRejected(t *testing.T) {
	clientset := k8sfake.NewClientset()
	_, _, err := ValidateAndConsumeBootstrapToken(context.Background(), clientset, testNamespace, "never-issued")
	assert.Error(t, err)
}

func TestBootstrapToken_ExpiredRejectedAndConsumed(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ctx := context.Background()

	token, err := GenerateBootstrapToken(ctx, clientset, testNamespace, "acme", "web")
	require.NoError(t, err)

	// Simulate expiry by rewriting the persisted Secret's expiresAt into
	// the past, rather than waiting out the real 15m TTL in a test.
	name := bootstrapSecretName(token)
	secret, err := clientset.CoreV1().Secrets(testNamespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	secret.Data["expiresAt"] = []byte(time.Now().Add(-time.Minute).Format(time.RFC3339))
	_, err = clientset.CoreV1().Secrets(testNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)

	_, _, err = ValidateAndConsumeBootstrapToken(ctx, clientset, testNamespace, token)
	assert.Error(t, err, "an expired token must be rejected")

	_, getErr := clientset.CoreV1().Secrets(testNamespace).Get(ctx, name, metav1.GetOptions{})
	assert.Error(t, getErr, "an expired token must still be consumed (deleted), not left retryable")
}

func TestBootstrapToken_DifferentTargetsGetDifferentTokens(t *testing.T) {
	clientset := k8sfake.NewClientset()
	ctx := context.Background()

	tokenA, err := GenerateBootstrapToken(ctx, clientset, testNamespace, "acme", "web")
	require.NoError(t, err)
	tokenB, err := GenerateBootstrapToken(ctx, clientset, testNamespace, "acme", "api")
	require.NoError(t, err)
	assert.NotEqual(t, tokenA, tokenB)

	ns, name, err := ValidateAndConsumeBootstrapToken(ctx, clientset, testNamespace, tokenB)
	require.NoError(t, err)
	assert.Equal(t, "acme", ns)
	assert.Equal(t, "api", name)

	// tokenA must still be valid — consuming tokenB must not have
	// affected it.
	_, _, err = ValidateAndConsumeBootstrapToken(ctx, clientset, testNamespace, tokenA)
	assert.NoError(t, err)
}
