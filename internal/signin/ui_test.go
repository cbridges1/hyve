package signin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// uiRequest sends a request as the UI would: with its cookie, and with
// UIHeader unless noHeader.
func uiRequest(t *testing.T, h http.Handler, method, path, cookie string, body any, noHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.1:5555"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: cookie})
	}
	if !noHeader {
		req.Header.Set(UIHeader, "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie {
			return c
		}
	}
	return nil
}

func TestCookieLogin(t *testing.T) {
	s, _ := newStore(t, nil)
	addUser(t, s, "alice", "correct-horse-battery", "ops")
	h := NewServer(s, nil, "test").Handler()
	login := LoginRequest{Type: LocalProviderType, Username: "alice", Password: "correct-horse-battery", ResponseType: "cookie"}

	// Without the UI's header, a cookie login is refused: a form on another
	// site can't sign the browser in.
	rec := uiRequest(t, h, http.MethodPost, "/v1-public/login", "", login, true)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = uiRequest(t, h, http.MethodPost, "/v1-public/login", "", login, false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp LoginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.Token, "the token goes only in the cookie")
	c := sessionCookie(t, rec)
	require.NotNil(t, c)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.False(t, c.Secure, "plain HTTP in the test")

	// The cookie authenticates reads.
	rec = uiRequest(t, h, http.MethodGet, "/v1/me", c.Value, nil, true)
	require.Equal(t, http.StatusOK, rec.Code)
	var me MeResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	assert.Equal(t, "alice", me.Username)
	assert.Equal(t, []string{"ops"}, me.Groups)

	// Writes need the header too.
	rec = uiRequest(t, h, http.MethodPost, "/v1/logout", c.Value, map[string]any{}, true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	_, err := s.Lookup(context.Background(), c.Value)
	assert.NoError(t, err, "a refused logout leaves the session")

	rec = uiRequest(t, h, http.MethodPost, "/v1/logout", c.Value, map[string]any{}, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	if cleared := sessionCookie(t, rec); assert.NotNil(t, cleared) {
		assert.Less(t, cleared.MaxAge, 0)
	}
	_, err = s.Lookup(context.Background(), c.Value)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestUISessionFromCLI(t *testing.T) {
	s, _ := newStore(t, nil)
	addUser(t, s, "alice", "correct-horse-battery")
	h := NewServer(s, nil, "test").Handler()
	cli, _, err := s.Login(context.Background(), "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	// The CLI asks for a session for the browser with its bearer token.
	rec := postJSON(t, h, "/v1/ui-sessions", cli, map[string]any{})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var ui UISessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ui))
	require.NotEqual(t, cli, ui.Token, "the browser gets a session of its own")

	// The browser's cookie can't mint more.
	rec = uiRequest(t, h, http.MethodPost, "/v1/ui-sessions", ui.Token, map[string]any{}, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// The page trades it for its cookie, only with the UI's header.
	rec = uiRequest(t, h, http.MethodPost, "/v1-public/ui-session", "", map[string]string{"token": ui.Token}, true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = uiRequest(t, h, http.MethodPost, "/v1-public/ui-session", "", map[string]string{"token": ui.Token}, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	c := sessionCookie(t, rec)
	require.NotNil(t, c)
	assert.Equal(t, ui.Token, c.Value)

	rec = uiRequest(t, h, http.MethodPost, "/v1-public/ui-session", "", map[string]string{"token": "hs-aaaaaaaaaa:" + "bbbb"}, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Signing the browser out leaves the CLI signed in.
	rec = uiRequest(t, h, http.MethodPost, "/v1/logout", c.Value, map[string]any{}, false)
	require.Equal(t, http.StatusOK, rec.Code)
	_, err = s.Lookup(context.Background(), cli)
	assert.NoError(t, err)
}

func TestNamespacesEndpoint(t *testing.T) {
	nsObj := func(name string, labels map[string]string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	}
	// alice can list clusters in dev and templates in capi-clusters.
	allow := func(sar *authorizationv1.SubjectAccessReview) bool {
		a := sar.Spec.ResourceAttributes
		if a == nil || sar.Spec.User != "hyve:alice" {
			return false
		}
		return (a.Namespace == "dev" && a.Resource == "clusters") || (a.Namespace == "capi-clusters" && a.Resource == "clustertemplates")
	}
	s, _ := newStore(t, allow,
		nsObj("dev", map[string]string{"hyve.io/environment": ""}),
		nsObj("prod", map[string]string{"hyve.io/environment": ""}),
		nsObj("capi-clusters", nil),
		nsObj("kube-system", nil),
	)
	addUser(t, s, "alice", "correct-horse-battery")
	h := NewServer(s, nil, "test").Handler()
	tok, _, err := s.Login(context.Background(), "alice", "correct-horse-battery", "")
	require.NoError(t, err)

	rec := uiRequest(t, h, http.MethodGet, "/v1/namespaces", tok, nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Items []NamespaceInfo `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	var names []string
	for _, n := range out.Items {
		names = append(names, n.Name)
	}
	assert.Equal(t, []string{"capi-clusters", "dev"}, names)
	assert.Contains(t, out.Items[1].Labels, "hyve.io/environment")

	rec = uiRequest(t, h, http.MethodGet, "/v1/namespaces", "", nil, true)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLocalSession(t *testing.T) {
	s, _ := newStore(t, nil)
	addUser(t, s, "admin", "correct-horse-battery", "ops")
	local := func(srv *Server, host string, noHeader bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1-public/local-session", nil)
		req.Host = host
		if !noHeader {
			req.Header.Set(UIHeader, "1")
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// Off unless the host was started with a local user.
	off := NewServer(s, nil, "test")
	assert.Equal(t, http.StatusNotFound, local(off, "127.0.0.1:30444", false).Code)

	srv := NewServer(s, nil, "test")
	srv.LocalUser = "admin"
	// A cross-site page can't add the UI's header.
	assert.Equal(t, http.StatusForbidden, local(srv, "127.0.0.1:30444", true).Code)
	// A name that resolves to 127.0.0.1 (DNS rebinding) isn't a loopback Host.
	assert.Equal(t, http.StatusForbidden, local(srv, "evil.example:30444", false).Code)
	assert.Equal(t, http.StatusForbidden, local(srv, "192.168.1.5:30444", false).Code)

	for _, host := range []string{"127.0.0.1:30444", "localhost:5173", "[::1]:30444", "127.0.0.1"} {
		rec := local(srv, host, false)
		require.Equal(t, http.StatusCreated, rec.Code, host+": "+rec.Body.String())
		c := sessionCookie(t, rec)
		require.NotNil(t, c, host)
		assert.Equal(t, http.SameSiteStrictMode, c.SameSite)

		rec = uiRequest(t, srv.Handler(), http.MethodGet, "/v1/me", c.Value, nil, true)
		require.Equal(t, http.StatusOK, rec.Code)
		var me MeResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
		assert.Equal(t, "admin", me.Username)
		assert.Equal(t, []string{"ops"}, me.Groups)
		assert.True(t, me.AutoSignIn)
	}
}

func TestWebSocketCookieNeedsSameOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://hyve.example.com/k8s/clusters/x/api/v1/namespaces/a/pods/p/exec", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "hs-a:b"})
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://evil.example")
	_, ok := sessionToken(r)
	assert.False(t, ok)
	r.Header.Set("Origin", "https://hyve.example.com")
	tok, ok := sessionToken(r)
	assert.True(t, ok)
	assert.Equal(t, "hs-a:b", tok)
}
