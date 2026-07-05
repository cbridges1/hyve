package state

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/git"
	"github.com/cbridges1/hyve/internal/types"
)

// ReconcileMode represents how reconciliation should be executed
type ReconcileMode string

const (
	// ReconcileModeLocal performs reconciliation on the local machine (default)
	ReconcileModeLocal ReconcileMode = "local"
	// ReconcileModeCICD skips local reconciliation, deferring it to a CI/CD pipeline
	ReconcileModeCICD ReconcileMode = "cicd"
)

// ReconcileConfig holds reconciliation configuration from the repository
type ReconcileConfig struct {
	Mode                 ReconcileMode `yaml:"mode" json:"mode"`
	StrictDelete         bool          `yaml:"strictDelete" json:"strictDelete"`
	StrictResourceDelete bool          `yaml:"strictResourceDelete" json:"strictResourceDelete"`
}

// ServerAuthMode selects how hyve-server validates incoming requests.
type ServerAuthMode string

const (
	// ServerAuthModeNone requires no Authorization header; the server binds
	// to 127.0.0.1 only unless --require-auth is also set.
	ServerAuthModeNone ServerAuthMode = "none"
	// ServerAuthModeForward delegates the entire validity decision to an
	// external HTTP endpoint (server.auth.forward.validateUrl) — hyve never
	// inspects, decodes, or has any opinion about the credential's format.
	ServerAuthModeForward ServerAuthMode = "forward"
)

// ForwardAuthConfig configures forward-auth mode: the incoming Authorization
// header is relayed as-is to ValidateURL; a 2xx response lets the request
// through, anything else (or a network error/timeout) rejects it with 401.
type ForwardAuthConfig struct {
	// ValidateURL is read from HYVE_AUTH_VALIDATE_URL when unset here.
	ValidateURL string `yaml:"validateUrl,omitempty" json:"validateUrl,omitempty"`
	// Timeout is a Go duration string (e.g. "3s"), read from
	// HYVE_AUTH_VALIDATE_TIMEOUT when unset here. Defaults to 3s.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// ServerAuthConfig is the server.auth section of hyve.yaml.
type ServerAuthConfig struct {
	Mode    ServerAuthMode    `yaml:"mode,omitempty" json:"mode,omitempty"`
	Forward ForwardAuthConfig `yaml:"forward,omitempty" json:"forward,omitempty"`
}

// ServerConfig is the server section of hyve.yaml — configuration for
// `hyve serve`. All fields are optional.
type ServerConfig struct {
	// Port hyve serve listens on. Also read from --port / HYVE_PORT.
	Port int `yaml:"port,omitempty" json:"port,omitempty"`
	// FrontendUrl is opened (with ?server=<addr> appended) by `hyve open`.
	// If empty, hyve open opens http://localhost:<port> directly.
	FrontendUrl string           `yaml:"frontendUrl,omitempty" json:"frontendUrl,omitempty"`
	Auth        ServerAuthConfig `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// RepoConfig represents the repository-level Hyve configuration stored in hyve.yaml
type RepoConfig struct {
	Reconcile ReconcileConfig `yaml:"reconcile" json:"reconcile"`
	Server    ServerConfig    `yaml:"server,omitempty" json:"server,omitempty"`
}

// Manager handles state file operations using Git repositories
type Manager struct {
	stateDir   string
	gitManager *git.SystemBackend
}

// NewManagerFromPath creates a Manager backed by an existing local directory
// (no git remote required). stateDir must be the clusters/ subdirectory path;
// GetStateRoot() will return its parent.
func NewManagerFromPath(stateDir string) *Manager {
	return &Manager{stateDir: stateDir}
}

// NewManager creates a new state manager with Git repository support
func NewManager(gitRepoURL, localPath, username, token string) (*Manager, error) {
	gitMgr, err := git.NewBackend(gitRepoURL, localPath, username, token)
	if err != nil {
		return nil, fmt.Errorf("failed to create git backend: %w", err)
	}

	return &Manager{
		stateDir:   gitMgr.GetStateDir(),
		gitManager: gitMgr,
	}, nil
}

// LocalPath returns the root directory of the local repository checkout.
func (m *Manager) LocalPath() string {
	return filepath.Dir(m.stateDir)
}

// InitializeGitRepo initializes or clones the Git repository
func (m *Manager) InitializeGitRepo(ctx context.Context) error {
	return m.gitManager.InitializeRepo(ctx)
}

// SyncWithRemote pulls latest changes from the remote repository
func (m *Manager) SyncWithRemote(ctx context.Context) error {
	return m.gitManager.Pull(ctx)
}

// CommitAndPush commits changes and pushes to remote repository
func (m *Manager) CommitAndPush(ctx context.Context, message string) error {
	if err := m.gitManager.Commit(ctx, message); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	if err := m.gitManager.Push(ctx); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}

// GetStateRoot returns the root directory of the state repository (the parent of the
// clusters/ directory). Provider config files live here under provider-configs/.
func (m *Manager) GetStateRoot() string {
	return filepath.Dir(m.stateDir)
}

// LoadRepoConfig reads hyve.yaml from the repository root.
// If the file does not exist, a default config with local mode is returned.
func (m *Manager) LoadRepoConfig() (*RepoConfig, error) {
	configPath := filepath.Join(filepath.Dir(m.stateDir), "hyve.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &RepoConfig{Reconcile: ReconcileConfig{Mode: ReconcileModeLocal}}, nil
		}
		return nil, fmt.Errorf("failed to read hyve.yaml: %w", err)
	}

	var cfg RepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse hyve.yaml: %w", err)
	}

	if cfg.Reconcile.Mode == "" {
		cfg.Reconcile.Mode = ReconcileModeLocal
	}

	return &cfg, nil
}

// SaveRepoConfig writes cfg back to hyve.yaml at the repository root. The
// caller is responsible for committing the change if it should be pushed to
// the remote. It does not restart the server — a change to cfg.Server only
// takes effect on the next `hyve serve` invocation.
func (m *Manager) SaveRepoConfig(cfg *RepoConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal hyve.yaml: %w", err)
	}
	configPath := filepath.Join(filepath.Dir(m.stateDir), "hyve.yaml")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write hyve.yaml: %w", err)
	}
	return nil
}

// SaveClusterDefinition writes a cluster definition back to its YAML file in the
// state directory. Only serializable fields are written (runtime-only yaml:"-"
// fields such as AWSEKSRoleARN are not persisted). The caller is responsible for
// committing the change if it should be pushed to the remote.
func (m *Manager) SaveClusterDefinition(def *types.ClusterDefinition) error {
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster definition: %w", err)
	}
	path := filepath.Join(m.stateDir, def.Metadata.Name+".yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write cluster definition: %w", err)
	}
	return nil
}

// RemoveClusterFile finds and removes the YAML file containing the given cluster
// definition. The caller is responsible for committing the deletion.
func (m *Manager) RemoveClusterFile(clusterName string) error {
	var found string

	err := filepath.WalkDir(m.stateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var cluster types.ClusterDefinition
		if err := yaml.Unmarshal(data, &cluster); err != nil {
			return nil
		}
		if cluster.Metadata.Name == clusterName {
			found = path
			return filepath.SkipAll
		}
		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to search for cluster file: %w", err)
	}
	if found == "" {
		return fmt.Errorf("no YAML file found for cluster %s", clusterName)
	}

	if err := os.Remove(found); err != nil {
		return fmt.Errorf("failed to remove cluster file %s: %w", found, err)
	}

	return nil
}

// LoadClusterDefinitions loads all cluster definitions from YAML files
func (m *Manager) LoadClusterDefinitions() ([]types.ClusterDefinition, error) {
	var clusters []types.ClusterDefinition

	err := filepath.WalkDir(m.stateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		var cluster types.ClusterDefinition
		if err := yaml.Unmarshal(data, &cluster); err != nil {
			return fmt.Errorf("failed to unmarshal YAML file %s: %w", path, err)
		}

		clusters = append(clusters, cluster)
		return nil
	})

	if err != nil {
		if os.IsNotExist(err) {
			// clusters/ directory doesn't exist — treat as empty desired state.
			// ReconcileAll will still run strictDelete if enabled.
			return nil, nil
		}
		return nil, err
	}

	return clusters, nil
}

// ValidateClusterDefinitions validates cluster definitions
func (m *Manager) ValidateClusterDefinitions(clusters []types.ClusterDefinition) error {
	// Basic validation can be added here if needed in the future
	return nil
}

// OrderClusters returns clusters in their original order
func (m *Manager) OrderClusters(clusters []types.ClusterDefinition) []types.ClusterDefinition {
	return clusters
}
