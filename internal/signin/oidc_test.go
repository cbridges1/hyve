package signin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// fakeIdP is an OpenID Connect provider that signs in whoever claims says.
type fakeIdP struct {
	*httptest.Server
	key    *rsa.PrivateKey
	claims map[string]any
	nonce  string // from the last authorization request
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	idp := &fakeIdP{key: key}
	mux := http.NewServeMux()
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                idp.URL,
			"authorization_endpoint":                idp.URL + "/authorize",
			"token_endpoint":                        idp.URL + "/token",
			"jwks_uri":                              idp.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		c := map[string]any{"iss": idp.URL, "aud": "hyve", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": idp.nonce}
		for k, v := range idp.claims {
			c[k] = v
		}
		payload, _ := json.Marshal(c)
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		require.NoError(t, err)
		jws, err := signer.Sign(payload)
		require.NoError(t, err)
		idt, _ := jws.CompactSerialize()
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idt, "expires_in": 3600})
	})
	return idp
}

// signInWith runs a whole sign-in against srv and returns the callback's
// response.
func signInWith(t *testing.T, srv *httptest.Server, idp *fakeIdP, code string) *http.Response {
	t.Helper()
	c := srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(srv.URL + "/v1-public/oidc/start?redirect=" + url.QueryEscape("/clusters"))
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	auth, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(auth.String(), idp.URL+"/authorize"), auth.String())
	q := auth.Query()
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, srv.URL+"/v1-public/oidc/callback", q.Get("redirect_uri"))
	idp.nonce = q.Get("nonce")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1-public/oidc/callback?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), nil)
	for _, ck := range resp.Cookies() {
		req.AddCookie(ck)
	}
	out, err := c.Do(req)
	require.NoError(t, err)
	return out
}

func oidcSessionCookie(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func newOIDCHost(t *testing.T, cfg OIDCConfig) (*httptest.Server, *Store, *fakeIdP) {
	t.Helper()
	store, _ := newStore(t, nil)
	idp := newFakeIdP(t)
	srv := NewServer(store, nil, "test")
	ts := httptest.NewServer(nil)
	t.Cleanup(ts.Close)
	cfg.Issuer, cfg.ClientID, cfg.PublicURL = idp.URL, "hyve", ts.URL
	srv.OIDC = &OIDC{Config: cfg, Store: store}
	ts.Config.Handler = srv.Handler()
	return ts, store, idp
}

func TestOIDCSignIn(t *testing.T) {
	ts, store, idp := newOIDCHost(t, OIDCConfig{DisplayName: "Keycloak"})
	ctx := context.Background()
	addUser(t, store, "alice", "a-password-long-enough")

	idp.claims = map[string]any{"sub": "u-1", "preferred_username": "Alice", "email": "alice@example.com", "name": "Alice A", "groups": []string{"ops", "dev"}}
	resp := signInWith(t, ts, idp, "good-code")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/clusters", resp.Header.Get("Location"))
	token := oidcSessionCookie(resp)
	require.NotEmpty(t, token)

	// "alice" is a password user already, so the newcomer is alice-2,
	// with the provider's groups on the session.
	ident, err := store.Lookup(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "alice-2", ident.Username)
	assert.ElementsMatch(t, []string{"ops", "dev"}, ident.Groups)
	var u hyvev1alpha1.User
	require.NoError(t, store.Client.Get(ctx, client.ObjectKey{Name: "alice-2"}, &u))
	assert.Equal(t, ProviderOIDC, u.Spec.Provider)
	assert.Equal(t, "u-1", u.Spec.Subject)
	assert.Equal(t, "Alice A", u.Spec.DisplayName)
	assert.Empty(t, u.Spec.Groups, "groups stay on the session")

	// Signing in again finds the same User, with this sign-in's groups.
	idp.claims["groups"] = []string{"ops"}
	resp = signInWith(t, ts, idp, "good-code")
	ident, err = store.Lookup(ctx, oidcSessionCookie(resp))
	require.NoError(t, err)
	assert.Equal(t, "alice-2", ident.Username)
	assert.Equal(t, []string{"ops"}, ident.Groups)

	// A session for the CLI keeps the provider's groups.
	cli, _, err := store.NewSession(ctx, ident)
	require.NoError(t, err)
	ci, err := store.Lookup(ctx, cli)
	require.NoError(t, err)
	assert.Equal(t, []string{"ops"}, ci.Groups)

	// A disabled User is refused.
	require.NoError(t, store.Client.Get(ctx, client.ObjectKey{Name: "alice-2"}, &u))
	u.Spec.Disabled = true
	require.NoError(t, store.Client.Update(ctx, &u))
	resp = signInWith(t, ts, idp, "good-code")
	assert.Empty(t, oidcSessionCookie(resp))
	assert.Contains(t, resp.Header.Get("Location"), "/login?")

	// The code must be the provider's.
	idp.claims["sub"] = "u-2"
	resp = signInWith(t, ts, idp, "forged")
	assert.Empty(t, oidcSessionCookie(resp))
}

func TestOIDCAllowedDomainsAndGroups(t *testing.T) {
	ts, _, idp := newOIDCHost(t, OIDCConfig{AllowedDomains: []string{"example.com"}, AllowedGroups: []string{"hyve-users"}})

	idp.claims = map[string]any{"sub": "x", "email": "x@elsewhere.org", "groups": []string{"hyve-users"}}
	resp := signInWith(t, ts, idp, "good-code")
	assert.Empty(t, oidcSessionCookie(resp))
	assert.Contains(t, resp.Header.Get("Location"), "domain")

	idp.claims = map[string]any{"sub": "x", "email": "x@example.com", "email_verified": false, "groups": []string{"hyve-users"}}
	assert.Empty(t, oidcSessionCookie(signInWith(t, ts, idp, "good-code")))

	idp.claims = map[string]any{"sub": "x", "email": "x@example.com", "groups": []string{"other"}}
	resp = signInWith(t, ts, idp, "good-code")
	assert.Empty(t, oidcSessionCookie(resp))
	assert.Contains(t, resp.Header.Get("Location"), "group")

	idp.claims = map[string]any{"sub": "x", "email": "x@example.com", "email_verified": true, "groups": []string{"hyve-users"}}
	assert.NotEmpty(t, oidcSessionCookie(signInWith(t, ts, idp, "good-code")))
}

func TestOIDCRedirectStaysOnHost(t *testing.T) {
	assert.Equal(t, "/clusters?x=1", safeRedirect("/clusters?x=1"))
	for _, r := range []string{"https://evil.example", "//evil.example", "/\\evil.example", "clusters", ""} {
		assert.Equal(t, "/", safeRedirect(r), r)
	}
}

func TestAuthMethodsAndNoPasswords(t *testing.T) {
	ts, store, _ := newOIDCHost(t, OIDCConfig{DisplayName: "Keycloak"})
	resp, err := ts.Client().Get(ts.URL + "/v1-public/auth-methods")
	require.NoError(t, err)
	var m AuthMethods
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
	assert.True(t, m.Passwords)
	require.NotNil(t, m.OIDC)
	assert.Equal(t, "Keycloak", m.OIDC.DisplayName)

	addUser(t, store, "bob", "a-password-long-enough")
	srv := NewServer(store, nil, "test")
	srv.NoPasswords = true
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1-public/login", strings.NewReader(`{"username":"bob","password":"a-password-long-enough"}`)))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCLISessionOnlyFromTheUI(t *testing.T) {
	store, _ := newStore(t, nil)
	addUser(t, store, "carol", "a-password-long-enough")
	token, _, err := store.Login(context.Background(), "carol", "a-password-long-enough", "")
	require.NoError(t, err)
	h := NewServer(store, nil, "test").Handler()

	// The page: cookie plus the UI header.
	req := httptest.NewRequest(http.MethodPost, "/v1/cli-sessions", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	req.Header.Set(UIHeader, "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out UISessionResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	assert.NotEqual(t, token, out.Token)
	_, err = store.Lookup(context.Background(), out.Token)
	assert.NoError(t, err)

	// A cross-site page can't add the header.
	req = httptest.NewRequest(http.MethodPost, "/v1/cli-sessions", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
