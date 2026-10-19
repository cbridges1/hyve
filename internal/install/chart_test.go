package install

import (
	"strings"
	"testing"

	"github.com/cbridges1/hyve/internal/kube"
)

func TestRenderChart(t *testing.T) {
	out, err := RenderChart("hyve-system", map[string]any{"host": map[string]any{"enabled": true, "agentURL": "https://hyve.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	objs, err := kube.Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, o := range objs {
		kinds[o.GetKind()]++
	}
	if kinds["CustomResourceDefinition"] < 5 || kinds["Deployment"] != 2 || kinds["MutatingWebhookConfiguration"] != 1 {
		t.Errorf("kinds = %v", kinds)
	}
	if !strings.Contains(string(out), "--host-url=https://hyve.example.com") {
		t.Error("controller isn't given the host URL")
	}
}
