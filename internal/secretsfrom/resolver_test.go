package secretsfrom

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestResolveWithClient_Success(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "harbor-registry-credentials", Namespace: "harbor"},
		Data: map[string][]byte{
			"username": []byte("robot$hyve"),
			"password": []byte("s3cr3t"),
		},
	})

	src := SecretSource{
		Cluster:   "main",
		Namespace: "harbor",
		SecretRef: "harbor-registry-credentials",
		Keys: []SecretKeyMap{
			{Key: "username", Env: "HARBOR_USERNAME"},
			{Key: "password", Env: "HARBOR_PASSWORD"},
		},
	}

	resolved, err := resolveWithClient(context.Background(), client, src)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"HARBOR_USERNAME": "robot$hyve",
		"HARBOR_PASSWORD": "s3cr3t",
	}, resolved)
}

func TestResolveWithClient_EnvDefaultsToKey(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Data:       map[string][]byte{"token": []byte("abc123")},
	})

	src := SecretSource{
		Cluster: "main", Namespace: "ns", SecretRef: "creds",
		Keys: []SecretKeyMap{{Key: "token"}}, // no Env override
	}

	resolved, err := resolveWithClient(context.Background(), client, src)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"token": "abc123"}, resolved)
}

func TestResolveWithClient_StringDataFallback(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		StringData: map[string]string{"token": "abc123"},
	})

	src := SecretSource{
		Cluster: "main", Namespace: "ns", SecretRef: "creds",
		Keys: []SecretKeyMap{{Key: "token", Env: "TOKEN"}},
	}

	resolved, err := resolveWithClient(context.Background(), client, src)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"TOKEN": "abc123"}, resolved)
}

func TestResolveWithClient_SecretNotFound(t *testing.T) {
	client := fake.NewClientset()

	src := SecretSource{
		Cluster: "main", Namespace: "ns", SecretRef: "missing",
		Keys: []SecretKeyMap{{Key: "token"}},
	}

	_, err := resolveWithClient(context.Background(), client, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ns/missing")
}

func TestResolveWithClient_MissingKey(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Data:       map[string][]byte{"username": []byte("u")},
	})

	src := SecretSource{
		Cluster: "main", Namespace: "ns", SecretRef: "creds",
		Keys: []SecretKeyMap{{Key: "username"}, {Key: "password"}},
	}

	_, err := resolveWithClient(context.Background(), client, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password")
}

func TestResolveWithClient_RBACDenied(t *testing.T) {
	client := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Data:       map[string][]byte{"token": []byte("abc123")},
	})
	client.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "creds", fmt.Errorf("access denied"))
	})

	src := SecretSource{
		Cluster: "main", Namespace: "ns", SecretRef: "creds",
		Keys: []SecretKeyMap{{Key: "token"}},
	}

	_, err := resolveWithClient(context.Background(), client, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestValidate_RequiresAllFields(t *testing.T) {
	base := SecretSource{
		Cluster: "main", Namespace: "ns", SecretRef: "creds",
		Keys: []SecretKeyMap{{Key: "token"}},
	}

	t.Run("valid passes", func(t *testing.T) {
		assert.NoError(t, validate(base))
	})
	t.Run("missing cluster", func(t *testing.T) {
		s := base
		s.Cluster = ""
		assert.ErrorContains(t, validate(s), "cluster")
	})
	t.Run("missing namespace", func(t *testing.T) {
		s := base
		s.Namespace = ""
		assert.ErrorContains(t, validate(s), "namespace")
	})
	t.Run("missing secretRef", func(t *testing.T) {
		s := base
		s.SecretRef = ""
		assert.ErrorContains(t, validate(s), "secretRef")
	})
	t.Run("missing keys", func(t *testing.T) {
		s := base
		s.Keys = nil
		assert.ErrorContains(t, validate(s), "keys")
	})
}

func TestResolve_LocatorError(t *testing.T) {
	locate := func(cluster string) (string, error) {
		return "", fmt.Errorf("cluster %q not found", cluster)
	}
	src := SecretSource{Cluster: "missing-cluster", Namespace: "ns", SecretRef: "creds", Keys: []SecretKeyMap{{Key: "token"}}}

	_, err := Resolve(context.Background(), locate, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-cluster")
}

func TestResolve_NoKubeconfigFileYet(t *testing.T) {
	locate := func(cluster string) (string, error) {
		return "/nonexistent/path/kubeconfig.yaml", nil
	}
	src := SecretSource{Cluster: "not-authed-yet", Namespace: "ns", SecretRef: "creds", Keys: []SecretKeyMap{{Key: "token"}}}

	_, err := Resolve(context.Background(), locate, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no kubeconfig yet")
}
