package release

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRefKind(t *testing.T) {
	gk, v := refKind(map[string]any{"apiGroup": "infrastructure.cluster.x-k8s.io", "kind": "CivoCluster", "name": "a"})
	if gk != (schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "CivoCluster"}) || v != "" {
		t.Errorf("v1beta2 ref: %v %q", gk, v)
	}
	gk, v = refKind(map[string]any{"apiVersion": "controlplane.cluster.x-k8s.io/v1beta1", "kind": "KubeadmControlPlane", "name": "a"})
	if gk != (schema.GroupKind{Group: "controlplane.cluster.x-k8s.io", Kind: "KubeadmControlPlane"}) || v != "v1beta1" {
		t.Errorf("v1beta1 ref: %v %q", gk, v)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine(errors.Join(errors.New("a"), errors.New("b"), errors.New("c"))); got != "a (and 2 more)" {
		t.Errorf("got %q", got)
	}
	if got := firstLine(errors.New("a")); got != "a" {
		t.Errorf("got %q", got)
	}
}
