package api

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// APICredentialsSecretName holds the session-signing key — see
	// LoadSigningKey. An operator creates this Secret once at install
	// time; hyve itself never generates or stores it.
	APICredentialsSecretName = "hyve-api-credentials"
	signingKeyDataKey        = "session-signing-key"

	// userCredentialsSecretSuffix names the Secret paired with a local
	// HyveAccessBinding, holding its bcrypt password hash — never stored
	// inline on the binding itself. Mirrors the <cluster-name>-access-
	// kubeconfig Secret naming convention used elsewhere in this plan.
	userCredentialsSecretSuffix = "-credentials"
	passwordHashDataKey         = "password-hash"
)

// UserCredentialsSecretName returns the paired Secret name for a local
// user's HyveAccessBinding.
func UserCredentialsSecretName(bindingName string) string {
	return bindingName + userCredentialsSecretSuffix
}

// LoadSigningKey reads the session-signing key from the
// hyve-api-credentials Secret in namespace. Create it once with e.g.:
//
//	kubectl create secret generic hyve-api-credentials -n hyve-system \
//	  --from-literal=session-signing-key=$(openssl rand -hex 32)
func LoadSigningKey(ctx context.Context, c client.Client, namespace string) ([]byte, error) {
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: APICredentialsSecretName}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %s/%s not found — create it with a %q key before starting the API (see LoadSigningKey's doc comment)", namespace, APICredentialsSecretName, signingKeyDataKey)
		}
		return nil, fmt.Errorf("get signing key secret: %w", err)
	}
	key, ok := secret.Data[signingKeyDataKey]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("secret %s/%s has no %q key", namespace, APICredentialsSecretName, signingKeyDataKey)
	}
	return key, nil
}

// LoadPasswordHash reads bindingName's paired credentials Secret and
// returns its stored bcrypt hash.
func LoadPasswordHash(ctx context.Context, c client.Client, namespace, bindingName string) (string, error) {
	var secret corev1.Secret
	name := UserCredentialsSecretName(bindingName)
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("credentials secret %s/%s not found", namespace, name)
		}
		return "", fmt.Errorf("get credentials secret: %w", err)
	}
	hash, ok := secret.Data[passwordHashDataKey]
	if !ok || len(hash) == 0 {
		return "", fmt.Errorf("secret %s/%s has no %q key", namespace, name, passwordHashDataKey)
	}
	return string(hash), nil
}
