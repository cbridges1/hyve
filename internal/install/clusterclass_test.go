package install

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestClassURL(t *testing.T) {
	for _, tc := range []struct{ fetch, name, want string }{
		{"https://github.com/o/r/releases/v0.2.0/infrastructure-components.yaml", "civo", "https://github.com/o/r/releases/v0.2.0/clusterclass-civo.yaml"},
		{"http://127.0.0.1:8765/infrastructure-components.yaml", "quick-start", "http://127.0.0.1:8765/clusterclass-quick-start.yaml"},
		{"", "https://example.com/my-class.yaml", "https://example.com/my-class.yaml"},
	} {
		got, err := ClassURL(tc.fetch, tc.name)
		if err != nil || got != tc.want {
			t.Errorf("ClassURL(%q, %q) = %q, %v; want %q", tc.fetch, tc.name, got, err, tc.want)
		}
	}
	if _, err := ClassURL("", "civo"); !errors.Is(err, ErrNoClassSource) {
		t.Errorf("a name without a components URL: %v, want ErrNoClassSource", err)
	}
}

func TestIsClassObject(t *testing.T) {
	for _, tc := range []struct {
		apiVersion, kind string
		want             bool
	}{
		{"cluster.x-k8s.io/v1beta2", "ClusterClass", true},
		{"infrastructure.cluster.x-k8s.io/v1alpha1", "CivoClusterTemplate", true},
		{"controlplane.cluster.x-k8s.io/v1beta2", "KubeadmControlPlaneTemplate", true},
		{"bootstrap.cluster.x-k8s.io/v1beta2", "KubeadmConfigTemplate", true},
		{"cluster.x-k8s.io/v1beta2", "Cluster", false},
		{"infrastructure.cluster.x-k8s.io/v1alpha1", "CivoCluster", false},
		{"hyve.io/v1alpha1", "ClusterTemplate", false},
		{"rbac.authorization.k8s.io/v1", "ClusterRole", false},
	} {
		o := &unstructured.Unstructured{}
		o.SetAPIVersion(tc.apiVersion)
		o.SetKind(tc.kind)
		if got := IsClassObject(o); got != tc.want {
			t.Errorf("IsClassObject(%s %s) = %v, want %v", tc.apiVersion, tc.kind, got, tc.want)
		}
	}
}

// A class file with anything but the class and its templates is refused
// before the cluster is touched (kc is nil), and its variables must have
// values.
func TestAddClusterClassRefuses(t *testing.T) {
	files := map[string]string{
		"/extra.yaml": `apiVersion: cluster.x-k8s.io/v1beta2
kind: ClusterClass
metadata: {name: c}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: everyone-admin}
`,
		"/vars.yaml": `apiVersion: cluster.x-k8s.io/v1beta2
kind: ClusterClass
metadata: {name: ${CLASS_NAME}}
`,
		"/empty.yaml": "",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(f))
	}))
	defer srv.Close()
	for path, want := range map[string]string{
		"/extra.yaml":   "ClusterRoleBinding everyone-admin isn't part of a ClusterClass",
		"/vars.yaml":    "CLASS_NAME",
		"/empty.yaml":   "nothing in it",
		"/missing.yaml": "404",
	} {
		_, err := AddClusterClass(context.Background(), nil, srv.URL+path, "capi-clusters", nil, time.Second)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error containing %q", path, err, want)
		}
	}
}

// A server error is retried; a 404 isn't.
func TestFetchManifestRetries(t *testing.T) {
	saved := fetchRetries
	fetchRetries = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { fetchRetries = saved }()
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch {
		case r.URL.Path == "/flaky.yaml" && hits[r.URL.Path] < 3:
			w.WriteHeader(http.StatusGatewayTimeout)
		case r.URL.Path == "/flaky.yaml":
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/down.yaml":
			w.WriteHeader(http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	raw, err := fetchManifest(context.Background(), srv.URL+"/flaky.yaml")
	if err != nil || string(raw) != "ok" || hits["/flaky.yaml"] != 3 {
		t.Errorf("flaky: %q, %v after %d tries; want ok after 3", raw, err, hits["/flaky.yaml"])
	}
	if _, err := fetchManifest(context.Background(), srv.URL+"/down.yaml"); err == nil || hits["/down.yaml"] != 4 {
		t.Errorf("down: %v after %d tries; want an error after 4", err, hits["/down.yaml"])
	}
	if _, err := fetchManifest(context.Background(), srv.URL+"/gone.yaml"); err == nil || hits["/gone.yaml"] != 1 {
		t.Errorf("404: %v after %d tries; want an error after 1", err, hits["/gone.yaml"])
	}
}
