package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"hyve/internal/types"
)

// Manager handles cluster template operations
type Manager struct {
	templatesDir string
}

// NewManager creates a new template manager
func NewManager(repoPath string) *Manager {
	return &Manager{
		templatesDir: filepath.Join(repoPath, "templates"),
	}
}

// EnsureTemplatesDir ensures the templates directory exists
func (m *Manager) EnsureTemplatesDir() error {
	return os.MkdirAll(m.templatesDir, 0755)
}

// GetTemplatePath returns the path to a template file
func (m *Manager) GetTemplatePath(name string) string {
	// Ensure .yaml extension
	if !strings.HasSuffix(name, ".yaml") {
		name = name + ".yaml"
	}
	return filepath.Join(m.templatesDir, name)
}

// CreateTemplate creates a new template file
func (m *Manager) CreateTemplate(template *Template) error {
	if err := m.EnsureTemplatesDir(); err != nil {
		return fmt.Errorf("failed to ensure templates directory: %w", err)
	}

	templatePath := m.GetTemplatePath(template.Metadata.Name)

	// Check if template already exists
	if _, err := os.Stat(templatePath); err == nil {
		return fmt.Errorf("template '%s' already exists", template.Metadata.Name)
	}

	// Set defaults
	if template.APIVersion == "" {
		template.APIVersion = "v1"
	}
	if template.Kind == "" {
		template.Kind = "Template"
	}

	// Marshal to YAML
	data, err := yaml.Marshal(template)
	if err != nil {
		return fmt.Errorf("failed to marshal template: %w", err)
	}

	// Write to file
	if err := os.WriteFile(templatePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}

	return nil
}

// findTemplateFile scans the templates directory and returns the file path
// of the template whose metadata.name matches the given name.
func (m *Manager) findTemplateFile(name string) (string, error) {
	entries, err := os.ReadDir(m.templatesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("template '%s' not found", name)
		}
		return "", fmt.Errorf("failed to read templates directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(m.templatesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t Template
		if err := yaml.Unmarshal(data, &t); err != nil {
			continue
		}
		if t.Metadata.Name == name {
			return path, nil
		}
	}
	return "", fmt.Errorf("template '%s' not found", name)
}

// GetTemplate reads a template from disk by metadata.name.
func (m *Manager) GetTemplate(name string) (*Template, error) {
	path, err := m.findTemplateFile(name)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template: %w", err)
	}

	var template Template
	if err := yaml.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	return &template, nil
}

// ListTemplates lists all available templates
func (m *Manager) ListTemplates() ([]*Template, error) {
	if _, err := os.Stat(m.templatesDir); os.IsNotExist(err) {
		return []*Template{}, nil
	}

	entries, err := os.ReadDir(m.templatesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read templates directory: %w", err)
	}

	var templates []*Template
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(m.templatesDir, entry.Name()))
		if err != nil {
			continue
		}
		var t Template
		if err := yaml.Unmarshal(data, &t); err != nil {
			continue
		}
		t.Filename = entry.Name()
		templates = append(templates, &t)
	}

	return templates, nil
}

// DeleteTemplate deletes a template file by metadata.name.
func (m *Manager) DeleteTemplate(name string) error {
	path, err := m.findTemplateFile(name)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete template: %w", err)
	}

	return nil
}

// ConvertToClusterDefinition converts a template to a cluster definition.
// Provider-specific account fields are carried over from the template when present.
// The caller may override any of these by assigning to the returned ClusterDefinition
// before use (e.g. from CLI flags passed to `template execute`).
func (m *Manager) ConvertToClusterDefinition(template *Template, clusterName string) *types.ClusterDefinition {
	return &types.ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata: types.ClusterMetadata{
			Name:   clusterName,
			Region: template.Spec.Region,
		},
		Spec: types.ClusterSpec{
			Provider:    template.Spec.Provider,
			Nodes:       template.Spec.Nodes,
			NodeGroups:  template.Spec.NodeGroups,
			ClusterType: template.Spec.ClusterType,
			Ingress: types.IngressSpec{
				Enabled:      template.Spec.Ingress.Enabled,
				LoadBalancer: template.Spec.Ingress.LoadBalancer,
				ChartVersion: template.Spec.Ingress.ChartVersion,
			},
			Workflows: types.WorkflowsSpec{
				OnCreated: template.Spec.Workflows.OnCreated,
				OnDestroy: template.Spec.Workflows.OnDestroy,
			},
			// AWS-specific alias names (resolved to IDs during template execution)
			AWSAccount:  template.Spec.AWSAccount,
			AWSVPCName:  template.Spec.AWSVPCName,
			AWSEKSRole:  template.Spec.AWSEKSRole,
			AWSNodeRole: template.Spec.AWSNodeRole,
			// Azure-specific alias names
			AzureSubscription:  template.Spec.AzureSubscription,
			AzureResourceGroup: template.Spec.AzureResourceGroup,
			// GCP-specific alias names
			GCPProject: template.Spec.GCPProject,
			// Civo-specific alias names
			CivoOrganization: template.Spec.CivoOrganization,
		},
	}
}

// ExecuteTemplate creates a cluster from a template
func (m *Manager) ExecuteTemplate(ctx context.Context, templateName, clusterName string) (*Template, *types.ClusterDefinition, error) {
	// Get template
	template, err := m.GetTemplate(templateName)
	if err != nil {
		return nil, nil, err
	}

	// Convert to cluster definition
	clusterDef := m.ConvertToClusterDefinition(template, clusterName)

	return template, clusterDef, nil
}
