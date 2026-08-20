package template

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	k8syaml "sigs.k8s.io/yaml"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// decodeTemplate parses a template file's bytes, validates its apiVersion/
// kind against the real Template CRD, and converts it into the in-memory
// Template shape.
func decodeTemplate(path string, data []byte) (*Template, error) {
	var cr hyvev1alpha1.Template
	if err := k8syaml.Unmarshal(data, &cr); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if cr.APIVersion != APIVersion || cr.Kind != Kind {
		return nil, fmt.Errorf("%s: apiVersion/kind must be %q/%q, got %q/%q",
			path, APIVersion, Kind, cr.APIVersion, cr.Kind)
	}
	return toTemplate(&cr), nil
}

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

	template.APIVersion = APIVersion
	template.Kind = Kind

	data, err := k8syaml.Marshal(fromTemplate(template))
	if err != nil {
		return fmt.Errorf("failed to marshal template: %w", err)
	}

	if err := os.WriteFile(templatePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}

	return nil
}

// findTemplateFile scans the templates directory and returns the file path
// of the template whose metadata.name matches the given name. A file that
// fails to decode (e.g. a leftover pre-hyve.io/v1alpha1 file — see
// decodeTemplate) doesn't abort the scan, since it might just be an
// unrelated file — but if the target name is never found, its decode
// errors are surfaced rather than silently dropped, so a real "not found"
// isn't confused with "found, but unreadable."
func (m *Manager) findTemplateFile(name string) (string, error) {
	entries, err := os.ReadDir(m.templatesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("template '%s' not found", name)
		}
		return "", fmt.Errorf("failed to read templates directory: %w", err)
	}

	var decodeErrs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(m.templatesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			decodeErrs = append(decodeErrs, err)
			continue
		}
		t, err := decodeTemplate(path, data)
		if err != nil {
			decodeErrs = append(decodeErrs, err)
			continue
		}
		if t.Metadata.Name == name {
			return path, nil
		}
	}

	if len(decodeErrs) > 0 {
		msgs := make([]string, len(decodeErrs))
		for i, e := range decodeErrs {
			msgs[i] = "  " + e.Error()
		}
		return "", fmt.Errorf("template '%s' not found; %d file(s) in %s could not be read:\n%s",
			name, len(decodeErrs), m.templatesDir, strings.Join(msgs, "\n"))
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

	return decodeTemplate(path, data)
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

		path := filepath.Join(m.templatesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		t, err := decodeTemplate(path, data)
		if err != nil {
			continue
		}
		t.Filename = entry.Name()
		templates = append(templates, t)
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
// optionally overriding region and any params via the overrides map — via
// hyvev1alpha1.RenderClusterDefinitionSpec, the one place this rendering
// logic lives, shared with cluster mode's template-render API endpoints.
func (t *Template) GenerateClusterDefinition(name, region string, overrides map[string]string) types.ClusterDefinition {
	spec := hyvev1alpha1.RenderClusterDefinitionSpec(fromTemplate(t).Spec, region, overrides)
	cr := hyvev1alpha1.ClusterDefinition{
		TypeMeta:   metav1.TypeMeta{APIVersion: hyvev1alpha1.GroupVersion.String(), Kind: "ClusterDefinition"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
	return crdconv.ToTypesClusterDefinition(&cr)
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
