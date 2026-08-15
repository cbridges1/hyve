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

// EnvConfig is the env section of hyve.yaml — where to load local
// environment variables (Infisical bootstrap creds, cloud provider
// tokens, ...) from before any command runs.
type EnvConfig struct {
	// File is a path, relative to the repository root, to a dotenv-format
	// file to load. Defaults to ".env" (godotenv's own default) if this
	// whole section, or just this field, is left unset.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
}

// RepoConfig represents the repository-level Hyve configuration stored in hyve.yaml
type RepoConfig struct {
	Reconcile ReconcileConfig `yaml:"reconcile" json:"reconcile"`
	Env       EnvConfig       `yaml:"env,omitempty" json:"env,omitempty"`
}

// DefaultEnvFileName is what's loaded when hyve.yaml has no env.file set
// (or doesn't exist at all yet) — matches godotenv's own built-in default,
// preserving prior behavior for repos that never configured this.
const DefaultEnvFileName = ".env"

// ResolveEnvFile returns the dotenv file path to load for repoRoot,
// honoring hyve.yaml's env.file if set. Called from main() before any
// command runs and before a full Manager (which needs a resolved
// clusters/ path, git state, etc.) can be constructed — so this does a
// minimal, best-effort standalone read of hyve.yaml rather than going
// through LoadRepoConfig. Never errors: a missing or unparseable
// hyve.yaml just falls back to DefaultEnvFileName, the same as if this
// function didn't exist at all.
func ResolveEnvFile(repoRoot string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, "hyve.yaml"))
	if err != nil {
		return filepath.Join(repoRoot, DefaultEnvFileName)
	}
	var cfg RepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil || cfg.Env.File == "" {
		return filepath.Join(repoRoot, DefaultEnvFileName)
	}
	if filepath.IsAbs(cfg.Env.File) {
		return cfg.Env.File
	}
	return filepath.Join(repoRoot, cfg.Env.File)
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
// the remote.
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

// stateSidecarSuffix names the reconciler-owned sidecar file that holds
// ClusterState (DriverOutputs/AppliedResources) for a cluster. It
// deliberately still ends in ".yaml" (so it stays plain YAML,
// human-inspectable, and git-diffable). Sidecars live in sidecarDir(), a
// sibling of stateDir — never stateDir itself — precisely so `ls clusters/`
// only ever shows files a person wrote; the walks over stateDir in
// LoadClusterDefinitions/RemoveClusterFile still skip this suffix
// defensively (a stray or pre-migration sidecar left in stateDir would
// otherwise be unmarshaled as a bogus second ClusterDefinition with empty
// Metadata/Kind), but nothing relies on it as the primary separation
// mechanism anymore.
const stateSidecarSuffix = ".state.yaml"

func (m *Manager) clusterPath(name string) string {
	return filepath.Join(m.stateDir, name+".yaml")
}

// sidecarDir returns cluster-state/, a sibling of stateDir (clusters/) —
// keeping reconciler-owned bookkeeping (content hashes, timestamps,
// tracked-object lists) out of the directory a person actually edits.
func (m *Manager) sidecarDir() string {
	return filepath.Join(filepath.Dir(m.stateDir), "cluster-state")
}

func (m *Manager) sidecarPath(name string) string {
	return filepath.Join(m.sidecarDir(), name+stateSidecarSuffix)
}

// mergeSidecar overlays cluster-state/<name>.state.yaml onto cluster.Spec's
// DriverOutputs/AppliedResources, if that file exists. If it doesn't, those
// two fields are left exactly as yaml.Unmarshal decoded them from the
// primary file — which is what makes reading a pre-migration monolithic
// cluster file (inline appliedResources/driverOutputs, no sidecar yet) just
// work with no special-case migration code. Once a sidecar exists, it is
// authoritative and overwrites whatever came from the primary file.
func (m *Manager) mergeSidecar(cluster *types.ClusterDefinition) error {
	data, err := os.ReadFile(m.sidecarPath(cluster.Metadata.Name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st types.ClusterState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return err
	}
	cluster.Spec.DriverOutputs = st.DriverOutputs
	cluster.Spec.AppliedResources = st.AppliedResources
	return nil
}

// HasStateSidecar reports whether cluster-state/<name>.state.yaml exists.
func (m *Manager) HasStateSidecar(name string) bool {
	_, err := os.Stat(m.sidecarPath(name))
	return err == nil
}

// LoadClusterDefinition loads one cluster by name, merging its primary
// desired-state file with its state sidecar (if present) into a single
// in-memory ClusterDefinition. Returns the merged definition and the raw
// primary-file bytes (for byte-for-byte round-trip use, e.g. YAML content
// negotiation) — deliberately only the primary file's bytes, never a
// re-serialized merged view, so a caller that persists these bytes verbatim
// never reintroduces reconciler-owned fields into the human-edited file.
// The primary-file read's error is returned unwrapped so os.IsNotExist(err)
// checks at call sites keep working unmodified.
func (m *Manager) LoadClusterDefinition(name string) (*types.ClusterDefinition, []byte, error) {
	data, err := os.ReadFile(m.clusterPath(name))
	if err != nil {
		return nil, nil, err
	}
	var def types.ClusterDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, nil, fmt.Errorf("failed to parse cluster definition %s: %w", name, err)
	}
	if err := m.mergeSidecar(&def); err != nil {
		return nil, nil, fmt.Errorf("failed to merge state sidecar for %s: %w", name, err)
	}
	return &def, data, nil
}

// SaveClusterDefinition writes a cluster definition back to its primary YAML
// file in the state directory, and its reconciler-owned DriverOutputs /
// AppliedResources to a separate sidecar file (cluster-state/<name>.state.yaml,
// a sibling directory of stateDir) — keeping machine bookkeeping (content
// hashes, timestamps, tracked-object lists) out of the human-authored
// file's directory entirely, not just out of its git diff. def itself is left
// untouched: the split is performed on a shallow copy (safe because
// ClusterDefinition embeds ClusterSpec by value, not by pointer, so clearing
// the copy's map fields doesn't affect the caller's maps). If the resulting
// state is empty (e.g. a cluster with no resources yet), any existing
// sidecar file is removed rather than left as an empty file. The caller is
// responsible for committing the change if it should be pushed to the
// remote.
func (m *Manager) SaveClusterDefinition(def *types.ClusterDefinition) error {
	if err := os.MkdirAll(m.stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create clusters directory: %w", err)
	}

	state := types.ClusterState{
		DriverOutputs:    def.Spec.DriverOutputs,
		AppliedResources: def.Spec.AppliedResources,
	}

	primary := *def
	primary.Spec.DriverOutputs = nil
	primary.Spec.AppliedResources = nil

	data, err := yaml.Marshal(&primary)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster definition: %w", err)
	}
	if err := os.WriteFile(m.clusterPath(def.Metadata.Name), data, 0644); err != nil {
		return fmt.Errorf("failed to write cluster definition: %w", err)
	}

	sidecarPath := m.sidecarPath(def.Metadata.Name)
	if state.IsEmpty() {
		if err := os.Remove(sidecarPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove empty state sidecar: %w", err)
		}
		return nil
	}
	sdata, err := yaml.Marshal(&state)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster state sidecar: %w", err)
	}
	if err := os.MkdirAll(m.sidecarDir(), 0755); err != nil {
		return fmt.Errorf("failed to create cluster-state directory: %w", err)
	}
	if err := os.WriteFile(sidecarPath, sdata, 0644); err != nil {
		return fmt.Errorf("failed to write cluster state sidecar: %w", err)
	}
	return nil
}

// RemoveClusterFile finds and removes the primary YAML file (and its state
// sidecar, if any) for the given cluster. The caller is responsible for
// committing the deletion.
func (m *Manager) RemoveClusterFile(clusterName string) error {
	var found string

	err := filepath.WalkDir(m.stateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || strings.HasSuffix(name, stateSidecarSuffix) {
			return nil
		}
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
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

	if err := os.Remove(m.sidecarPath(clusterName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state sidecar for %s: %w", clusterName, err)
	}

	return nil
}

// LoadClusterDefinitions loads all cluster definitions from YAML files,
// merging each one's state sidecar (if present) — see mergeSidecar.
func (m *Manager) LoadClusterDefinitions() ([]types.ClusterDefinition, error) {
	var clusters []types.ClusterDefinition

	err := filepath.WalkDir(m.stateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		name := d.Name()
		if d.IsDir() || strings.HasSuffix(name, stateSidecarSuffix) {
			return nil
		}
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
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
		if err := m.mergeSidecar(&cluster); err != nil {
			return fmt.Errorf("failed to merge state sidecar for %s: %w", cluster.Metadata.Name, err)
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
