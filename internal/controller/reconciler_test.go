package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestSchemeWithCore(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := newTestScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func TestFetchCLISecrets_MissingSecretReturnsNil(t *testing.T) {
	c := clientfake.NewClientBuilder().WithScheme(newTestSchemeWithCore(t)).Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}

	got := r.fetchCLISecrets(context.Background())
	assert.Nil(t, got)
}

func TestFetchCLISecrets_ReturnsSecretDataAsStrings(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: testNamespace},
		Data: map[string][]byte{
			"GITHUB_TOKEN": []byte("ghp_example"),
			"CIVO_TOKEN":   []byte("civo_example"),
		},
	}
	c := clientfake.NewClientBuilder().WithScheme(newTestSchemeWithCore(t)).WithObjects(secret).Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}

	got := r.fetchCLISecrets(context.Background())
	assert.Equal(t, "ghp_example", got["GITHUB_TOKEN"])
	assert.Equal(t, "civo_example", got["CIVO_TOKEN"])
}

func TestFetchCLISecrets_WrongNamespaceIsTreatedAsMissing(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: "other-namespace"},
		Data:       map[string][]byte{"GITHUB_TOKEN": []byte("ghp_example")},
	}
	c := clientfake.NewClientBuilder().WithScheme(newTestSchemeWithCore(t)).WithObjects(secret).Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}

	got := r.fetchCLISecrets(context.Background())
	assert.Nil(t, got)
}
