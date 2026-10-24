package login

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/signin"
)

func TestConfigRoundTrip(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	require.NoError(t, err)
	assert.Empty(t, cfg.Servers)

	exp := time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC)
	cfg.Put(Server{Name: "hyve-example-com", URL: "https://hyve.example.com", Kind: KindHyve, Token: "hs-x:y", ExpiresAt: exp})
	cfg.Current = "hyve-example-com"
	require.NoError(t, cfg.Save())

	st, err := os.Stat(filepath.Join(home, "logins.yaml"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	again, err := Load(home)
	require.NoError(t, err)
	s, err := again.Get("")
	require.NoError(t, err)
	assert.Equal(t, "hs-x:y", s.Token)
	assert.True(t, s.ExpiresAt.Equal(exp))
	assert.True(t, s.SignedIn(exp.Add(-time.Minute)))
	assert.False(t, s.SignedIn(exp))

	assert.Equal(t, "hyve-example-com-2", again.UniqueName("https://hyve.example.com"))
	assert.True(t, again.Remove("hyve-example-com"))
	assert.Empty(t, again.Current)
}

func TestRancherServerRefused(t *testing.T) {
	cfg, err := Load(t.TempDir())
	require.NoError(t, err)
	cfg.Put(Server{Name: "rancher-example-com", URL: "https://rancher.example.com", Kind: "rancher"})
	_, err = cfg.Get("rancher-example-com")
	assert.ErrorContains(t, err, "no longer works with Rancher")
	_, err = cfg.Get("https://rancher.example.com")
	assert.Error(t, err)
	assert.NotNil(t, cfg.Find("rancher-example-com"))
}

func TestNormalizeURL(t *testing.T) {
	u, err := NormalizeURL("hyve.example.com/")
	require.NoError(t, err)
	assert.Equal(t, "https://hyve.example.com", u)
	_, err = NormalizeURL("ftp://x")
	assert.Error(t, err)
}

// hyveHost runs the real sign-in server on a fake cluster. It returns
// alice's password and the bootstrap admin's.
func hyveHost(t *testing.T) (srv *httptest.Server, alicePW, adminPW string) {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, hyvev1alpha1.AddToScheme(s))
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&hyvev1alpha1.User{}).Build()
	store := &signin.Store{Client: c, Namespace: "hyve-system"}
	ctx := context.Background()
	require.NoError(t, store.EnsureBootstrapAdmin(ctx, "bootstrap-secret"))
	require.NoError(t, c.Create(ctx, &hyvev1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}))
	require.NoError(t, store.SetPassword(ctx, "alice", "correct-horse-battery"))

	var sec corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "hyve-system", Name: "bootstrap-secret"}, &sec))

	srv = httptest.NewServer(signin.NewServer(store, nil, "test").Handler())
	t.Cleanup(srv.Close)
	return srv, "correct-horse-battery", string(sec.Data["bootstrapPassword"])
}

func TestAgainstHyveHost(t *testing.T) {
	srv, pw, _ := hyveHost(t)
	ctx := context.Background()
	hc := srv.Client()

	require.NoError(t, Detect(ctx, hc, srv.URL))
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	assert.Error(t, Detect(ctx, other.Client(), other.URL))

	_, err := PasswordLogin(ctx, hc, srv.URL, "alice", "wrong-wrong-wrong", "")
	assert.ErrorIs(t, err, ErrInvalidCredentials)

	sess, err := PasswordLogin(ctx, hc, srv.URL, "alice", pw, "")
	require.NoError(t, err)
	assert.True(t, signin.IsSessionToken(sess.Token))
	assert.False(t, sess.ExpiresAt.IsZero())

	// Change it, then sign out.
	s := &Server{URL: srv.URL, Kind: KindHyve, Username: "alice", Token: sess.Token}
	require.NoError(t, SetPassword(ctx, hc, s, "alice", pw, "brand-new-password"))
	_, err = PasswordLogin(ctx, hc, srv.URL, "alice", "brand-new-password", "")
	require.NoError(t, err)
	require.NoError(t, Logout(ctx, hc, s))
	err = SetPassword(ctx, hc, s, "alice", "brand-new-password", "another-new-password")
	assert.Error(t, err, "the session is gone")
}

func TestMustChangePasswordAgainstHyveHost(t *testing.T) {
	srv, _, adminPW := hyveHost(t)
	ctx := context.Background()
	hc := srv.Client()
	_, err := PasswordLogin(ctx, hc, srv.URL, "admin", adminPW, "")
	assert.ErrorIs(t, err, ErrMustChangePassword)
	_, err = PasswordLogin(ctx, hc, srv.URL, "admin", adminPW, "too-short")
	assert.ErrorContains(t, err, "at least 12")
	sess, err := PasswordLogin(ctx, hc, srv.URL, "admin", adminPW, "the-admins-own-password")
	require.NoError(t, err)
	assert.NotEmpty(t, sess.Token)
}

func TestBrowserLogin(t *testing.T) {
	open := func(u string) error {
		parsed, err := url.Parse(u)
		require.NoError(t, err)
		assert.Equal(t, CLILoginPath, parsed.Path)
		q := parsed.Query()
		der, err := base64.RawURLEncoding.DecodeString(q.Get("publicKey"))
		require.NoError(t, err)
		key, err := x509.ParsePKIXPublicKey(der)
		require.NoError(t, err)
		ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, key.(*rsa.PublicKey), []byte("token-xyz:api-key-secret"), nil)
		require.NoError(t, err)

		cb := "http://127.0.0.1:" + q.Get("port") + "/callback"
		go func() {
			// A request with the wrong state is ignored...
			if resp, err := http.Get(cb + "?state=wrong&token=x"); err == nil {
				resp.Body.Close()
			}
			// ...and the real redirect completes the sign-in.
			v := url.Values{}
			v.Set("state", q.Get("state"))
			v.Set("token", base64.RawURLEncoding.EncodeToString(ct))
			v.Set("expiresAt", "2026-10-17T04:00:00Z")
			if resp, err := http.Get(cb + "?" + v.Encode()); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	sess, err := BrowserLogin(context.Background(), "https://rancher.example.com", open, io.Discard, BrowserOptions{Timeout: 5 * time.Second})
	require.NoError(t, err)
	assert.Equal(t, "token-xyz:api-key-secret", sess.Token)
	assert.Equal(t, time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC), sess.ExpiresAt)
}

func TestBrowserLoginCancelled(t *testing.T) {
	open := func(u string) error {
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		go func() {
			if resp, err := http.Get("http://127.0.0.1:" + q.Get("port") + "/callback?state=" + url.QueryEscape(q.Get("state")) + "&error=cancelled"); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := BrowserLogin(context.Background(), "https://rancher.example.com", open, io.Discard, BrowserOptions{Timeout: 5 * time.Second})
	assert.ErrorContains(t, err, "cancelled")
}

func TestKubeconfigHoldsNoSecret(t *testing.T) {
	s := &Server{Name: "hyve-example-com", URL: "https://hyve.example.com", Kind: KindHyve, Token: "hs-secret:token"}
	data, err := Kubeconfig(s, "c-m-abc", "/usr/local/bin/hyve", "")
	require.NoError(t, err)
	assert.NotContains(t, string(data), "hs-secret")

	cfg, err := clientcmd.Load(data)
	require.NoError(t, err)
	ctx := cfg.Contexts[cfg.CurrentContext]
	assert.Equal(t, "https://hyve.example.com/k8s/clusters/c-m-abc", cfg.Clusters[ctx.Cluster].Server)
	exec := cfg.AuthInfos[ctx.AuthInfo].Exec
	require.NotNil(t, exec)
	assert.Equal(t, "/usr/local/bin/hyve", exec.Command)
	assert.Equal(t, []string{"token", "--server", "hyve-example-com"}, exec.Args)

	local, err := Kubeconfig(s, LocalCluster, "hyve", "/tmp/h")
	require.NoError(t, err)
	assert.Contains(t, string(local), "https://hyve.example.com/k8s/clusters/local")
}

func TestExecCredential(t *testing.T) {
	out, err := ExecCredential("tok", time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.JSONEq(t, `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"tok","expirationTimestamp":"2026-10-17T04:00:00Z"}}`, string(out))
}
