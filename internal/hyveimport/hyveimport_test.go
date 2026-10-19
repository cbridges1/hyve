package hyveimport

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func parse(t *testing.T, s string) *unstructured.Unstructured {
	t.Helper()
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: m}
}

func hasNote(r *Result, sub string) bool {
	for _, n := range r.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// nexus-config's templates/capd.yaml
const capd = `apiVersion: hyve.io/v1alpha1
kind: Template
metadata: {name: capd}
spec:
  description: >-
    A Cluster API cluster whose nodes are Docker containers.
  mgmtCluster: unraid-k3s
  driver: {source: ../../../hyve-capi-module, version: local}
  params: {flavor: capd, kubernetes_version: v1.34.8, control_plane_count: "1", worker_count: "1"}
`

// branlen/gke on unraid's hyve, plus a locked machine_type, a resource and a workflow
const gke = `apiVersion: hyve.io/v1alpha1
kind: Template
metadata: {name: gke, namespace: branlen}
spec:
  driver: {source: github.com/cbridges1/hyve-capi-module, version: latest}
  mgmtCluster: unraid-k3s
  params: {flavor: gke, kubernetes_version: v1.35.8, machine_type: e2-small, worker_count: "2", variables: "project=misc-306417;zones=[us-central1-a]"}
  schedule: 0 17 * * *
  lockParams: [machine_type, worker_count]
  workflows: {onCreate: [setup]}
  resources:
    - name: podinfo
      helm: {chart: podinfo, repo: https://stefanprodan.github.io/podinfo, version: 6.7.1, namespace: podinfo, values: {replicaCount: "2", ingress.enabled: true, "a\\.b": x}}
    - name: base
      source: github.com/cbridges1/nexus-configuration//resource-files/pod-info@main
    - name: gone
      delete: true
      source: github.com/x/y//z
`

// branlen/civo on unraid's hyve
const civo = `apiVersion: hyve.io/v1alpha1
kind: Template
metadata: {name: civo, namespace: branlen}
spec:
  driver: {source: github.com/cbridges1/hyve-civo-module, version: main}
  region: NYC1
  params: {cluster_type: k3s, cni_plugin: flannel, node_count: "1", node_size: g4s.kube.small, kubernetes_version: 1.30.5-k3s1}
  schedule: 0 20 * * 5
  lockParams: true
  resources:
    - name: podinfo
      helm: {chart: podinfo, repo: https://stefanprodan.github.io/podinfo, namespace: podinfo}
`

// nexus-config's templates/unraid-k3s.yaml shape
const unraid = `apiVersion: hyve.io/v1alpha1
kind: Template
metadata: {name: unraid-k3s}
spec:
  driver: {source: github.com/cbridges1/hyve-unraid-k3s-module, version: latest}
  params: {container_name: unraid-k3s, docker_socket: "true", ip_address: 192.168.50.100, k3s_version: v1.34.1-k3s1}
`

func TestCapdFlavor(t *testing.T) {
	r := Convert(parse(t, capd), "capd", "capi-clusters")
	if r.Skipped != "" {
		t.Fatal(r.Skipped)
	}
	s := r.Template.Spec
	if s.ClusterClassRef.Name != "capd" || s.Version != "v1.34.8" || *s.Workers.Count != 1 || s.Workers.Type != "machineDeployment" || s.ControlPlane.Replicas != 1 {
		t.Errorf("spec = %+v", s)
	}
	if s.Labels["cni"] != "flannel" || s.Description != "A Cluster API cluster whose nodes are Docker containers." {
		t.Errorf("labels/description = %v / %q", s.Labels, s.Description)
	}
	if len(r.Notes) != 0 {
		t.Errorf("unexpected notes: %v", r.Notes)
	}
}

func TestGkeFlavorWithLocksResourcesAndWorkflows(t *testing.T) {
	r := Convert(parse(t, gke), "branlen-gke", "capi-clusters")
	if r.Skipped != "" {
		t.Fatal(r.Skipped)
	}
	s := r.Template.Spec
	if s.ClusterClassRef.Name != "gke" || s.Workers.Type != "machinePool" || s.ControlPlane != nil || *s.Workers.Count != 2 {
		t.Errorf("spec = %+v (managed control plane must have no replicas)", s)
	}
	if s.Variables["machineType"] != "e2-small" || s.Variables["project"] != "misc-306417" {
		t.Errorf("variables = %v", s.Variables)
	}
	if z, ok := s.Variables["zones"].([]any); !ok || z[0] != "us-central1-a" {
		t.Errorf("zones = %#v, want a parsed YAML list", s.Variables["zones"])
	}
	if len(s.LockedVariables) != 1 || s.LockedVariables[0] != "machineType" || s.Schedule != "0 17 * * *" {
		t.Errorf("locks/schedule = %v / %q", s.LockedVariables, s.Schedule)
	}
	if !hasNote(r, `lockParams "worker_count"`) || !hasNote(r, "workflows setup (onCreate)") || !hasNote(r, "delete: true") {
		t.Errorf("notes = %v", r.Notes)
	}
	if len(s.Resources) != 2 {
		t.Fatalf("resources = %+v", s.Resources)
	}
	h := s.Resources[0]
	vals, _ := h.HelmOp["helm"].(map[string]any)["values"].(map[string]any)
	if h.Namespace != "podinfo" || vals["replicaCount"] != "2" ||
		vals["ingress"].(map[string]any)["enabled"] != true || vals["a.b"] != "x" {
		t.Errorf("helm resource = %+v", h.HelmOp)
	}
	g := s.Resources[1].GitRepo
	if g == nil || g["repo"] != "https://github.com/cbridges1/nexus-configuration.git" || g["branch"] != "main" || g["paths"].([]any)[0] != "resource-files/pod-info" {
		t.Errorf("git resource = %+v", g)
	}
}

func TestUnknownModuleIsSkipped(t *testing.T) {
	r := Convert(parse(t, strings.Replace(civo, "hyve-civo-module", "hyve-aws-module", 1)), "aws", "capi-clusters")
	if r.Template != nil || !strings.Contains(r.Skipped, "hyve-aws-module") {
		t.Errorf("got %+v", r)
	}
}

func TestCivoModule(t *testing.T) {
	r := Convert(parse(t, civo), "branlen-civo", "capi-clusters")
	if r.Skipped != "" {
		t.Fatal(r.Skipped)
	}
	s := r.Template.Spec
	if s.ClusterClassRef.Name != "civo" || s.Version != "v1.30.5" || s.Schedule != "0 20 * * 5" {
		t.Errorf("spec = %+v", s)
	}
	v := s.Variables
	if v["nodeSize"] != "g4s.kube.small" || v["nodeCount"] != int64(1) || v["civoVersion"] != "1.30.5-k3s1" || v["region"] != "NYC1" || v["clusterType"] != "k3s" {
		t.Errorf("variables = %v", v)
	}
	if !strings.Contains(strings.Join(s.LockedVariables, ","), "nodeSize") || len(s.Resources) != 1 || s.Resources[0].HelmOp["helm"].(map[string]any)["chart"] != "podinfo" {
		t.Errorf("locks/resources = %v / %+v", s.LockedVariables, s.Resources)
	}
}

func TestUnraidModule(t *testing.T) {
	r := Convert(parse(t, unraid), "unraid-k3s", "capi-clusters")
	if r.Skipped != "" {
		t.Fatal(r.Skipped)
	}
	s := r.Template.Spec
	if s.ClusterClassRef.Name != "unraid" || s.Version != "v1.34.1" || s.Workers != nil {
		t.Errorf("spec = %+v", s)
	}
	if s.Variables["host"] != "192.168.50.100" || s.Variables["containerName"] != "unraid-k3s" || s.Variables["dockerSocket"] != true {
		t.Errorf("variables = %v", s.Variables)
	}
}

func TestLockAllParams(t *testing.T) {
	r := Convert(parse(t, `apiVersion: hyve.io/v1alpha1
kind: Template
metadata: {name: g}
spec:
  driver: {source: github.com/cbridges1/hyve-capi-module}
  params: {flavor: gke, variables: "project=p1"}
  lockParams: true
`), "g", "capi-clusters")
	if got := strings.Join(r.Template.Spec.LockedVariables, ","); got != "machineType,project" {
		t.Errorf("locked = %q", got)
	}
	if !hasNote(r, "lockParams: true") {
		t.Errorf("notes = %v", r.Notes)
	}
}
