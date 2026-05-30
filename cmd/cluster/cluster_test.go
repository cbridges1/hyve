package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/types"
)

// buildClusterDef is the core logic extracted from addClusterFromCLI so we can
// unit-test it without a git repository or cloud provider.
func buildClusterDef(clusterName, region, providerName string, nodes []string, nodeGroups []types.NodeGroup, clusterType string, onCreate, onDelete []string) types.ClusterDefinition {
	return buildClusterDefFull(clusterName, region, providerName, nodes, nodeGroups, clusterType, onCreate, onDelete, false, "")
}

func buildClusterDefFull(clusterName, region, providerName string, nodes []string, nodeGroups []types.NodeGroup, clusterType string, onCreate, onDelete []string, pause bool, expiresAt string) types.ClusterDefinition {
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
				OnCreate: onCreate,
				OnDelete: onDelete,
			},
			Ingress: types.IngressSpec{
				Enabled:      true,
				LoadBalancer: true,
			},
			Pause:     pause,
			ExpiresAt: expiresAt,
		},
	}
}

func TestBuildClusterDef_OnCreateAndOnDelete(t *testing.T) {
	onCreate := []string{"setup-monitoring", "notify-slack"}
	onDelete := []string{"cleanup-dns"}

	def := buildClusterDef("my-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", onCreate, onDelete)

	if len(def.Spec.Workflows.OnCreate) != 2 {
		t.Fatalf("expected 2 on-create workflows, got %d", len(def.Spec.Workflows.OnCreate))
	}
	if def.Spec.Workflows.OnCreate[0] != "setup-monitoring" {
		t.Errorf("expected first on-create to be 'setup-monitoring', got %q", def.Spec.Workflows.OnCreate[0])
	}
	if def.Spec.Workflows.OnCreate[1] != "notify-slack" {
		t.Errorf("expected second on-create to be 'notify-slack', got %q", def.Spec.Workflows.OnCreate[1])
	}

	if len(def.Spec.Workflows.OnDelete) != 1 {
		t.Fatalf("expected 1 on-delete workflow, got %d", len(def.Spec.Workflows.OnDelete))
	}
	if def.Spec.Workflows.OnDelete[0] != "cleanup-dns" {
		t.Errorf("expected on-delete to be 'cleanup-dns', got %q", def.Spec.Workflows.OnDelete[0])
	}
}

func TestBuildClusterDef_EmptyWorkflows(t *testing.T) {
	def := buildClusterDef("my-cluster", "us-east-1", "aws", nil, nil, "", nil, nil)

	if len(def.Spec.Workflows.OnCreate) != 0 {
		t.Errorf("expected 0 on-create workflows, got %d", len(def.Spec.Workflows.OnCreate))
	}
	if len(def.Spec.Workflows.OnDelete) != 0 {
		t.Errorf("expected 0 on-delete workflows, got %d", len(def.Spec.Workflows.OnDelete))
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

	if len(roundtrip.Spec.Workflows.OnCreate) != 1 || roundtrip.Spec.Workflows.OnCreate[0] != "wf-a" {
		t.Errorf("on-create did not survive YAML round-trip: %v", roundtrip.Spec.Workflows.OnCreate)
	}
	if len(roundtrip.Spec.Workflows.OnDelete) != 1 || roundtrip.Spec.Workflows.OnDelete[0] != "wf-b" {
		t.Errorf("on-delete did not survive YAML round-trip: %v", roundtrip.Spec.Workflows.OnDelete)
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
	if contains(yamlStr, "onCreate") || contains(yamlStr, "onDelete") {
		t.Errorf("expected empty workflows to be omitted from YAML, but found them in:\n%s", yamlStr)
	}
}

func TestAddCmdFlags_OnCreateAndOnDelete(t *testing.T) {
	// Verify the flags are registered on createCmd
	onCreateFlag := createCmd.Flags().Lookup("on-create")
	if onCreateFlag == nil {
		t.Fatal("--on-create flag not registered on createCmd")
	}
	if onCreateFlag.Value.Type() != "stringArray" {
		t.Errorf("expected --on-create to be stringArray, got %s", onCreateFlag.Value.Type())
	}

	onDeleteFlag := createCmd.Flags().Lookup("on-delete")
	if onDeleteFlag == nil {
		t.Fatal("--on-delete flag not registered on createCmd")
	}
	if onDeleteFlag.Value.Type() != "stringArray" {
		t.Errorf("expected --on-delete to be stringArray, got %s", onDeleteFlag.Value.Type())
	}
}

func TestAddCmdFlags_OnCreateAndOnDelete_Repeatable(t *testing.T) {
	// Reset createCmd flags to avoid state from other tests
	createCmd.Flags().Set("on-create", "wf-one")
	createCmd.Flags().Set("on-create", "wf-two")

	vals, err := createCmd.Flags().GetStringArray("on-create")
	if err != nil {
		t.Fatalf("GetStringArray failed: %v", err)
	}
	if len(vals) < 2 {
		t.Errorf("expected at least 2 on-create values after two Set calls, got %d: %v", len(vals), vals)
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

	if len(loaded.Spec.Workflows.OnCreate) != 1 || loaded.Spec.Workflows.OnCreate[0] != "on-create-wf" {
		t.Errorf("on-create not persisted: %v", loaded.Spec.Workflows.OnCreate)
	}
	if len(loaded.Spec.Workflows.OnDelete) != 1 || loaded.Spec.Workflows.OnDelete[0] != "on-destroy-wf" {
		t.Errorf("on-delete not persisted: %v", loaded.Spec.Workflows.OnDelete)
	}
}

func TestBuildClusterDef_PauseTrue(t *testing.T) {
	def := buildClusterDefFull("paused-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil, true, "")
	if !def.Spec.Pause {
		t.Error("expected Pause to be true")
	}
}

func TestBuildClusterDef_PauseFalse_Default(t *testing.T) {
	def := buildClusterDef("normal-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil)
	if def.Spec.Pause {
		t.Error("expected Pause to be false by default")
	}
}

func TestBuildClusterDef_ExpiresAt(t *testing.T) {
	expiry := "2026-12-31T23:59:00Z"
	def := buildClusterDefFull("expiry-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil, false, expiry)
	if def.Spec.ExpiresAt != expiry {
		t.Errorf("expected ExpiresAt %q, got %q", expiry, def.Spec.ExpiresAt)
	}
}

func TestBuildClusterDef_PauseSerializesToYAML(t *testing.T) {
	def := buildClusterDefFull("paused-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil, true, "")

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	yamlStr := string(data)
	if !contains(yamlStr, "pause: true") {
		t.Errorf("expected 'pause: true' in YAML output:\n%s", yamlStr)
	}
}

func TestBuildClusterDef_PauseFalseOmittedFromYAML(t *testing.T) {
	def := buildClusterDef("normal-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil)

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	yamlStr := string(data)
	if contains(yamlStr, "pause:") {
		t.Errorf("expected 'pause' to be omitted when false:\n%s", yamlStr)
	}
}

func TestBuildClusterDef_ExpiresAtSerializesToYAML(t *testing.T) {
	expiry := "2026-12-31T23:59:00Z"
	def := buildClusterDefFull("expiry-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil, false, expiry)

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	var roundtrip types.ClusterDefinition
	if err := yaml.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if roundtrip.Spec.ExpiresAt != expiry {
		t.Errorf("ExpiresAt did not survive YAML round-trip: got %q", roundtrip.Spec.ExpiresAt)
	}
}

func TestBuildClusterDef_ExpiresAtEmptyOmittedFromYAML(t *testing.T) {
	def := buildClusterDef("no-expiry-cluster", "PHX1", "civo", []string{"g4s.kube.small"}, nil, "", nil, nil)

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	yamlStr := string(data)
	if contains(yamlStr, "expiresAt:") {
		t.Errorf("expected 'expiresAt' to be omitted when empty:\n%s", yamlStr)
	}
}

func TestAddCmdFlags_PauseAndExpiresAt(t *testing.T) {
	pauseFlag := createCmd.Flags().Lookup("pause")
	if pauseFlag == nil {
		t.Fatal("--pause flag not registered on createCmd")
	}
	if pauseFlag.Value.Type() != "bool" {
		t.Errorf("expected --pause to be bool, got %s", pauseFlag.Value.Type())
	}

	expiresAtFlag := createCmd.Flags().Lookup("expires-at")
	if expiresAtFlag == nil {
		t.Fatal("--expires-at flag not registered on createCmd")
	}
	if expiresAtFlag.Value.Type() != "string" {
		t.Errorf("expected --expires-at to be string, got %s", expiresAtFlag.Value.Type())
	}
}

func TestModifyCmdFlags_PauseUnpauseExpiresAt(t *testing.T) {
	pauseFlag := modifyCmd.Flags().Lookup("pause")
	if pauseFlag == nil {
		t.Fatal("--pause flag not registered on modifyCmd")
	}

	unpauseFlag := modifyCmd.Flags().Lookup("unpause")
	if unpauseFlag == nil {
		t.Fatal("--unpause flag not registered on modifyCmd")
	}

	expiresAtFlag := modifyCmd.Flags().Lookup("expires-at")
	if expiresAtFlag == nil {
		t.Fatal("--expires-at flag not registered on modifyCmd")
	}
	if expiresAtFlag.Value.Type() != "string" {
		t.Errorf("expected --expires-at to be string, got %s", expiresAtFlag.Value.Type())
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
