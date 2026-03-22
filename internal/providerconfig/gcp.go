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

// LoadGCPConfig loads the GCP configuration from the repository
func (m *Manager) LoadGCPConfig() (*GCPConfig, error) {
	configPath := m.getConfigPath("gcp")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &GCPConfig{Projects: []GCPProject{}}, nil
		}
		return nil, fmt.Errorf("failed to read GCP config: %w", err)
	}

	var config GCPConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse GCP config: %w", err)
	}

	return &config, nil
}

// SaveGCPConfig saves the GCP configuration to the repository
func (m *Manager) SaveGCPConfig(config *GCPConfig) error {
	if err := m.ensureConfigDir(); err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal GCP config: %w", err)
	}

	configPath := m.getConfigPath("gcp")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write GCP config: %w", err)
	}

	return nil
}

// AddGCPProject adds a named project to the GCP configuration
func (m *Manager) AddGCPProject(name, projectID string) error {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return err
	}

	for i, p := range config.Projects {
		if p.Name == name {
			config.Projects[i].ProjectID = projectID
			return m.SaveGCPConfig(config)
		}
	}

	config.Projects = append(config.Projects, GCPProject{
		Name:      name,
		ProjectID: projectID,
	})

	return m.SaveGCPConfig(config)
}

// RemoveGCPProject removes a project by name from the GCP configuration
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

// GetGCPProjectID returns the project ID for a given name/alias
func (m *Manager) GetGCPProjectID(name string) (string, error) {
	config, err := m.LoadGCPConfig()
	if err != nil {
		return "", err
	}

	for _, p := range config.Projects {
		if p.Name == name {
			return p.ProjectID, nil
		}
	}

	return "", fmt.Errorf("GCP project '%s' not found in repository configuration", name)
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
	config, err := m.LoadGCPConfig()
	if err != nil {
		return false, err
	}

	for _, p := range config.Projects {
		if p.Name == name {
			return true, nil
		}
	}

	return false, nil
}
