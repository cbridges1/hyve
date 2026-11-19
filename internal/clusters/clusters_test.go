package clusters

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestName(t *testing.T) {
	for in, want := range map[string]string{"web": "web", "WgClust": "wgclust", " Dev-1 ": "dev-1"} {
		if got, err := Name(in); err != nil || got != want {
			t.Errorf("Name(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "my_cluster", "web.prod", "-web"} {
		if _, err := Name(in); err == nil {
			t.Errorf("Name(%q) took it", in)
		}
	}
}

func TestClassVariable(t *testing.T) {
	cls := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"variables": []any{map[string]any{"name": "region"}, map[string]any{"name": NodeCountVariable}},
	}}}
	if !ClassVariable(cls, NodeCountVariable) || ClassVariable(cls, "nodeSize") {
		t.Error("ClassVariable read the class's variables wrong")
	}
}
