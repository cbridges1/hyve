package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/types"
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

	if _, err := os.Stat(templatePath); err == nil {
		return fmt.Errorf("template '%s' already exists", template.Metadata.Name)
	}

	if template.APIVersion == "" {
		template.APIVersion = "v1"
	}
	if template.Kind == "" {
		template.Kind = "Template"
	}

	data, err := yaml.Marshal(template)
	if err != nil {
		return fmt.Errorf("failed to marshal template: %w", err)
	}

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

// GenerateClusterDefinition produces a ClusterDefinition from a template,
// optionally overriding region and any params via the overrides map.
func (t *Template) GenerateClusterDefinition(name, region string, overrides map[string]string) types.ClusterDefinition {
	params := make(map[string]string, len(t.Spec.Params))
	for k, v := range t.Spec.Params {
		params[k] = v
	}
	for k, v := range overrides {
		params[k] = v
	}
	if region == "" {
		region = t.Spec.Region
	}
	return types.ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata:   types.ClusterMetadata{Name: name, Region: region},
		Spec: types.ClusterSpec{
			Driver: types.DriverRef{
				Source:  t.Spec.Driver.Source,
				Version: t.Spec.Driver.Version,
			},
			Params: params,
			Workflows: types.WorkflowsSpec{
				BeforeCreate: t.Spec.Workflows.BeforeCreate,
				OnCreate:     t.Spec.Workflows.OnCreate,
				AfterCreate:  t.Spec.Workflows.AfterCreate,
				OnDelete:     t.Spec.Workflows.OnDelete,
				AfterDelete:  t.Spec.Workflows.AfterDelete,
			},
			Resources: t.Spec.Resources,
		},
	}
}

// ExecuteTemplate creates a cluster definition from a template
func (m *Manager) ExecuteTemplate(ctx context.Context, templateName, clusterName string, region string, overrides map[string]string) (*Template, *types.ClusterDefinition, error) {
	template, err := m.GetTemplate(templateName)
	if err != nil {
		return nil, nil, err
	}
	def := template.GenerateClusterDefinition(clusterName, region, overrides)
	return template, &def, nil
}
