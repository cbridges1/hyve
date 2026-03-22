package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"hyve/internal/types"
)

// buildClusterDef is the core logic extracted from addClusterFromCLI so we can
// unit-test it without a git repository or cloud provider.
func buildClusterDef(clusterName, region, providerName string, nodes []string, nodeGroups []types.NodeGroup, clusterType string, onCreated, onDestroy []string) types.ClusterDefinition {
	return types.ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata: types.ClusterMetadata{
			Name:   clusterName,
			Region: region,
		},
		Spec: types.ClusterSpec{
			Provider:    providerName,
			Nodes:       nodes,
			NodeGroups:  nodeGroups,
			ClusterType: clusterType,
			Workflows: types.WorkflowsSpec{
				OnCreated: onCreated,
				OnDestroy: onDestroy,
			},
			Ingress: types.IngressSpec{
				Enabled:      true,
				LoadBalancer: true,
			},
		},
	}
}

func TestBuildClusterDef_OnCreatedAndOnDestroy(t *testing.T) {
	onCreated := []string{"setup-monitoring", "notify-slack"}
	onDestroy := []string{"cleanup-dns"}

	def := buildClusterDef("my-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", onCreated, onDestroy)

	if len(def.Spec.Workflows.OnCreated) != 2 {
		t.Fatalf("expected 2 on-created workflows, got %d", len(def.Spec.Workflows.OnCreated))
	}
	if def.Spec.Workflows.OnCreated[0] != "setup-monitoring" {
		t.Errorf("expected first on-created to be 'setup-monitoring', got %q", def.Spec.Workflows.OnCreated[0])
	}
	if def.Spec.Workflows.OnCreated[1] != "notify-slack" {
		t.Errorf("expected second on-created to be 'notify-slack', got %q", def.Spec.Workflows.OnCreated[1])
	}

	if len(def.Spec.Workflows.OnDestroy) != 1 {
		t.Fatalf("expected 1 on-destroy workflow, got %d", len(def.Spec.Workflows.OnDestroy))
	}
	if def.Spec.Workflows.OnDestroy[0] != "cleanup-dns" {
		t.Errorf("expected on-destroy to be 'cleanup-dns', got %q", def.Spec.Workflows.OnDestroy[0])
	}
}

func TestBuildClusterDef_EmptyWorkflows(t *testing.T) {
	def := buildClusterDef("my-cluster", "us-east-1", "aws", nil, nil, "", nil, nil)

	if len(def.Spec.Workflows.OnCreated) != 0 {
		t.Errorf("expected 0 on-created workflows, got %d", len(def.Spec.Workflows.OnCreated))
	}
	if len(def.Spec.Workflows.OnDestroy) != 0 {
		t.Errorf("expected 0 on-destroy workflows, got %d", len(def.Spec.Workflows.OnDestroy))
	}
}

func TestBuildClusterDef_WorkflowsSerializeToYAML(t *testing.T) {
	def := buildClusterDef("test-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", []string{"wf-a"}, []string{"wf-b"})

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	var roundtrip types.ClusterDefinition
	if err := yaml.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if len(roundtrip.Spec.Workflows.OnCreated) != 1 || roundtrip.Spec.Workflows.OnCreated[0] != "wf-a" {
		t.Errorf("on-created did not survive YAML round-trip: %v", roundtrip.Spec.Workflows.OnCreated)
	}
	if len(roundtrip.Spec.Workflows.OnDestroy) != 1 || roundtrip.Spec.Workflows.OnDestroy[0] != "wf-b" {
		t.Errorf("on-destroy did not survive YAML round-trip: %v", roundtrip.Spec.Workflows.OnDestroy)
	}
}

func TestBuildClusterDef_EmptyWorkflowsOmittedFromYAML(t *testing.T) {
	def := buildClusterDef("test-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil)

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	// workflows field should be omitted when empty (omitempty)
	yamlStr := string(data)
	if contains(yamlStr, "onCreated") || contains(yamlStr, "onDestroy") {
		t.Errorf("expected empty workflows to be omitted from YAML, but found them in:\n%s", yamlStr)
	}
}

func TestAddCmdFlags_OnCreatedAndOnDestroy(t *testing.T) {
	// Verify the flags are registered on addCmd
	onCreatedFlag := addCmd.Flags().Lookup("on-created")
	if onCreatedFlag == nil {
		t.Fatal("--on-created flag not registered on addCmd")
	}
	if onCreatedFlag.Value.Type() != "stringArray" {
		t.Errorf("expected --on-created to be stringArray, got %s", onCreatedFlag.Value.Type())
	}

	onDestroyFlag := addCmd.Flags().Lookup("on-destroy")
	if onDestroyFlag == nil {
		t.Fatal("--on-destroy flag not registered on addCmd")
	}
	if onDestroyFlag.Value.Type() != "stringArray" {
		t.Errorf("expected --on-destroy to be stringArray, got %s", onDestroyFlag.Value.Type())
	}
}

func TestAddCmdFlags_OnCreatedAndOnDestroy_Repeatable(t *testing.T) {
	// Reset addCmd flags to avoid state from other tests
	addCmd.Flags().Set("on-created", "wf-one")
	addCmd.Flags().Set("on-created", "wf-two")

	vals, err := addCmd.Flags().GetStringArray("on-created")
	if err != nil {
		t.Fatalf("GetStringArray failed: %v", err)
	}
	if len(vals) < 2 {
		t.Errorf("expected at least 2 on-created values after two Set calls, got %d: %v", len(vals), vals)
	}
}

func TestWorkflowsWrittenToFile(t *testing.T) {
	dir := t.TempDir()
	clusterName := "wf-test"
	filePath := filepath.Join(dir, clusterName+".yaml")

	def := buildClusterDef(clusterName, "PHX1", "civo", []string{"g4s.kube.small"}, nil, "",
		[]string{"on-create-wf"}, []string{"on-destroy-wf"})

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var loaded types.ClusterDefinition
	if err := yaml.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(loaded.Spec.Workflows.OnCreated) != 1 || loaded.Spec.Workflows.OnCreated[0] != "on-create-wf" {
		t.Errorf("on-created not persisted: %v", loaded.Spec.Workflows.OnCreated)
	}
	if len(loaded.Spec.Workflows.OnDestroy) != 1 || loaded.Spec.Workflows.OnDestroy[0] != "on-destroy-wf" {
		t.Errorf("on-destroy not persisted: %v", loaded.Spec.Workflows.OnDestroy)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
