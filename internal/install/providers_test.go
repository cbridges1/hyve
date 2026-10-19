package install

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRefKinds(t *testing.T) {
	class := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta2", "kind": "ClusterClass",
		"spec": map[string]any{
			"infrastructure": map[string]any{"templateRef": map[string]any{"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta2", "kind": "DockerClusterTemplate", "name": "a"}},
			"controlPlane": map[string]any{
				"templateRef":           map[string]any{"apiVersion": "controlplane.cluster.x-k8s.io/v1beta2", "kind": "KubeadmControlPlaneTemplate", "name": "b"},
				"machineInfrastructure": map[string]any{"templateRef": map[string]any{"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta2", "kind": "DockerMachineTemplate", "name": "c"}},
			},
			"workers": map[string]any{"machineDeployments": []any{map[string]any{
				"bootstrap": map[string]any{"templateRef": map[string]any{"apiVersion": "bootstrap.cluster.x-k8s.io/v1beta2", "kind": "KubeadmConfigTemplate", "name": "d"}},
			}}},
		},
	}}
	got := RefKinds(class)
	want := []schema.GroupKind{
		{Group: "bootstrap.cluster.x-k8s.io", Kind: "KubeadmConfigTemplate"},
		{Group: "controlplane.cluster.x-k8s.io", Kind: "KubeadmControlPlaneTemplate"},
		{Group: "infrastructure.cluster.x-k8s.io", Kind: "DockerClusterTemplate"},
		{Group: "infrastructure.cluster.x-k8s.io", Kind: "DockerMachineTemplate"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RefKinds = %v", got)
	}
	for _, gk := range got {
		if ProviderFor(gk) == "" {
			t.Errorf("no provider for %v", gk)
		}
	}
	if p := ProviderFor(schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "CivoClusterTemplate"}); p != "" {
		t.Errorf("civo mapped to %q", p)
	}
}

func TestMissingKinds(t *testing.T) {
	msg := `{"message":"resource mapping not found for name: \"capd\" namespace: \"\" from \"\": no matches for kind \"DockerClusterTemplate\" in version \"infrastructure.cluster.x-k8s.io/v1beta2\"\nensure CRDs are installed first; no matches for kind "KubeadmConfigTemplate" in version "bootstrap.cluster.x-k8s.io/v1beta2"}`
	got := MissingKinds(msg)
	want := []schema.GroupKind{
		{Group: "bootstrap.cluster.x-k8s.io", Kind: "KubeadmConfigTemplate"},
		{Group: "infrastructure.cluster.x-k8s.io", Kind: "DockerClusterTemplate"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingKinds = %v", got)
	}
}
