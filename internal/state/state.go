package state

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	k8syaml "sigs.k8s.io/yaml"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// RepoConfig represents the repository-level Hyve configuration stored in
// hyve.yaml — not a CRD-shaped resource, so this stays on gopkg.in/yaml.v3
// and its own hand-written shape, unlike ClusterDefinition below.
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

// Manager handles reading and writing cluster state to a local directory. It
// has no awareness of git at all — git sync (pulling latest, committing and
// pushing changes back) is not a native hyve capability; it's the caller's
// job, done via the plain `git` CLI directly or an equivalent workflow. See
// internal/reconcile.StateProvider.
type Manager struct {
	stateDir string
}

// NewManagerFromPath creates a Manager backed by an existing local directory.
// stateDir must be the clusters/ subdirectory path; GetStateRoot() will
// return its parent.
func NewManagerFromPath(stateDir string) *Manager {
	return &Manager{stateDir: stateDir}
}

// LocalPath returns the root directory of the local repository checkout.
func (m *Manager) LocalPath() string {
	return filepath.Dir(m.stateDir)
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

// stateSidecarSuffix names the reconciler-owned sidecar file that holds a
// ClusterDefinitionStatus (DriverOutputs/AppliedResources) for a cluster —
// the file-mode analogue of a real ClusterDefinition's status subresource.
// It deliberately still ends in ".yaml" (so it stays plain YAML,
// human-inspectable, and git-diffable). Sidecars live in sidecarDir(), a
// sibling of stateDir — never stateDir itself — precisely so `ls clusters/`
// only ever shows files a person wrote; the walks over stateDir in
// LoadClusterDefinitions/RemoveClusterFile still skip this suffix
// defensively (a stray sidecar left in stateDir would otherwise be
// unmarshaled as a bogus second ClusterDefinition with empty metadata), but
// nothing relies on it as the primary separation mechanism anymore.
const stateSidecarSuffix = ".state.yaml"

// clusterGroupVersion/clusterKind are the required apiVersion/kind for a
// primary clusters/<name>.yaml file — matching the real ClusterDefinition
// CRD exactly (group hyve.io/v1alpha1) is the whole point of this file
// format: the same YAML you author here is `kubectl apply -f`-able against
// a real cluster with zero transformation. No other apiVersion/kind is
// accepted — this project doesn't carry forward a legacy file format.
var clusterGroupVersion = hyvev1alpha1.GroupVersion.String()

const clusterKind = "ClusterDefinition"

// localClusterDefinition is a primary clusters/<name>.yaml file's on-disk
// shape: the real CRD's TypeMeta/ObjectMeta/Spec, but deliberately no
// Status field — status is reconciler-owned and lives in the separate
// cluster-state/<name>.state.yaml sidecar instead (see
// FromTypesClusterDefinitionStatus), exactly mirroring how a real
// ClusterDefinition CR's status lives in a separate subresource rather than
// inline in what a human edits.
type localClusterDefinition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              hyvev1alpha1.ClusterDefinitionSpec `json:"spec,omitempty"`
}

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

// decodeClusterDefinition parses a primary file's bytes, validates its
// apiVersion/kind against the real CRD's, and converts it into the shared
// in-memory types.ClusterDefinition shape internal/reconcile's engine uses
// — with DriverOutputs/AppliedResources left empty; mergeSidecar overlays
// those from the separate sidecar file.
func decodeClusterDefinition(path string, data []byte) (types.ClusterDefinition, error) {
	var local localClusterDefinition
	if err := k8syaml.Unmarshal(data, &local); err != nil {
		return types.ClusterDefinition{}, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if local.APIVersion != clusterGroupVersion || local.Kind != clusterKind {
		return types.ClusterDefinition{}, fmt.Errorf(
			"%s: apiVersion/kind must be %q/%q, got %q/%q",
			path, clusterGroupVersion, clusterKind, local.APIVersion, local.Kind)
	}
	cr := hyvev1alpha1.ClusterDefinition{
		TypeMeta:   local.TypeMeta,
		ObjectMeta: local.ObjectMeta,
		Spec:       local.Spec,
	}
	return crdconv.ToTypesClusterDefinition(&cr), nil
}

// mergeSidecar overlays cluster-state/<name>.state.yaml onto cluster.Spec's
// DriverOutputs/AppliedResources, if that file exists. If it doesn't, those
// two fields are left as decodeClusterDefinition's zero values. Once a
// sidecar exists, it is authoritative and overwrites whatever came from the
// primary file.
func (m *Manager) mergeSidecar(cluster *types.ClusterDefinition) error {
	data, err := os.ReadFile(m.sidecarPath(cluster.Metadata.Name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var status hyvev1alpha1.ClusterDefinitionStatus
	if err := k8syaml.Unmarshal(data, &status); err != nil {
		return err
	}
	cluster.Spec.DriverOutputs = status.DriverOutputs
	cluster.Spec.AppliedResources = crdconv.ToTypesAppliedResources(status.AppliedResources)
	return nil
}

// HasStateSidecar reports whether cluster-state/<name>.state.yaml exists.
func (m *Manager) HasStateSidecar(name string) bool {
	_, err := os.Stat(m.sidecarPath(name))
	return err == nil
}

// WorkflowSource resolves a local-name WorkflowRef against workflows/ under
// this repository — file mode's only ever source, unchanged behavior.
func (m *Manager) WorkflowSource() workflow.Source {
	return workflow.FileSource{Dir: filepath.Join(m.LocalPath(), workflow.WorkflowsDir)}
}

// statusIsEmpty reports whether there is nothing worth persisting to a
// sidecar file (e.g. a brand-new cluster before its first create/apply
// cycle) — the ClusterDefinitionStatus analogue of the old ClusterState.IsEmpty.
func statusIsEmpty(s hyvev1alpha1.ClusterDefinitionStatus) bool {
	return len(s.DriverOutputs) == 0 && len(s.AppliedResources) == 0
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
	def, err := decodeClusterDefinition(m.clusterPath(name), data)
	if err != nil {
		return nil, nil, err
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
// file's directory entirely, not just out of its git diff. The primary file
// is exactly what a real ClusterDefinition CR's spec looks like
// (apiVersion: hyve.io/v1alpha1, kind: ClusterDefinition) — `kubectl apply
// -f` against it works unmodified. If the resulting status is empty (e.g. a
// cluster with no resources yet), any existing sidecar file is removed
// rather than left as an empty file. The caller is responsible for
// committing the change if it should be pushed to the remote.
func (m *Manager) SaveClusterDefinition(def *types.ClusterDefinition) error {
	if err := os.MkdirAll(m.stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create clusters directory: %w", err)
	}

	local := localClusterDefinition{
		TypeMeta:   metav1.TypeMeta{APIVersion: clusterGroupVersion, Kind: clusterKind},
		ObjectMeta: metav1.ObjectMeta{Name: def.Metadata.Name},
		Spec:       crdconv.FromTypesClusterDefinitionSpec(def),
	}
	data, err := k8syaml.Marshal(&local)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster definition: %w", err)
	}
	if err := os.WriteFile(m.clusterPath(def.Metadata.Name), data, 0644); err != nil {
		return fmt.Errorf("failed to write cluster definition: %w", err)
	}

	status := crdconv.FromTypesClusterDefinitionStatus(def)
	sidecarPath := m.sidecarPath(def.Metadata.Name)
	if statusIsEmpty(status) {
		if err := os.Remove(sidecarPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove empty state sidecar: %w", err)
		}
		return nil
	}
	sdata, err := k8syaml.Marshal(&status)
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
		def, err := decodeClusterDefinition(path, data)
		if err != nil {
			return nil
		}
		if def.Metadata.Name == clusterName {
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

		cluster, err := decodeClusterDefinition(path, data)
		if err != nil {
			return err
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
