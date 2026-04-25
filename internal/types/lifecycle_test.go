package types

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ── WorkflowsSpec ─────────────────────────────────────────────────────────────

func TestWorkflowsSpec_BeforeCreateAfterDelete_RoundTrip(t *testing.T) {
	def := ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata:   ClusterMetadata{Name: "test", Region: "PHX1"},
		Spec: ClusterSpec{
			Provider: "civo",
			Workflows: WorkflowsSpec{
				BeforeCreate: []string{"provision-vpc", "provision-roles"},
				OnCreated:    []string{"notify-slack"},
				OnDestroy:    []string{"drain-nodes"},
				AfterDelete:  []string{"cleanup-vpc", "cleanup-roles"},
			},
		},
	}

	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ClusterDefinition
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	assertStringSlice(t, "BeforeCreate", got.Spec.Workflows.BeforeCreate, "provision-vpc", "provision-roles")
	assertStringSlice(t, "OnCreated", got.Spec.Workflows.OnCreated, "notify-slack")
	assertStringSlice(t, "OnDestroy", got.Spec.Workflows.OnDestroy, "drain-nodes")
	assertStringSlice(t, "AfterDelete", got.Spec.Workflows.AfterDelete, "cleanup-vpc", "cleanup-roles")
}

func TestWorkflowsSpec_EmptyFieldsOmitted(t *testing.T) {
	def := ClusterDefinition{
		Spec: ClusterSpec{
			Provider:  "civo",
			Workflows: WorkflowsSpec{OnCreated: []string{"wf-a"}},
		},
	}
	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "beforeCreate") {
		t.Error("beforeCreate should be omitted when empty")
	}
	if strings.Contains(s, "afterDelete") {
		t.Error("afterDelete should be omitted when empty")
	}
	if !strings.Contains(s, "onCreated") {
		t.Error("onCreated should be present")
	}
}

// ── PendingWorkflow ───────────────────────────────────────────────────────────

func TestPendingWorkflow_WithoutRunAt(t *testing.T) {
	spec := ClusterSpec{
		Provider: "civo",
		PendingWorkflows: []PendingWorkflow{
			{Workflow: "rotate-certs"},
		},
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ClusterSpec
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.PendingWorkflows) != 1 {
		t.Fatalf("expected 1 pending workflow, got %d", len(got.PendingWorkflows))
	}
	if got.PendingWorkflows[0].Workflow != "rotate-certs" {
		t.Errorf("expected workflow 'rotate-certs', got %q", got.PendingWorkflows[0].Workflow)
	}
	if got.PendingWorkflows[0].RunAt != "" {
		t.Errorf("expected empty RunAt, got %q", got.PendingWorkflows[0].RunAt)
	}
	if strings.Contains(string(data), "runAt") {
		t.Error("runAt should be omitted when empty")
	}
}

func TestPendingWorkflow_WithRunAt(t *testing.T) {
	spec := ClusterSpec{
		Provider: "civo",
		PendingWorkflows: []PendingWorkflow{
			{Workflow: "sync-secrets", RunAt: "2026-06-01T00:00:00Z"},
		},
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ClusterSpec
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.PendingWorkflows[0].RunAt != "2026-06-01T00:00:00Z" {
		t.Errorf("RunAt round-trip failed: got %q", got.PendingWorkflows[0].RunAt)
	}
}

func TestPendingWorkflow_MultipleEntries(t *testing.T) {
	spec := ClusterSpec{
		Provider: "aws",
		PendingWorkflows: []PendingWorkflow{
			{Workflow: "immediate"},
			{Workflow: "scheduled", RunAt: "2026-07-01T12:00:00Z"},
		},
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ClusterSpec
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.PendingWorkflows) != 2 {
		t.Fatalf("expected 2 pending workflows, got %d", len(got.PendingWorkflows))
	}
}

func TestPendingWorkflow_EmptyOmitted(t *testing.T) {
	spec := ClusterSpec{Provider: "civo"}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "pendingWorkflows") {
		t.Error("pendingWorkflows should be omitted when nil")
	}
}

// ── WorkflowSchedule ──────────────────────────────────────────────────────────

func TestWorkflowSchedule_RoundTrip(t *testing.T) {
	spec := ClusterSpec{
		Provider: "aws",
		WorkflowSchedules: []WorkflowSchedule{
			{Workflow: "rotate-certs", Schedule: "0 0 * * 0"},
			{Workflow: "sync-secrets", Schedule: "0 */6 * * *"},
		},
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ClusterSpec
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.WorkflowSchedules) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(got.WorkflowSchedules))
	}
	if got.WorkflowSchedules[0].Workflow != "rotate-certs" || got.WorkflowSchedules[0].Schedule != "0 0 * * 0" {
		t.Errorf("first schedule mismatch: %+v", got.WorkflowSchedules[0])
	}
	if got.WorkflowSchedules[1].Workflow != "sync-secrets" || got.WorkflowSchedules[1].Schedule != "0 */6 * * *" {
		t.Errorf("second schedule mismatch: %+v", got.WorkflowSchedules[1])
	}
}

func TestWorkflowSchedule_EmptyOmitted(t *testing.T) {
	spec := ClusterSpec{Provider: "civo"}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "workflowSchedules") {
		t.Error("workflowSchedules should be omitted when nil")
	}
}

// ── AWS role name fields ──────────────────────────────────────────────────────

func TestAWSRoleNames_SerializedToYAML(t *testing.T) {
	spec := ClusterSpec{
		Provider:        "aws",
		AWSEKSRoleName:  "my-eks-role",
		AWSNodeRoleName: "my-node-role",
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, "awsEksRoleName: my-eks-role") {
		t.Errorf("awsEksRoleName not found in YAML:\n%s", s)
	}
	if !strings.Contains(s, "awsNodeRoleName: my-node-role") {
		t.Errorf("awsNodeRoleName not found in YAML:\n%s", s)
	}
}

func TestAWSRoleARNs_NotSerializedToYAML(t *testing.T) {
	spec := ClusterSpec{
		Provider:       "aws",
		AWSEKSRoleARN:  "arn:aws:iam::123456789012:role/eks-role",
		AWSNodeRoleARN: "arn:aws:iam::123456789012:role/node-role",
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)

	if strings.Contains(s, "arn:aws:iam") {
		t.Errorf("ARN should not appear in serialized YAML (yaml:\"-\"):\n%s", s)
	}
}

func TestAWSRoleNames_RoundTrip(t *testing.T) {
	spec := ClusterSpec{
		Provider:        "aws",
		AWSEKSRoleName:  "eks-control-plane-role",
		AWSNodeRoleName: "eks-worker-role",
		AWSEKSRoleARN:   "arn:aws:iam::999:role/should-not-persist",
	}
	data, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ClusterSpec
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.AWSEKSRoleName != "eks-control-plane-role" {
		t.Errorf("AWSEKSRoleName: expected 'eks-control-plane-role', got %q", got.AWSEKSRoleName)
	}
	if got.AWSNodeRoleName != "eks-worker-role" {
		t.Errorf("AWSNodeRoleName: expected 'eks-worker-role', got %q", got.AWSNodeRoleName)
	}
	if got.AWSEKSRoleARN != "" {
		t.Errorf("AWSEKSRoleARN should be empty after round-trip (yaml:\"-\"), got %q", got.AWSEKSRoleARN)
	}
	if got.AWSNodeRoleARN != "" {
		t.Errorf("AWSNodeRoleARN should be empty after round-trip (yaml:\"-\"), got %q", got.AWSNodeRoleARN)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertStringSlice(t *testing.T, field string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: expected %d elements, got %d: %v", field, len(want), len(got), got)
		return
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("%s[%d]: expected %q, got %q", field, i, w, got[i])
		}
	}
}
