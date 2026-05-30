package providerconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// GCPProject represents a named GCP project
type GCPProject struct {
	Name            string `yaml:"name"`
	ProjectID       string `yaml:"project_id"`
	CredentialsJSON string `yaml:"credentials_json,omitempty"`
}

// GCPConfig represents GCP-specific configuration
type GCPConfig struct {
	Projects []GCPProject `yaml:"projects"`
}

// LoadGCPConfig reads provider-configs/gcp.yaml and returns the full config.
func (m *Manager) LoadGCPConfig() (*GCPConfig, error) {
	data, err := os.ReadFile(m.getConfigPath("gcp"))
	if err != nil {
		if os.IsNotExist(err) {
			return &GCPConfig{Projects: []GCPProject{}}, nil
		}
		return nil, fmt.Errorf("failed to read GCP config: %w", err)
	}
	config := &GCPConfig{Projects: []GCPProject{}}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse GCP config: %w", err)
	}
	return config, nil
}

// SaveGCPConfig writes the full GCP config to provider-configs/gcp.yaml.
func (m *Manager) SaveGCPConfig(config *GCPConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal GCP config: %w", err)
	}
	if err := os.WriteFile(m.getConfigPath("gcp"), data, 0644); err != nil {
		return fmt.Errorf("failed to write GCP config: %w", err)
	}
	return nil
}

// LoadGCPProject reads the config for a single named GCP project.
// Returns (nil, nil) when no project with that name exists.
func (m *Manager) LoadGCPProject(name string) (*GCPProject, error) {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return nil, err
	}
	for i := range config.Projects {
		if config.Projects[i].Name == name {
			return &config.Projects[i], nil
		}
	}
	return nil, nil
}

// SaveGCPProject upserts a project entry in provider-configs/gcp.yaml.
func (m *Manager) SaveGCPProject(proj *GCPProject) error {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return err
	}
	for i := range config.Projects {
		if config.Projects[i].Name == proj.Name {
			config.Projects[i] = *proj
			return m.SaveGCPConfig(config)
		}
	}
	config.Projects = append(config.Projects, *proj)
	return m.SaveGCPConfig(config)
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

// RemoveGCPProject removes a project by name.
func (m *Manager) RemoveGCPProject(name string) error {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return err
	}
	filtered := []GCPProject{}
	found := false
	for _, p := range config.Projects {
		if p.Name != name {
			filtered = append(filtered, p)
		} else {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("project '%s' not found", name)
	}
	config.Projects = filtered
	return m.SaveGCPConfig(config)
}

// GetGCPProjectID returns the project ID for a given name.
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

// ListGCPProjects returns all configured GCP projects.
func (m *Manager) ListGCPProjects() ([]GCPProject, error) {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return nil, err
	}
	return config.Projects, nil
}

// HasGCPProject checks if a project with the given name exists.
func (m *Manager) HasGCPProject(name string) (bool, error) {
	proj, err := m.LoadGCPProject(name)
	if err != nil {
		return false, err
	}
	return proj != nil, nil
}
