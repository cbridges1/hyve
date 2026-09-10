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

// ── HostProvider ──────────────────────────────────────────────────────────

func TestHostProvider_RequiresSuperadmin(t *testing.T) {
	p := &HostProvider{
		Clientset:             fake.NewClientset(),
		HostServiceAccountRef: hyvev1alpha1.ServiceAccountRef{Name: "hyve-host-admin", Namespace: testNamespace},
	}
	ctx := contextWithRole(context.Background(), hyvev1alpha1.RoleAdmin)
	_, err := p.Kubeconfig(ctx, &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "host"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "superadmin")
}

func TestHostProvider_SuperadminMintsAgainstHostServiceAccount(t *testing.T) {
	clientset := fake.NewClientset()
	var requestedSA string
	clientset.PrependReactor("create", "serviceaccounts", func(action ktesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(ktesting.CreateActionImpl)
		if !ok || createAction.GetSubresource() != "token" {
			return false, nil, nil
		}
		requestedSA = createAction.Name
		return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{Token: "host-admin-token"}}, nil
	})

	p := &HostProvider{
		Clientset:             clientset,
		CA:                    []byte("fake-ca-data"),
		PublicBaseURL:         "https://hyve-api.example.com",
		HostServiceAccountRef: hyvev1alpha1.ServiceAccountRef{Name: "hyve-host-admin", Namespace: testNamespace},
	}
	ctx := contextWithRole(context.Background(), hyvev1alpha1.RoleSuperadmin)

	kc, err := p.Kubeconfig(ctx, &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "host"}})
	require.NoError(t, err)
	assert.Equal(t, "hyve-host-admin", requestedSA)
	kcStr := string(kc)
	assert.Contains(t, kcStr, "host-admin-token")
	assert.Contains(t, kcStr, "https://hyve-api.example.com/proxy")
}

func TestHostProvider_NoHostServiceAccountConfigured_Errors(t *testing.T) {
	p := &HostProvider{Clientset: fake.NewClientset()}
	ctx := contextWithRole(context.Background(), hyvev1alpha1.RoleSuperadmin)
	_, err := p.Kubeconfig(ctx, &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "host"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no host ServiceAccount configured")
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

// TestHandleKubeconfig_DispatchesToHostProvider confirms a primary-marked
// cluster with no real spec.driver — the common, zero-config host-cluster
// case — is served by HostProvider automatically, with no module involved.
func TestHandleKubeconfig_DispatchesToHostProvider(t *testing.T) {
	host := &recordingProvider{kc: []byte("host-kubeconfig")}
	moduleAuth := &recordingProvider{kc: []byte("module-auth-kubeconfig")}
	hostCD := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "host", Namespace: testNamespace},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Access: hyvev1alpha1.AccessSpec{Method: hyvev1alpha1.AccessMethodPrimary}},
	}
	s := &Server{
		Client:             newFakeClient(t, hostCD),
		Namespace:          testNamespace,
		HostProvider:       host,
		ModuleAuthProvider: moduleAuth,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=host", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, host.called)
	assert.False(t, moduleAuth.called)
	assert.Equal(t, "host-kubeconfig", rec.Body.String())
}

// TestHandleKubeconfig_PrimaryWithRealDriverIsNotServedHere confirms a
// primary-marked cluster that DOES have a real spec.driver (an admin's
// deliberate opt-out of the automatic host path) is NOT intercepted by
// HostProvider — it falls through to the same 409 "use client-side auth
// instead" response as any other driver-having, default-auth cluster (see
// handleAuthContext's own matching carve-out).
func TestHandleKubeconfig_PrimaryWithRealDriverIsNotServedHere(t *testing.T) {
	host := &recordingProvider{kc: []byte("host-kubeconfig")}
	hostCD := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "host", Namespace: testNamespace},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Access: hyvev1alpha1.AccessSpec{Method: hyvev1alpha1.AccessMethodPrimary},
			Driver: hyvev1alpha1.DriverRef{Source: "./modules/civo", Version: "latest"},
		},
	}
	s := &Server{
		Client:       newFakeClient(t, hostCD),
		Namespace:    testNamespace,
		HostProvider: host,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=host", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.False(t, host.called)
}

func TestHandleKubeconfig_DefaultIsClientSideAuthNotServed(t *testing.T) {
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

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.False(t, moduleAuth.called)
	assert.False(t, tunnel.called)
}

func TestHandleKubeconfig_DispatchesToModuleAuthWhenExplicitlyOverridden(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Access.Method = hyvev1alpha1.AccessMethodModuleAuth
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
	cd := newClusterDef("prod")
	cd.Spec.Access.Method = hyvev1alpha1.AccessMethodModuleAuth
	moduleAuth := &recordingProvider{err: fmt.Errorf("boom")}
	s := &Server{
		Client:             newFakeClient(t, cd),
		Namespace:          testNamespace,
		ModuleAuthProvider: moduleAuth,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/kubeconfig?cluster=prod", nil)
	rec := httptest.NewRecorder()
	s.handleKubeconfig(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}
