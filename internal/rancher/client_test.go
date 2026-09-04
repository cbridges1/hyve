package rancher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v3-public/localProviders/local", r.URL.Path)
		assert.Equal(t, "login", r.URL.Query().Get("action"))
		assert.Equal(t, http.MethodPost, r.Method)

		var body loginRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "alice", body.Username)
		assert.Equal(t, "s3cret", body.Password)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(loginResponse{Token: "rancher-token-abc"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	token, err := c.Login(context.Background(), "alice", "s3cret")
	require.NoError(t, err)
	assert.Equal(t, "rancher-token-abc", token)
}

func TestLogin_WrongCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"authentication failed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Login(context.Background(), "alice", "wrong")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "login failed")
}

func TestLogin_EmptyTokenInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(loginResponse{})
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Login(context.Background(), "alice", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no token")
}

func TestGenerateKubeconfig_Success(t *testing.T) {
	const kc = "apiVersion: v1\nkind: Config\nclusters:\n- name: prod\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v3/clusters/c-abc123", r.URL.Path)
		assert.Equal(t, "generateKubeconfig", r.URL.Query().Get("action"))
		assert.Equal(t, "Bearer rancher-token-abc", r.Header.Get("Authorization"))

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(generateKubeconfigResponse{Config: kc})
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.GenerateKubeconfig(context.Background(), "rancher-token-abc", "c-abc123")
	require.NoError(t, err)
	assert.Equal(t, kc, string(got))
}

func TestGenerateKubeconfig_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"not authorized for this cluster"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.GenerateKubeconfig(context.Background(), "rancher-token-abc", "c-abc123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generateKubeconfig")
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("https://rancher.example.com/")
	assert.Equal(t, "https://rancher.example.com", c.ServerURL)
}
