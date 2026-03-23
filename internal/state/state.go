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
	Mode         ReconcileMode `yaml:"mode"`
	StrictDelete bool          `yaml:"strictDelete"`
}

// RepoConfig represents the repository-level Hyve configuration stored in hyve.yaml
type RepoConfig struct {
	Reconcile ReconcileConfig `yaml:"reconcile"`
}

// Manager handles state file operations using Git repositories
type Manager struct {
	stateDir   string
	gitManager *git.SystemBackend
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
