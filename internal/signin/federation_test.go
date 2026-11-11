package signin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
)

// ciIssuer is a CI system's OIDC issuer: discovery, keys, and tokens it
// signs.
type ciIssuer struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newCIIssuer(t *testing.T) *ciIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	c := &ciIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": c.srv.URL, "jwks_uri": c.srv.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	c.srv = httptest.NewTLSServer(mux)
	t.Cleanup(c.srv.Close)
	return c
}

func (c *ciIssuer) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: c.key, KeyID: "k1"}}, nil)
	require.NoError(t, err)
	all := map[string]any{"iss": c.srv.URL, "aud": "hyve", "sub": "repo:org/web:ref:refs/heads/main",
		"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
	for k, v := range claims {
		all[k] = v
	}
	payload, err := json.Marshal(all)
	require.NoError(t, err)
	jws, err := sig.Sign(payload)
	require.NoError(t, err)
	raw, err := jws.CompactSerialize()
	require.NoError(t, err)
	return raw
}

func TestValidateTrustedCI(t *testing.T) {
	ok := hyvev1alpha1.TrustedCI{Name: "web", Issuer: "https://token.actions.githubusercontent.com", Claims: map[string]string{"repository": "org/web"}}
	assert.NoError(t, ValidateTrustedCI(ok))
	for name, bad := range map[string]hyvev1alpha1.TrustedCI{
		"no binding claim": {Name: "web", Issuer: ok.Issuer, Claims: map[string]string{"ref": "refs/heads/main"}},
		"any repository":   {Name: "web", Issuer: ok.Issuer, Claims: map[string]string{"repository": "*"}},
		"http issuer":      {Name: "web", Issuer: "http://ci.example", Claims: ok.Claims},
		"bad name":         {Name: "Web", Issuer: ok.Issuer, Claims: ok.Claims},
	} {
		assert.Error(t, ValidateTrustedCI(bad), name)
	}
}

func TestClaimMatches(t *testing.T) {
	assert.True(t, claimMatches("refs/tags/*", "refs/tags/v1.2.3"))
	assert.False(t, claimMatches("refs/tags/*", "refs/heads/main"))
	assert.True(t, claimMatches("org/web", "org/web"))
	assert.False(t, claimMatches("org/web", "org/web-evil"), "no prefix match")
	assert.False(t, claimMatches("org.web", "orgXweb"), "a dot is a dot")
	assert.True(t, claimMatches("42", float64(42)))
	assert.False(t, claimMatches("*", []any{"x"}))
	assert.False(t, claimMatches("x", nil))
}

func TestFederatedLogin(t *testing.T) {
	iss := newCIIssuer(t)
	s, _ := newStore(t, nil)
	ctx := oidc.ClientContext(context.Background(), iss.srv.Client())
	require.NoError(t, s.Client.Create(ctx, &hyvev1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "ci", UID: "uid-ci"},
		Spec: hyvev1alpha1.UserSpec{Groups: []string{"deployers"}, TrustedCI: []hyvev1alpha1.TrustedCI{{
			Name: "web-releases", Issuer: iss.srv.URL,
			Claims: map[string]string{"repository": "org/web", "ref": "refs/tags/*"},
		}}},
	}))
	var v ciVerifier
	login := func(user, raw string) (string, error) {
		token, _, _, err := s.FederatedLogin(ctx, v.verify, user, raw)
		return token, err
	}

	token, err := login("ci", iss.token(t, map[string]any{"repository": "org/web", "ref": "refs/tags/v1.4.0"}))
	require.NoError(t, err)
	ident, err := s.Lookup(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "ci", ident.Username)
	assert.Equal(t, KindFederated, ident.Kind)
	assert.WithinDuration(t, s.now().Add(FederatedSessionTTL), ident.ExpiresAt, time.Second)

	for name, raw := range map[string]string{
		"another repository": iss.token(t, map[string]any{"repository": "org/other", "ref": "refs/tags/v1"}),
		"a branch":           iss.token(t, map[string]any{"repository": "org/web", "ref": "refs/heads/main"}),
		"another audience":   iss.token(t, map[string]any{"repository": "org/web", "ref": "refs/tags/v1", "aud": "someone-else"}),
		"expired":            iss.token(t, map[string]any{"repository": "org/web", "ref": "refs/tags/v1", "exp": time.Now().Add(-time.Minute).Unix()}),
		"not a JWT":          "nope",
	} {
		_, err := login("ci", raw)
		assert.ErrorIs(t, err, errNotTrusted, name)
	}

	// Signed by someone else, claiming the trusted issuer.
	other := newCIIssuer(t)
	forged := other.token(t, map[string]any{"iss": iss.srv.URL, "repository": "org/web", "ref": "refs/tags/v1"})
	_, err = login("ci", forged)
	assert.ErrorIs(t, err, errNotTrusted, "a token not signed by the issuer's keys")

	_, err = login("nobody", iss.token(t, map[string]any{"repository": "org/web", "ref": "refs/tags/v1"}))
	assert.ErrorIs(t, err, errNotTrusted)
}

// A CI job's session can't make API tokens: the endpoint refuses it.
func TestFederatedSessionCantMakeTokens(t *testing.T) {
	iss := newCIIssuer(t)
	s, _ := newStore(t, nil)
	ctx := oidc.ClientContext(context.Background(), iss.srv.Client())
	require.NoError(t, s.Client.Create(ctx, &hyvev1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "ci", UID: "uid-ci"},
		Spec: hyvev1alpha1.UserSpec{TrustedCI: []hyvev1alpha1.TrustedCI{{
			Name: "web", Issuer: iss.srv.URL, Claims: map[string]string{"repository": "org/web"},
		}}},
	}))
	var v ciVerifier
	token, _, _, err := s.FederatedLogin(ctx, v.verify, "ci", iss.token(t, map[string]any{"repository": "org/web"}))
	require.NoError(t, err)
	h := NewServer(s, nil, "test").Handler()
	rec := postJSON(t, h, "/v1/tokens", token, CreateTokenRequest{Name: "escape"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
