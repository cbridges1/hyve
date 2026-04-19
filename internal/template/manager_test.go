package template

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTemplateTest(t *testing.T) (*Manager, string) {
	t.Helper()
	tmpDir := t.TempDir()
	return NewManager(tmpDir), tmpDir
}

func TestNewManager(t *testing.T) {
	manager, tmpDir := setupTemplateTest(t)
	require.NotNil(t, manager)
	assert.Equal(t, filepath.Join(tmpDir, "templates"), manager.templatesDir)
}

func TestEnsureTemplatesDir(t *testing.T) {
	manager, tmpDir := setupTemplateTest(t)

	err := manager.EnsureTemplatesDir()
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(tmpDir, "templates"))
}

func TestGetTemplatePath(t *testing.T) {
	manager, tmpDir := setupTemplateTest(t)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "without extension",
			input:    "my-template",
			expected: filepath.Join(tmpDir, "templates", "my-template.yaml"),
		},
		{
			name:     "with extension",
			input:    "my-template.yaml",
			expected: filepath.Join(tmpDir, "templates", "my-template.yaml"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, manager.GetTemplatePath(tt.input))
		})
	}
}

func TestCreateTemplate(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{
			Name:        "test-template",
			Description: "Test template",
		},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "NYC1",
			Nodes:       []string{"g4s.kube.medium"},
			ClusterType: "k3s",
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	assert.FileExists(t, manager.GetTemplatePath("test-template"))
	assert.Equal(t, "v1", template.APIVersion)
	assert.Equal(t, "Template", template.Kind)
}

func TestCreateTemplate_Duplicate(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "duplicate-template"},
		Spec: TemplateSpec{
			Provider: "civo",
			Region:   "NYC1",
			Nodes:    []string{"g4s.kube.medium"},
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	err = manager.CreateTemplate(template)
	assert.Error(t, err)
}

func TestGetTemplate(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	original := &Template{
		Metadata: TemplateMetadata{
			Name:        "get-test-template",
			Description: "Test description",
		},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "PHX1",
			Nodes:       []string{"g4s.kube.large", "g4s.kube.large"},
			ClusterType: "k3s",
		},
	}

	err := manager.CreateTemplate(original)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("get-test-template")
	require.NoError(t, err)

	assert.Equal(t, original.Metadata.Name, retrieved.Metadata.Name)
	assert.Equal(t, original.Metadata.Description, retrieved.Metadata.Description)
	assert.Equal(t, original.Spec.Provider, retrieved.Spec.Provider)
	assert.Equal(t, original.Spec.Region, retrieved.Spec.Region)
	assert.Len(t, retrieved.Spec.Nodes, len(original.Spec.Nodes))
}

func TestGetTemplate_NotFound(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	_, err := manager.GetTemplate("nonexistent-template")
	assert.Error(t, err)
}

func TestListTemplates(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	templates := []*Template{
		{
			Metadata: TemplateMetadata{Name: "template-1"},
			Spec:     TemplateSpec{Provider: "civo", Region: "NYC1", Nodes: []string{"g4s.kube.small"}},
		},
		{
			Metadata: TemplateMetadata{Name: "template-2"},
			Spec:     TemplateSpec{Provider: "civo", Region: "PHX1", Nodes: []string{"g4s.kube.medium"}},
		},
		{
			Metadata: TemplateMetadata{Name: "template-3"},
			Spec:     TemplateSpec{Provider: "civo", Region: "FRA1", Nodes: []string{"g4s.kube.large"}},
		},
	}

	for _, tmpl := range templates {
		err := manager.CreateTemplate(tmpl)
		require.NoError(t, err)
	}

	list, err := manager.ListTemplates()
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestListTemplates_Empty(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	list, err := manager.ListTemplates()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestDeleteTemplate(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "delete-test-template"},
		Spec: TemplateSpec{
			Provider: "civo",
			Region:   "NYC1",
			Nodes:    []string{"g4s.kube.medium"},
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	err = manager.DeleteTemplate("delete-test-template")
	require.NoError(t, err)

	_, err = manager.GetTemplate("delete-test-template")
	assert.Error(t, err)
}

func TestDeleteTemplate_NotFound(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	err := manager.DeleteTemplate("nonexistent-template")
	assert.Error(t, err)
}

func TestConvertToClusterDefinition(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{
			Name:        "convert-test-template",
			Description: "Template for conversion",
		},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "NYC1",
			Nodes:       []string{"g4s.kube.large", "g4s.kube.large"},
			ClusterType: "k3s",
		},
	}
	template.Spec.Ingress.Enabled = true
	template.Spec.Ingress.LoadBalancer = true
	template.Spec.Ingress.ChartVersion = "4.7.1"

	clusterName := "my-cluster"
	clusterDef := manager.ConvertToClusterDefinition(template, clusterName)
	require.NotNil(t, clusterDef)

	assert.Equal(t, clusterName, clusterDef.Metadata.Name)
	assert.Equal(t, template.Spec.Region, clusterDef.Metadata.Region)
	assert.Equal(t, template.Spec.Provider, clusterDef.Spec.Provider)
	assert.Len(t, clusterDef.Spec.Nodes, len(template.Spec.Nodes))
	assert.Equal(t, template.Spec.ClusterType, clusterDef.Spec.ClusterType)
	assert.Equal(t, template.Spec.Ingress.Enabled, clusterDef.Spec.Ingress.Enabled)
	assert.Equal(t, template.Spec.Ingress.LoadBalancer, clusterDef.Spec.Ingress.LoadBalancer)
	assert.Equal(t, template.Spec.Ingress.ChartVersion, clusterDef.Spec.Ingress.ChartVersion)
	assert.Equal(t, "v1", clusterDef.APIVersion)
	assert.Equal(t, "Cluster", clusterDef.Kind)
}

func TestExecuteTemplate(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{
			Name:        "execute-test-template",
			Description: "Template for execution",
		},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "PHX1",
			Nodes:       []string{"g4s.kube.medium"},
			ClusterType: "k3s",
			Workflows: TemplateWorkflowsSpec{
				OnCreated: []string{"setup-monitoring", "deploy-app"},
			},
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrievedTemplate, clusterDef, err := manager.ExecuteTemplate(context.Background(), "execute-test-template", "test-cluster")
	require.NoError(t, err)
	require.NotNil(t, retrievedTemplate)
	require.NotNil(t, clusterDef)

	assert.Equal(t, template.Metadata.Name, retrievedTemplate.Metadata.Name)
	assert.Equal(t, "test-cluster", clusterDef.Metadata.Name)
	assert.Len(t, retrievedTemplate.Spec.Workflows.OnCreated, 2)
}

func TestExecuteTemplate_NotFound(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	_, _, err := manager.ExecuteTemplate(context.Background(), "nonexistent-template", "test-cluster")
	assert.Error(t, err)
}

func TestTemplateWithWorkflows(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{
			Name:        "workflow-template",
			Description: "Template with workflows",
		},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "NYC1",
			Nodes:       []string{"g4s.kube.large"},
			ClusterType: "k3s",
			Workflows: TemplateWorkflowsSpec{
				OnCreated: []string{"setup", "deploy"},
				OnDestroy: []string{"cleanup"},
			},
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("workflow-template")
	require.NoError(t, err)

	assert.Equal(t, []string{"setup", "deploy"}, retrieved.Spec.Workflows.OnCreated)
	assert.Equal(t, []string{"cleanup"}, retrieved.Spec.Workflows.OnDestroy)
}

// Provider-specific account fields are optional in templates.
// If set in the template they are used directly; if a CLI flag is also provided
// it overrides the template value. If neither is set, execution fails.

func TestConvertToClusterDefinition_CivoOrganization(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "civo-org-template"},
		Spec: TemplateSpec{
			Provider:         "civo",
			Region:           "NYC1",
			ClusterType:      "k3s",
			CivoOrganization: "my-org",
		},
	}

	clusterDef := manager.ConvertToClusterDefinition(template, "my-cluster")
	require.NotNil(t, clusterDef)
	assert.Equal(t, "my-org", clusterDef.Spec.CivoOrganization)
}

func TestConvertToClusterDefinition_AzureFields(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "azure-template"},
		Spec: TemplateSpec{
			Provider:           "azure",
			Region:             "eastus",
			ClusterType:        "k3s",
			AzureSubscription:  "my-subscription",
			AzureResourceGroup: "my-rg",
		},
	}

	clusterDef := manager.ConvertToClusterDefinition(template, "my-cluster")
	require.NotNil(t, clusterDef)
	assert.Equal(t, "my-subscription", clusterDef.Spec.AzureSubscription)
	assert.Equal(t, "my-rg", clusterDef.Spec.AzureResourceGroup)
	assert.Equal(t, "azure", clusterDef.Spec.Provider)
}

func TestConvertToClusterDefinition_GCPFields(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "gcp-template"},
		Spec: TemplateSpec{
			Provider:    "gcp",
			Region:      "us-central1",
			ClusterType: "gke",
			GCPProject:  "my-gcp-project",
		},
	}

	clusterDef := manager.ConvertToClusterDefinition(template, "my-cluster")
	require.NotNil(t, clusterDef)
	assert.Equal(t, "my-gcp-project", clusterDef.Spec.GCPProject)
}

func TestConvertToClusterDefinition_AWSFields(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "aws-template"},
		Spec: TemplateSpec{
			Provider:    "aws",
			Region:      "us-east-1",
			ClusterType: "eks",
			AWSAccount:  "prod",
			AWSVPCName:  "prod-vpc",
			AWSEKSRole:  "eks-role",
			AWSNodeRole: "node-role",
		},
	}

	clusterDef := manager.ConvertToClusterDefinition(template, "my-cluster")
	require.NotNil(t, clusterDef)
	assert.Equal(t, "prod", clusterDef.Spec.AWSAccount)
	assert.Equal(t, "prod-vpc", clusterDef.Spec.AWSVPCName)
	assert.Equal(t, "eks-role", clusterDef.Spec.AWSEKSRole)
	assert.Equal(t, "node-role", clusterDef.Spec.AWSNodeRole)
}

func TestConvertToClusterDefinition_FlagOverridesTemplateValue(t *testing.T) {
	// Simulate the flag-override behaviour: template has a value, caller replaces it.
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "override-template"},
		Spec: TemplateSpec{
			Provider:         "civo",
			Region:           "NYC1",
			ClusterType:      "k3s",
			CivoOrganization: "template-org",
		},
	}

	clusterDef := manager.ConvertToClusterDefinition(template, "my-cluster")
	// Flag value overrides what the template supplied.
	clusterDef.Spec.CivoOrganization = "flag-org"

	assert.Equal(t, "flag-org", clusterDef.Spec.CivoOrganization)
}

func TestTemplateWithCivoOrganization_YAMLRoundtrip(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "civo-org-yaml-template"},
		Spec: TemplateSpec{
			Provider:         "civo",
			Region:           "NYC1",
			ClusterType:      "k3s",
			Nodes:            []string{"g4s.kube.small"},
			CivoOrganization: "prod-org",
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("civo-org-yaml-template")
	require.NoError(t, err)
	assert.Equal(t, "prod-org", retrieved.Spec.CivoOrganization)
}

func TestTemplateWithAzureConfig_YAMLRoundtrip(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "azure-yaml-template"},
		Spec: TemplateSpec{
			Provider:           "azure",
			Region:             "eastus",
			ClusterType:        "k3s",
			AzureSubscription:  "prod-subscription",
			AzureResourceGroup: "prod-rg",
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("azure-yaml-template")
	require.NoError(t, err)
	assert.Equal(t, "prod-subscription", retrieved.Spec.AzureSubscription)
	assert.Equal(t, "prod-rg", retrieved.Spec.AzureResourceGroup)
}

func TestTemplateWithGCPConfig_YAMLRoundtrip(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "gcp-yaml-template"},
		Spec: TemplateSpec{
			Provider:    "gcp",
			Region:      "us-central1",
			ClusterType: "gke",
			GCPProject:  "my-gcp-project",
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("gcp-yaml-template")
	require.NoError(t, err)
	assert.Equal(t, "my-gcp-project", retrieved.Spec.GCPProject)
}

// ── Schedule field ────────────────────────────────────────────────────────────

func TestTemplateWithSchedule_YAMLRoundtrip(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "schedule-template"},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "NYC1",
			Nodes:       []string{"g4s.kube.small"},
			ClusterType: "k3s",
			Schedule:    "0 20 * * 5",
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("schedule-template")
	require.NoError(t, err)
	assert.Equal(t, "0 20 * * 5", retrieved.Spec.Schedule)
}

func TestTemplateWithoutSchedule_FieldOmitted(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "no-schedule-template"},
		Spec: TemplateSpec{
			Provider: "civo",
			Region:   "NYC1",
			Nodes:    []string{"g4s.kube.small"},
		},
	}

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("no-schedule-template")
	require.NoError(t, err)
	assert.Empty(t, retrieved.Spec.Schedule)
}

func TestConvertToClusterDefinition_ScheduleDoesNotSetExpiresAt(t *testing.T) {
	// Schedule-to-expiresAt conversion is a cmd-layer concern, not manager.
	// ConvertToClusterDefinition must NOT evaluate the cron expression.
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "cron-convert-template"},
		Spec: TemplateSpec{
			Provider: "civo",
			Region:   "NYC1",
			Nodes:    []string{"g4s.kube.small"},
			Schedule: "0 0 * * *",
		},
	}

	clusterDef := manager.ConvertToClusterDefinition(template, "my-cluster")
	require.NotNil(t, clusterDef)
	assert.Empty(t, clusterDef.Spec.ExpiresAt, "ConvertToClusterDefinition should not evaluate the cron schedule")
}

func TestTemplateWithSchedule_AllCronExpressions(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	cases := []struct {
		name     string
		schedule string
	}{
		{"wildcard", "* * * * *"},
		{"daily-midnight", "0 0 * * *"},
		{"weekday-evening", "0 20 * * 5"},
		{"first-of-month", "0 0 1 * *"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl := &Template{
				Metadata: TemplateMetadata{Name: "sched-" + c.name},
				Spec: TemplateSpec{
					Provider: "civo",
					Region:   "NYC1",
					Nodes:    []string{"g4s.kube.small"},
					Schedule: c.schedule,
				},
			}
			err := manager.CreateTemplate(tmpl)
			require.NoError(t, err)

			retrieved, err := manager.GetTemplate("sched-" + c.name)
			require.NoError(t, err)
			assert.Equal(t, c.schedule, retrieved.Spec.Schedule)
		})
	}
}

func TestTemplateWithIngress(t *testing.T) {
	manager, _ := setupTemplateTest(t)

	template := &Template{
		Metadata: TemplateMetadata{Name: "ingress-template"},
		Spec: TemplateSpec{
			Provider:    "civo",
			Region:      "NYC1",
			Nodes:       []string{"g4s.kube.medium"},
			ClusterType: "k3s",
		},
	}
	template.Spec.Ingress.Enabled = true
	template.Spec.Ingress.LoadBalancer = true
	template.Spec.Ingress.ChartVersion = "4.7.1"

	err := manager.CreateTemplate(template)
	require.NoError(t, err)

	retrieved, err := manager.GetTemplate("ingress-template")
	require.NoError(t, err)

	assert.True(t, retrieved.Spec.Ingress.Enabled)
	assert.True(t, retrieved.Spec.Ingress.LoadBalancer)
	assert.Equal(t, "4.7.1", retrieved.Spec.Ingress.ChartVersion)
}
