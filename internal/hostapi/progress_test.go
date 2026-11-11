package hostapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/signin"
)

func fleetObj(kind, ns, name string, labels map[string]string, spec, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1", "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ns, "generation": int64(1)},
		"spec":     spec, "status": status,
	}}
	u.SetLabels(labels)
	return u
}

func fleetClient(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		resources.BundleDeploymentGVR: "BundleDeploymentList", resources.BundleGVR: "BundleList",
		resources.HelmOpGVR: "HelmOpList", resources.GitRepoGVR: "GitRepoList",
	}, objs...)
}

// The host reads BundleDeployments for anyone who can read the
// Deployment, and for no one else.
func TestDeploymentProgress(t *testing.T) {
	bundle := fleetObj("Bundle", "capi-clusters", "web", nil, map[string]any{},
		map[string]any{"observedGeneration": int64(1), "summary": map[string]any{"desiredReady": int64(1)}})
	bd := fleetObj("BundleDeployment", "cluster-fleet-dev-a", "web", map[string]string{
		"fleet.cattle.io/bundle-name": "web", "fleet.cattle.io/bundle-namespace": "capi-clusters",
		"fleet.cattle.io/cluster-namespace": "dev", "fleet.cattle.io/cluster": "a",
	}, map[string]any{"deploymentID": "d1"}, map[string]any{"appliedDeploymentID": "d1", "ready": true})

	a := &API{
		Identify: func(r *http.Request) (signin.Identity, bool) {
			return signin.Identity{Username: r.Header.Get("X-Test-User")}, true
		},
		// alice can read the Deployment; bob's client has nothing in it,
		// as RBAC would hide it from him.
		Dynamic: func(ident signin.Identity) (dynamic.Interface, error) {
			if ident.Username == "alice" {
				return fleetClient(bundle.DeepCopy()), nil
			}
			return fleetClient(), nil
		},
		HostDynamic: fleetClient(bundle.DeepCopy(), bd),
	}
	mux := http.NewServeMux()
	a.Register(mux)
	get := func(user, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Test-User", user)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := get("alice", "/v1/hyve/deployments/capi-clusters/Bundle/web/progress")
	if rec.Code != http.StatusOK {
		t.Fatalf("alice: %d %s", rec.Code, rec.Body)
	}
	var p progressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Done || p.Ready != 1 || len(p.Clusters) != 1 || p.Clusters[0].Cluster != "dev/a" {
		t.Fatalf("alice's progress: %+v", p)
	}

	if rec := get("bob", "/v1/hyve/deployments/capi-clusters/Bundle/web/progress"); rec.Code != http.StatusNotFound {
		t.Fatalf("bob, who can't read the Deployment: %d %s", rec.Code, rec.Body)
	}
	if rec := get("alice", "/v1/hyve/deployments/capi-clusters/ConfigMap/web/progress"); rec.Code != http.StatusBadRequest {
		t.Fatalf("not a Deployment kind: %d", rec.Code)
	}
}
