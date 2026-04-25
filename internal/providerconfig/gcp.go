package providerconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// GCPProject represents a named GCP project
type GCPProject struct {
	Name            string `yaml:"name"`
	ProjectID       string `yaml:"project_id"`
	CredentialsJSON string `yaml:"credentials_json,omitempty"`
}

// GCPConfig represents GCP-specific configuration (aggregated view across all project files)
type GCPConfig struct {
	Projects []GCPProject `yaml:"projects"`
}

// LoadGCPProject reads the config file for a single named GCP project.
// Returns (nil, nil) when no file exists for that project.
func (m *Manager) LoadGCPProject(name string) (*GCPProject, error) {
	path := m.getAccountConfigPath("gcp", name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read GCP project config: %w", err)
	}
	var proj GCPProject
	if err := yaml.Unmarshal(data, &proj); err != nil {
		return nil, fmt.Errorf("failed to parse GCP project config: %w", err)
	}
	return &proj, nil
}

// SaveGCPProject writes the config for a single GCP project to its own file.
func (m *Manager) SaveGCPProject(proj *GCPProject) error {
	if err := m.ensureProviderDir("gcp"); err != nil {
		return err
	}
	data, err := yaml.Marshal(proj)
	if err != nil {
		return fmt.Errorf("failed to marshal GCP project config: %w", err)
	}
	path := m.getAccountConfigPath("gcp", proj.Name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write GCP project config: %w", err)
	}
	return nil
}

// LoadGCPConfig reads all per-project files under provider-configs/gcp/ and
// returns them as a single aggregated GCPConfig.
func (m *Manager) LoadGCPConfig() (*GCPConfig, error) {
	dir := m.getProviderDir("gcp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &GCPConfig{Projects: []GCPProject{}}, nil
		}
		return nil, fmt.Errorf("failed to read GCP config directory: %w", err)
	}

	config := &GCPConfig{Projects: []GCPProject{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", e.Name(), err)
		}
		var proj GCPProject
		if err := yaml.Unmarshal(data, &proj); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", e.Name(), err)
		}
		config.Projects = append(config.Projects, proj)
	}
	return config, nil
}

// AddGCPProject adds or updates a named GCP project.
func (m *Manager) AddGCPProject(name, projectID string) error {
	proj, err := m.LoadGCPProject(name)
	if err != nil {
		return err
	}
	if proj == nil {
		proj = &GCPProject{Name: name}
	}
	proj.ProjectID = projectID
	return m.SaveGCPProject(proj)
}

// RemoveGCPProject removes a project by deleting its config file.
func (m *Manager) RemoveGCPProject(name string) error {
	path := m.getAccountConfigPath("gcp", name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("project '%s' not found", name)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove GCP project config: %w", err)
	}
	return nil
}

// GetGCPProjectID returns the project ID for a given name/alias
func (m *Manager) GetGCPProjectID(name string) (string, error) {
	proj, err := m.LoadGCPProject(name)
	if err != nil {
		return "", err
	}
	if proj == nil {
		return "", fmt.Errorf("GCP project '%s' not found in repository configuration", name)
	}
	return proj.ProjectID, nil
}

// ListGCPProjects returns all configured GCP projects
func (m *Manager) ListGCPProjects() ([]GCPProject, error) {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return nil, err
	}
	return config.Projects, nil
}

// HasGCPProject checks if a project with the given name exists
func (m *Manager) HasGCPProject(name string) (bool, error) {
	proj, err := m.LoadGCPProject(name)
	if err != nil {
		return false, err
	}
	return proj != nil, nil
}
