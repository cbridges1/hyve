package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyRoute_DoesNotRequireHyveSession is the direct regression test
// for the bug caught live: a client using /proxy/* (typically `kubectl
// --kubeconfig <minted file>`) authenticates with a real Kubernetes
// ServiceAccount token, not a hyve session — the two share the
// Authorization header, so /proxy/* must never be wrapped in
// requireAuth/requireRole (see Routes' doc comment). A 401 here would mean
// that regression came back; the expected failure mode with no Proxy
// configured is 503 ("proxy not configured"), proving the request reached
// handleProxy rather than being rejected by hyve's own auth middleware.
func TestProxyRoute_DoesNotRequireHyveSession(t *testing.T) {
	s := &Server{SigningKey: []byte("key")} // Proxy left nil deliberately

	req := httptest.NewRequest(http.MethodGet, "/proxy/api/v1/pods", nil)
	req.Header.Set("Authorization", "Bearer some-kubernetes-serviceaccount-token")
	rec := httptest.NewRecorder()

	s.Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "expected 503 (reached handleProxy, no Proxy configured), not 401 (blocked by hyve auth)")
}

func TestProxyRoute_NoAuthorizationHeaderAtAll_StillReachesHandler(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}

	req := httptest.NewRequest(http.MethodGet, "/proxy/api/v1/pods", nil)
	rec := httptest.NewRecorder()

	s.Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBuildProxy_ForwardsAuthorizationHeaderUnmodified(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := BuildProxy(upstream.URL, nil)
	require.NoError(t, err)

	s := &Server{SigningKey: []byte("key"), Proxy: proxy}
	req := httptest.NewRequest(http.MethodGet, "/proxy/api/v1/pods", nil)
	req.Header.Set("Authorization", "Bearer some-kubernetes-serviceaccount-token")
	rec := httptest.NewRecorder()

	s.Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Bearer some-kubernetes-serviceaccount-token", gotAuth)
}

func TestBuildProxy_StripsProxyPrefix(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := BuildProxy(upstream.URL, nil)
	require.NoError(t, err)

	s := &Server{SigningKey: []byte("key"), Proxy: proxy}
	req := httptest.NewRequest(http.MethodGet, "/proxy/api/v1/pods", nil)
	rec := httptest.NewRecorder()

	s.Routes().ServeHTTP(rec, req)
	assert.Equal(t, "/api/v1/pods", gotPath, "the /proxy prefix must be stripped before forwarding")
}

func TestBuildProxy_InvalidCA(t *testing.T) {
	_, err := BuildProxy("https://kubernetes.default.svc", []byte("not a valid PEM certificate"))
	assert.Error(t, err)
}

func TestBuildProxy_EmptyCA_StillBuildsHandler(t *testing.T) {
	// A local dev install may run the API pod outside a real cluster (no CA
	// available yet) — BuildProxy should still succeed with an empty
	// pool rather than erroring, matching cmd/api/run.go's own graceful
	// degradation (Proxy left nil entirely when the CA file isn't found,
	// but if a caller does pass an empty slice explicitly, this must not
	// panic or error).
	h, err := BuildProxy("https://kubernetes.default.svc", nil)
	require.NoError(t, err)
	assert.NotNil(t, h)
}
