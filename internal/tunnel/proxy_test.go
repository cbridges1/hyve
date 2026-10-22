package tunnel

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type user struct{}

func (user) KubeUser() string     { return "hyve:alice" }
func (user) KubeGroups() []string { return []string{"hyve:devs", "system:authenticated"} }

// A cluster with no agent connected is reached with its own credentials,
// as the caller.
func TestProxyDirect(t *testing.T) {
	var got *http.Request
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = io.WriteString(w, `{"kind":"PodList"}`)
	}))
	defer api.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw})

	reg := &Registry{Client: fake.NewClientBuilder().Build(), Namespace: "hyve-system"}
	p := &Proxy{
		Server:       NewServer(reg, ""),
		Authenticate: func(*http.Request) (Identity, bool) { return user{}, true },
		Direct: func(_ context.Context, id string) (*rest.Config, error) {
			if id != "default.demo" {
				return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, id)
			}
			return &rest.Config{Host: api.URL, BearerToken: "cluster-admin", TLSClientConfig: rest.TLSClientConfig{CAData: ca}}, nil
		},
	}

	req := httptest.NewRequest("GET", "/k8s/clusters/default.demo/api/v1/namespaces/web/pods?limit=5", nil)
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("Impersonate-User", "system:admin")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != 200 || got == nil {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	if got.URL.Path != "/api/v1/namespaces/web/pods" || got.URL.RawQuery != "limit=5" {
		t.Errorf("forwarded to %s", got.URL)
	}
	if got.Header.Get("Authorization") != "Bearer cluster-admin" {
		t.Errorf("auth = %q: want the cluster's own credentials, not the caller's", got.Header.Get("Authorization"))
	}
	if got.Header.Get("Impersonate-User") != "hyve:alice" || strings.Join(got.Header.Values("Impersonate-Group"), ",") != "hyve:devs,system:authenticated" {
		t.Errorf("impersonating %q %v", got.Header.Get("Impersonate-User"), got.Header.Values("Impersonate-Group"))
	}

	// No agent and no direct path: still a 404.
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest("GET", "/k8s/clusters/default.other/api", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown cluster: code %d", rec.Code)
	}
}
