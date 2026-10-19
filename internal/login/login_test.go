package login

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestNormalizeURL(t *testing.T) {
	u, err := NormalizeURL("hyve.example.com/")
	require.NoError(t, err)
	assert.Equal(t, "https://hyve.example.com", u)
	_, err = NormalizeURL("ftp://x")
	assert.Error(t, err)
}

func TestFlowFor(t *testing.T) {
	assert.Equal(t, FlowPassword, FlowFor("localProvider"))
	assert.Equal(t, FlowPassword, FlowFor("activeDirectoryProvider"))
	assert.Equal(t, FlowSAML, FlowFor("oktaProvider"))
	assert.Equal(t, FlowOAuth, FlowFor("azureADProvider"))
	assert.Equal(t, FlowBrowser, FlowFor("githubProvider"))
	assert.Equal(t, FlowBrowser, FlowFor("genericOIDCProvider"))
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

	kind, err := Detect(ctx, hc, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, KindHyve, kind)

	providers, err := ListProviders(ctx, hc, srv.URL)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	assert.Equal(t, FlowPassword, FlowFor(providers[0].Type))

	_, err = PasswordLogin(ctx, hc, srv.URL, "localProvider", "alice", "wrong-wrong-wrong", "")
	assert.ErrorIs(t, err, ErrInvalidCredentials)

	sess, err := PasswordLogin(ctx, hc, srv.URL, "localProvider", "alice", pw, "")
	require.NoError(t, err)
	assert.True(t, signin.IsSessionToken(sess.Token))
	assert.False(t, sess.ExpiresAt.IsZero())

	// Change it, then sign out.
	s := &Server{URL: srv.URL, Kind: KindHyve, Username: "alice", Token: sess.Token}
	require.NoError(t, SetPassword(ctx, hc, s, "alice", pw, "brand-new-password"))
	_, err = PasswordLogin(ctx, hc, srv.URL, "localProvider", "alice", "brand-new-password", "")
	require.NoError(t, err)
	require.NoError(t, Logout(ctx, hc, s))
	err = SetPassword(ctx, hc, s, "alice", "brand-new-password", "another-new-password")
	assert.Error(t, err, "the session is gone")
}

func TestMustChangePasswordAgainstHyveHost(t *testing.T) {
	srv, _, adminPW := hyveHost(t)
	ctx := context.Background()
	hc := srv.Client()
	_, err := PasswordLogin(ctx, hc, srv.URL, "localProvider", "admin", adminPW, "")
	assert.ErrorIs(t, err, ErrMustChangePassword)
	_, err = PasswordLogin(ctx, hc, srv.URL, "localProvider", "admin", adminPW, "too-short")
	assert.ErrorContains(t, err, "at least 12")
	sess, err := PasswordLogin(ctx, hc, srv.URL, "localProvider", "admin", adminPW, "the-admins-own-password")
	require.NoError(t, err)
	assert.NotEmpty(t, sess.Token)
}

// fakeRancher serves the bits of Rancher's /v1-public API the client uses.
type fakeRancher struct {
	*httptest.Server
	providers string
	logins    []map[string]any
	authToken atomic.Value // string: base64 ciphertext, once "stored"
	deleted   atomic.Bool
	polls     atomic.Int32
}

func newFakeRancher(t *testing.T, providers string) *fakeRancher {
	t.Helper()
	f := &fakeRancher{providers: providers}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1-public/hyve", http.NotFound)
	mux.HandleFunc("GET /v1-public/authproviders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, f.providers)
	})
	mux.HandleFunc("POST /v1-public/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.logins = append(f.logins, body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"token":"kubeconfig-u-abc:secret","expiresAt":"2026-11-15T00:00:00Z","userId":"u-abc"}`)
	})
	mux.HandleFunc("GET /v1-public/authtokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.polls.Add(1)
		tok, _ := f.authToken.Load().(string)
		if tok == "" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": tok, "expiresAt": "2026-11-15T00:00:00Z", "userId": "u-saml"})
	})
	mux.HandleFunc("DELETE /v1-public/authtokens/{id}", func(w http.ResponseWriter, _ *http.Request) {
		f.deleted.Store(true)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func TestDetectRancher(t *testing.T) {
	r := newFakeRancher(t, `{"data":[{"id":"local","type":"localProvider"},{"id":"github","type":"githubProvider"},{"id":"openldap","type":"openLdapProvider"}]}`)
	ctx := context.Background()
	kind, err := Detect(ctx, r.Client(), r.URL)
	require.NoError(t, err)
	assert.Equal(t, KindRancher, kind)

	providers, err := ListProviders(ctx, r.Client(), r.URL)
	require.NoError(t, err)
	require.Len(t, providers, 3)
	assert.Equal(t, "localProvider", providers[0].Type, "local first")

	sess, err := PasswordLogin(ctx, r.Client(), r.URL, "openLdapProvider", "bob", "pw", "")
	require.NoError(t, err)
	assert.Equal(t, "kubeconfig-u-abc:secret", sess.Token)
	assert.Equal(t, "u-abc", sess.UserID)
	assert.Equal(t, "openLdapProvider", r.logins[0]["type"])
	assert.Equal(t, "kubeconfig", r.logins[0]["responseType"])
}

func TestSAMLLogin(t *testing.T) {
	r := newFakeRancher(t, `{"data":[{"type":"oktaProvider"}]}`)
	var opened string
	open := func(u string) error {
		opened = u
		parsed, err := url.Parse(u)
		require.NoError(t, err)
		q := parsed.Query()
		assert.Equal(t, "/dashboard/auth/login", parsed.Path)
		assert.Equal(t, "true", q.Get("cli"))
		assert.Len(t, q.Get("requestId"), 32)
		assert.Equal(t, "kubeconfig", q.Get("responseType"))

		// Do what Rancher does after the IdP returns: encrypt the token
		// with the CLI's key and store it under the request ID.
		raw, err := base64.StdEncoding.DecodeString(q.Get("publicKey"))
		require.NoError(t, err)
		var pub rsa.PublicKey
		require.NoError(t, json.Unmarshal(raw, &pub))
		go func() {
			time.Sleep(30 * time.Millisecond) // the first poll finds nothing
			ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &pub, []byte("kubeconfig-u-saml:secret"), nil)
			if err == nil {
				r.authToken.Store(base64.StdEncoding.EncodeToString(ct))
			}
		}()
		return nil
	}
	sess, err := SAMLLogin(context.Background(), r.Client(), r.URL, open, io.Discard, SAMLOptions{PollInterval: 10 * time.Millisecond, Timeout: 5 * time.Second})
	require.NoError(t, err)
	assert.NotEmpty(t, opened)
	assert.Equal(t, "kubeconfig-u-saml:secret", sess.Token)
	assert.Equal(t, "u-saml", sess.UserID)
	assert.True(t, r.deleted.Load(), "the stored token is deleted")
	assert.Greater(t, r.polls.Load(), int32(1))
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

func TestOAuthDeviceCode(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device":
			_, _ = io.WriteString(w, `{"device_code":"dc","user_code":"ABCD-EFGH","verification_uri":"https://microsoft.com/devicelogin","expires_in":300,"interval":1}`)
		case "/token":
			_, _ = io.WriteString(w, `{"access_token":"at","token_type":"Bearer","id_token":"the-id-token","expires_in":3600}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	r := newFakeRancher(t, `{"data":[]}`)
	p := Provider{Type: "azureADProvider", ClientID: "cid", Scopes: []string{"openid"}, DeviceAuthURL: idp.URL + "/device", TokenURL: idp.URL + "/token"}

	var out strings.Builder
	sess, err := OAuthLogin(context.Background(), r.Client(), r.URL, p, OAuthDeviceCode, nil, &out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "ABCD-EFGH")
	assert.Equal(t, "kubeconfig-u-abc:secret", sess.Token)
	require.Len(t, r.logins, 1)
	assert.Equal(t, "the-id-token", r.logins[0]["id_token"])
	assert.Equal(t, "azureADProvider", r.logins[0]["type"])
}

func TestParseExtToken(t *testing.T) {
	s, err := parseSession([]byte(`{"apiVersion":"ext.cattle.io/v1","kind":"Token","spec":{"userID":"u-1"},"status":{"bearerToken":"ext/abc:def","expiresAt":"2026-11-01T00:00:00Z"}}`))
	require.NoError(t, err)
	assert.Equal(t, "ext/abc:def", s.Token)
	assert.Equal(t, "u-1", s.UserID)
	_, err = parseSession([]byte(`{}`))
	assert.Error(t, err)
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
