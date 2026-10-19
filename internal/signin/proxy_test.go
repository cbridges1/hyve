package signin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

type fakeSessions map[string]Identity

func (f fakeSessions) Lookup(_ context.Context, token string) (Identity, error) {
	if id, ok := f[token]; ok {
		return id, nil
	}
	return Identity{}, ErrInvalidSession
}

type seen struct {
	Path          string
	Authorization string
	Cookie        string
	User          string
	Groups        []string
}

// apiServer stands in for the management cluster's API: it reports the
// request it got, and echoes WebSocket messages on /ws.
func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") {
			if r.Header.Get("Impersonate-User") != "hyve:alice" {
				http.Error(w, "not impersonated", http.StatusForbidden)
				return
			}
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			typ, msg, err := c.Read(r.Context())
			if err != nil {
				return
			}
			_ = c.Write(r.Context(), typ, append([]byte("echo:"), msg...))
			return
		}
		_ = json.NewEncoder(w).Encode(seen{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Cookie:        r.Header.Get("Cookie"),
			User:          r.Header.Get("Impersonate-User"),
			Groups:        r.Header.Values("Impersonate-Group"),
		})
	}))
}

const aliceToken = "hs-abcdefghij:" + "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrst"

func newTestProxy(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	api := apiServer(t)
	t.Cleanup(api.Close)
	p, err := NewLocalProxy(&rest.Config{Host: api.URL, BearerToken: "host-sa-token"}, fakeSessions{
		aliceToken: {Username: "alice", Groups: []string{"ops"}},
	})
	require.NoError(t, err)
	front := httptest.NewServer(NewServer(nil, p, "test").Handler())
	t.Cleanup(front.Close)
	return api, front
}

func get(t *testing.T, url string, headers map[string]string) (int, seen) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var s seen
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
	}
	return resp.StatusCode, s
}

func TestProxyImpersonatesSessions(t *testing.T) {
	_, front := newTestProxy(t)
	status, s := get(t, front.URL+"/k8s/clusters/local/api/v1/namespaces/default/pods", map[string]string{
		"Authorization":     "Bearer " + aliceToken,
		"Impersonate-User":  "system:admin", // a caller can't pick who it is
		"Impersonate-Group": "system:masters",
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "/api/v1/namespaces/default/pods", s.Path)
	assert.Equal(t, "Bearer host-sa-token", s.Authorization)
	assert.Equal(t, "hyve:alice", s.User)
	assert.Equal(t, []string{"hyve:ops", "system:authenticated"}, s.Groups)
}

func TestProxyPassesOtherTokensThrough(t *testing.T) {
	_, front := newTestProxy(t)
	status, s := get(t, front.URL+"/k8s/clusters/local/apis/fleet.cattle.io/v1alpha1/clusters", map[string]string{
		"Authorization": "Bearer eyJhbGciOiJSUzI1NiJ9.fleet-agent.sig",
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "/apis/fleet.cattle.io/v1alpha1/clusters", s.Path)
	assert.Equal(t, "Bearer eyJhbGciOiJSUzI1NiJ9.fleet-agent.sig", s.Authorization, "the caller's own token, not the host's")
	assert.Empty(t, s.User)

	status, s = get(t, front.URL+"/k8s/clusters/local/version", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, s.Authorization, "anonymous stays anonymous")
}

func TestProxyRejectsBadSessions(t *testing.T) {
	_, front := newTestProxy(t)
	status, _ := get(t, front.URL+"/k8s/clusters/local/api", map[string]string{"Authorization": "Bearer hs-nope:nope"})
	assert.Equal(t, http.StatusUnauthorized, status, "a hyve-shaped token is never passed through")
}

func TestProxyUpgrades(t *testing.T) {
	_, front := newTestProxy(t)
	ctx := context.Background()
	wsURL := "ws" + strings.TrimPrefix(front.URL, "http") + "/k8s/clusters/local/api/v1/namespaces/default/pods/p/exec/ws"
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + aliceToken}},
	})
	require.NoError(t, err)
	defer c.CloseNow()
	require.NoError(t, c.Write(ctx, websocket.MessageText, []byte("ls")))
	_, msg, err := c.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "echo:ls", string(msg))
}

func TestProxyAcceptsTheUICookie(t *testing.T) {
	_, front := newTestProxy(t)
	url := front.URL + "/k8s/clusters/local/api/v1/namespaces"
	cookie := SessionCookie + "=" + aliceToken

	status, s := get(t, url, map[string]string{"Cookie": cookie})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "hyve:alice", s.User)
	assert.Empty(t, s.Cookie, "the session never reaches the API server")

	// A write authenticated by the cookie needs the UI's header.
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Cookie", cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	req, err = http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Cookie", cookie)
	req.Header.Set(UIHeader, "1")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
	assert.Equal(t, "hyve:alice", s.User)
}
