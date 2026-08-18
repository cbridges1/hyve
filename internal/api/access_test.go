package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// ── TunnelProvider ──────────────────────────────────────────────────────

func TestTunnelProvider_SecretExists(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-access-kubeconfig", Namespace: testNamespace},
		Data:       map[string][]byte{"kubeconfig": []byte("apiVersion: v1\nkind: Config\n")},
	}
	p := &TunnelProvider{Client: newFakeClient(t, secret), Namespace: testNamespace}

	kc, err := p.Kubeconfig(context.Background(), &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "prod"}})
	require.NoError(t, err)
	assert.Contains(t, string(kc), "kind: Config")
}

func TestTunnelProvider_SecretMissing(t *testing.T) {
	p := &TunnelProvider{Client: newFakeClient(t), Namespace: testNamespace}

	_, err := p.Kubeconfig(context.Background(), &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "prod"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prod-access-kubeconfig")
}

// ── ModuleAuthProvider ──────────────────────────────────────────────────

func writeFakeAuthOnlyModule(t *testing.T, modulesDir string) {
	t.Helper()
	dir := filepath.Join(modulesDir, "modules", "fake-driver")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.yaml"), []byte(`apiVersion: v1
kind: Module
metadata:
  name: fake-driver
  version: 0.1.0
  type: authOnly
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.yaml"), []byte(`apiVersion: v1
kind: ClusterAuth
metadata:
  name: fake-driver
spec:
  methods:
    - name: default
      auth:
        script: "echo cluster=$HYVE_CLUSTER_NAME > \"$KUBECONFIG\""
      exports: KUBECONFIG
`), 0644))
}

func TestModuleAuthProvider_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	modulesDir := t.TempDir()
	writeFakeAuthOnlyModule(t, modulesDir)

	p := &ModuleAuthProvider{ModulesDir: modulesDir}
	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "module-auth-test-cluster"},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/fake-driver", Version: "local"}},
	}

	kc, err := p.Kubeconfig(context.Background(), cd)
	require.NoError(t, err)
	assert.Contains(t, string(kc), "cluster=module-auth-test-cluster")
}

func TestModuleAuthProvider_ModuleNotFound(t *testing.T) {
	p := &ModuleAuthProvider{ModulesDir: t.TempDir()}
	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/does-not-exist", Version: "local"}},
	}

	_, err := p.Kubeconfig(context.Background(), cd)
	assert.Error(t, err)
}

// ── PrimaryClusterProvider ──────────────────────────────────────────────

func TestPrimaryClusterProvider_MintsTokenForResolvedServiceAccount(t *testing.T) {
	clientset := fake.NewClientset()
	clientset.PrependReactor("create", "serviceaccounts", func(action ktesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(ktesting.CreateActionImpl)
		if !ok || createAction.GetSubresource() != "token" {
			return false, nil, nil
		}
		return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{Token: "fake-minted-token"}}, nil
	})

	p := &PrimaryClusterProvider{
		Clientset:     clientset,
		CA:            []byte("fake-ca-data"),
		PublicBaseURL: "https://hyve-api.example.com",
	}

	ctx := context.WithValue(context.Background(), contextKeyServiceAccountRef, hyvev1alpha1.ServiceAccountRef{Name: "hyve-access-admin", Namespace: "hyve-system"})
	kc, err := p.Kubeconfig(ctx, &hyvev1alpha1.ClusterDefinition{})
	require.NoError(t, err)

	kcStr := string(kc)
	assert.Contains(t, kcStr, "fake-minted-token")
	assert.Contains(t, kcStr, "https://hyve-api.example.com/proxy")
}

func TestPrimaryClusterProvider_NoServiceAccountInContext_Errors(t *testing.T) {
	p := &PrimaryClusterProvider{Clientset: fake.NewClientset()}
	_, err := p.Kubeconfig(context.Background(), &hyvev1alpha1.ClusterDefinition{})
	assert.Error(t, err)
}

// ── handleKubeconfig dispatch ────────────────────────────────────────────

// recordingProvider is an AccessProvider test double that records whether
// it was invoked — used to prove handleKubeconfig dispatches to the right
// provider without needing any of the three real providers' actual
// network/filesystem dependencies.
type recordingProvider struct {
	called bool
	kc     []byte
	err    error
}

func (r *recordingProvider) Kubeconfig(context.Context, *hyvev1alpha1.ClusterDefinition) ([]byte, error) {
	r.called = true
	return r.kc, r.err
}

func TestHandleKubeconfig_MissingClusterParam(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleKubeconfig_UnknownCluster(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=missing", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleKubeconfig_DispatchesToPrimaryProvider(t *testing.T) {
	primary := &recordingProvider{kc: []byte("primary-kubeconfig")}
	moduleAuth := &recordingProvider{kc: []byte("module-auth-kubeconfig")}
	s := &Server{
		Client:             newFakeClient(t),
		Namespace:          testNamespace,
		PrimaryClusterName: "primary",
		PrimaryProvider:    primary,
		ModuleAuthProvider: moduleAuth,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=primary", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, primary.called)
	assert.False(t, moduleAuth.called)
	assert.Equal(t, "primary-kubeconfig", rec.Body.String())
}

func TestHandleKubeconfig_DispatchesToModuleAuthByDefault(t *testing.T) {
	moduleAuth := &recordingProvider{kc: []byte("module-auth-kubeconfig")}
	tunnel := &recordingProvider{kc: []byte("tunnel-kubeconfig")}
	s := &Server{
		Client:             newFakeClient(t, newClusterDef("prod")),
		Namespace:          testNamespace,
		ModuleAuthProvider: moduleAuth,
		TunnelProvider:     tunnel,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=prod", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, moduleAuth.called)
	assert.False(t, tunnel.called)
}

func TestHandleKubeconfig_DispatchesToTunnelWhenConfigured(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Access.Method = hyvev1alpha1.AccessMethodTunnel
	moduleAuth := &recordingProvider{kc: []byte("module-auth-kubeconfig")}
	tunnel := &recordingProvider{kc: []byte("tunnel-kubeconfig")}
	s := &Server{
		Client:             newFakeClient(t, cd),
		Namespace:          testNamespace,
		ModuleAuthProvider: moduleAuth,
		TunnelProvider:     tunnel,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=prod", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, tunnel.called)
	assert.False(t, moduleAuth.called)
}

func TestHandleKubeconfig_ProviderErrorSurfacesAsBadGateway(t *testing.T) {
	moduleAuth := &recordingProvider{err: fmt.Errorf("boom")}
	s := &Server{
		Client:             newFakeClient(t, newClusterDef("prod")),
		Namespace:          testNamespace,
		ModuleAuthProvider: moduleAuth,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=prod", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}
