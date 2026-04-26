package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/template"
)

// ── Flag registration ─────────────────────────────────────────────────────────

func TestTemplateCmdFlags_AllLifecycleHooks(t *testing.T) {
	flags := []string{"before-create", "on-created", "on-destroy", "after-delete"}
	for _, name := range flags {
		f := templateCreateCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s flag not registered on templateCreateCmd", name)
		}
	}
}

func TestTemplateCmdFlags_Schedule(t *testing.T) {
	f := templateCreateCmd.Flags().Lookup("schedule")
	if f == nil {
		t.Fatal("--schedule flag not registered on templateCreateCmd")
	}
	if f.Value.Type() != "string" {
		t.Errorf("expected --schedule to be string, got %s", f.Value.Type())
	}
}

func TestTemplateCmdFlags_ProviderAccountFields(t *testing.T) {
	flags := []struct {
		name     string
		wantType string
	}{
		{"org", "string"},
		{"account", "string"},
		{"vpc-id", "string"},
		{"eks-role-name", "string"},
		{"node-role-name", "string"},
		{"subscription", "string"},
		{"resource-group", "string"},
		{"project", "string"},
	}
	for _, f := range flags {
		flag := templateCreateCmd.Flags().Lookup(f.name)
		if flag == nil {
			t.Errorf("--%s flag not registered on templateCreateCmd", f.name)
			continue
		}
		if flag.Value.Type() != f.wantType {
			t.Errorf("--%s: expected type %s, got %s", f.name, f.wantType, flag.Value.Type())
		}
	}
}

// ── Template struct with all 4 lifecycle hooks ────────────────────────────────

func buildTemplate(name, provider string, hooks template.TemplateWorkflowsSpec) *template.Template {
	return &template.Template{
		APIVersion: "v1",
		Kind:       "Template",
		Metadata:   template.TemplateMetadata{Name: name},
		Spec: template.TemplateSpec{
			Provider:  provider,
			Region:    "PHX1",
			Nodes:     []string{"g4s.kube.small"},
			Workflows: hooks,
		},
	}
}

func TestBuildTemplate_AllFourHooks(t *testing.T) {
	tmpl := buildTemplate("my-template", "civo", template.TemplateWorkflowsSpec{
		BeforeCreate: []string{"provision-vpc"},
		OnCreated:    []string{"notify-slack"},
		OnDestroy:    []string{"drain-nodes"},
		AfterDelete:  []string{"cleanup-vpc"},
	})

	if len(tmpl.Spec.Workflows.BeforeCreate) != 1 || tmpl.Spec.Workflows.BeforeCreate[0] != "provision-vpc" {
		t.Errorf("BeforeCreate: got %v", tmpl.Spec.Workflows.BeforeCreate)
	}
	if len(tmpl.Spec.Workflows.OnCreated) != 1 || tmpl.Spec.Workflows.OnCreated[0] != "notify-slack" {
		t.Errorf("OnCreated: got %v", tmpl.Spec.Workflows.OnCreated)
	}
	if len(tmpl.Spec.Workflows.OnDestroy) != 1 || tmpl.Spec.Workflows.OnDestroy[0] != "drain-nodes" {
		t.Errorf("OnDestroy: got %v", tmpl.Spec.Workflows.OnDestroy)
	}
	if len(tmpl.Spec.Workflows.AfterDelete) != 1 || tmpl.Spec.Workflows.AfterDelete[0] != "cleanup-vpc" {
		t.Errorf("AfterDelete: got %v", tmpl.Spec.Workflows.AfterDelete)
	}
}

func TestBuildTemplate_EmptyHooks(t *testing.T) {
	tmpl := buildTemplate("no-hooks", "civo", template.TemplateWorkflowsSpec{})
	if len(tmpl.Spec.Workflows.BeforeCreate) != 0 {
		t.Errorf("expected empty BeforeCreate, got %v", tmpl.Spec.Workflows.BeforeCreate)
	}
	if len(tmpl.Spec.Workflows.AfterDelete) != 0 {
		t.Errorf("expected empty AfterDelete, got %v", tmpl.Spec.Workflows.AfterDelete)
	}
}

func TestBuildTemplate_AllHooks_YAMLRoundtrip(t *testing.T) {
	tmpl := buildTemplate("rt-template", "aws", template.TemplateWorkflowsSpec{
		BeforeCreate: []string{"pre-a", "pre-b"},
		OnCreated:    []string{"post-a"},
		OnDestroy:    []string{"teardown"},
		AfterDelete:  []string{"cleanup-a", "cleanup-b"},
	})

	data, err := yaml.Marshal(tmpl)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}

	var got template.Template
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	assertStrings(t, "BeforeCreate", got.Spec.Workflows.BeforeCreate, "pre-a", "pre-b")
	assertStrings(t, "OnCreated", got.Spec.Workflows.OnCreated, "post-a")
	assertStrings(t, "OnDestroy", got.Spec.Workflows.OnDestroy, "teardown")
	assertStrings(t, "AfterDelete", got.Spec.Workflows.AfterDelete, "cleanup-a", "cleanup-b")
}

func TestBuildTemplate_EmptyHooks_OmittedFromYAML(t *testing.T) {
	tmpl := buildTemplate("no-hooks-yaml", "civo", template.TemplateWorkflowsSpec{
		OnCreated: []string{"wf-a"},
	})

	data, err := yaml.Marshal(tmpl)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}

	s := string(data)
	if strings.Contains(s, "beforeCreate") {
		t.Error("beforeCreate should be omitted when empty")
	}
	if strings.Contains(s, "afterDelete") {
		t.Error("afterDelete should be omitted when empty")
	}
}

// ── Schedule field ────────────────────────────────────────────────────────────

func TestBuildTemplate_ScheduleField(t *testing.T) {
	tmpl := buildTemplate("sched-template", "civo", template.TemplateWorkflowsSpec{})
	tmpl.Spec.Schedule = "0 20 * * 5"

	data, err := yaml.Marshal(tmpl)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}

	var got template.Template
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	if got.Spec.Schedule != "0 20 * * 5" {
		t.Errorf("Schedule: expected '0 20 * * 5', got %q", got.Spec.Schedule)
	}
}

func TestBuildTemplate_EmptySchedule_OmittedFromYAML(t *testing.T) {
	tmpl := buildTemplate("no-sched", "civo", template.TemplateWorkflowsSpec{})

	data, err := yaml.Marshal(tmpl)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}

	if strings.Contains(string(data), "schedule:") {
		t.Error("schedule should be omitted when empty")
	}
}

// ── Hooks persisted to file ───────────────────────────────────────────────────

func TestHooksWrittenToFile(t *testing.T) {
	dir := t.TempDir()
	tmplName := "hooks-file-test"
	filePath := filepath.Join(dir, tmplName+".yaml")

	tmpl := buildTemplate(tmplName, "civo", template.TemplateWorkflowsSpec{
		BeforeCreate: []string{"before-wf"},
		OnCreated:    []string{"on-created-wf"},
		OnDestroy:    []string{"on-destroy-wf"},
		AfterDelete:  []string{"after-wf"},
	})

	data, err := yaml.Marshal(tmpl)
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

	var loaded template.Template
	if err := yaml.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	assertStrings(t, "BeforeCreate", loaded.Spec.Workflows.BeforeCreate, "before-wf")
	assertStrings(t, "OnCreated", loaded.Spec.Workflows.OnCreated, "on-created-wf")
	assertStrings(t, "OnDestroy", loaded.Spec.Workflows.OnDestroy, "on-destroy-wf")
	assertStrings(t, "AfterDelete", loaded.Spec.Workflows.AfterDelete, "after-wf")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertStrings(t *testing.T, field string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: expected %v, got %v", field, want, got)
		return
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("%s[%d]: expected %q, got %q", field, i, w, got[i])
		}
	}
}
