package hostapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cbridges1/hyve/internal/signin"
)

func server(signedIn bool) *httptest.Server {
	a := &API{
		Identify: func(*http.Request) (signin.Identity, bool) {
			return signin.Identity{Username: "alice"}, signedIn
		},
		DefaultTemplateNamespace: "capi-clusters",
	}
	mux := http.NewServeMux()
	a.Register(mux)
	return httptest.NewServer(mux)
}

func post(t *testing.T, srv *httptest.Server, path string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestNeedsASession(t *testing.T) {
	srv := server(false)
	defer srv.Close()
	for _, p := range []string{"/v1/hyve/providers", "/v1/hyve/render", "/v1/hyve/import"} {
		if code, _ := post(t, srv, p, map[string]any{}); code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", p, code)
		}
	}
}

const tpl = `apiVersion: hyve.io/v1alpha1
kind: ClusterTemplate
metadata: {name: small}
spec:
  clusterClassRef: {name: capd}
  kubernetesVersion: v1.34.8
  workers: {count: 2}
  resources:
    - name: podinfo
      helmOp: {helm: {repo: https://stefanprodan.github.io/podinfo, chart: podinfo, version: 6.7.1}}
`

func TestRender(t *testing.T) {
	srv := server(true)
	defer srv.Close()
	code, out := post(t, srv, "/v1/hyve/render", RenderRequest{YAML: tpl, Name: "demo", Env: "ci"})
	if code != http.StatusOK {
		t.Fatalf("render: %d %v", code, out)
	}
	objs, _ := out["objects"].([]any)
	if len(objs) != 2 {
		t.Fatalf("want the Cluster and one HelmOp, got %d objects", len(objs))
	}
	c := objs[0].(map[string]any)
	if c["kind"] != "Cluster" || c["metadata"].(map[string]any)["namespace"] != "ci" {
		t.Errorf("first object: %v", c["metadata"])
	}
	if objs[1].(map[string]any)["kind"] != "HelmOp" {
		t.Errorf("second object: %v", objs[1].(map[string]any)["kind"])
	}
	if notes, _ := out["notes"].([]any); len(notes) != 1 || !strings.Contains(notes[0].(string), "ClusterClass capd") {
		t.Errorf("notes: %v", out["notes"])
	}
	if code, out := post(t, srv, "/v1/hyve/render", RenderRequest{YAML: "kind: ConfigMap\napiVersion: v1\nmetadata: {name: x}\n"}); code != http.StatusBadRequest {
		t.Errorf("no template: %d %v", code, out)
	}
}

const oldTemplate = `apiVersion: hyve.io/v1alpha1
kind: Template
metadata: {name: capd, namespace: team}
spec:
  driver: {source: github.com/cbridges1/hyve-capi-module, version: latest}
  params: {flavor: capd, kubernetes_version: v1.34.8, control_plane_count: "1", worker_count: "1"}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: ignored}
`

func TestImport(t *testing.T) {
	srv := server(true)
	defer srv.Close()
	code, out := post(t, srv, "/v1/hyve/import", ImportRequest{YAML: oldTemplate, PrefixNamespace: true})
	if code != http.StatusOK {
		t.Fatalf("import: %d %v", code, out)
	}
	res, _ := out["results"].([]any)
	if len(res) != 1 {
		t.Fatalf("want one result (the ConfigMap isn't a template), got %v", res)
	}
	r := res[0].(map[string]any)
	if r["name"] != "team-capd" || r["skipped"] != nil {
		t.Fatalf("result: %v", r)
	}
	md := r["template"].(map[string]any)["metadata"].(map[string]any)
	if md["namespace"] != "capi-clusters" {
		t.Errorf("namespace %v, want the default template namespace", md["namespace"])
	}
}
