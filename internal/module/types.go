package module

// ModuleManifest is parsed from module.yaml inside a module directory.
type ModuleManifest struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind" json:"kind"`
	Metadata   ModuleMetadata `yaml:"metadata" json:"metadata"`
	Spec       ModuleSpec     `yaml:"spec" json:"spec"`
}

type ModuleMetadata struct {
	Name        string   `yaml:"name" json:"name"`
	Version     string   `yaml:"version" json:"version"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Author      string   `yaml:"author,omitempty" json:"author,omitempty"`
	License     string   `yaml:"license,omitempty" json:"license,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Type        string   `yaml:"type,omitempty" json:"type,omitempty"`
}

type ModuleSpec struct {
	Runner         RunnerConfig       `yaml:"runner,omitempty" json:"runner,omitempty"`
	Params         []ParamSpec        `yaml:"params,omitempty" json:"params,omitempty"`
	Requirements   ModuleRequirements `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	StatusCacheTTL string             `yaml:"statusCacheTTL,omitempty" json:"statusCacheTTL,omitempty"`
}

type RunnerConfig struct {
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
}

type ParamSpec struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Default     string   `yaml:"default,omitempty" json:"default,omitempty"`
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`
	Choices     []string `yaml:"choices,omitempty" json:"choices,omitempty"`
}

type ModuleRequirements struct {
	Env   []EnvRequirement  `yaml:"env,omitempty" json:"env,omitempty"`
	Tools []ToolRequirement `yaml:"tools,omitempty" json:"tools,omitempty"`

	// MgmtCluster names another hyve-managed ClusterDefinition this module
	// depends on for credentials — e.g. a module wrapping Cluster API,
	// which needs a kubeconfig for a separate CAPI management cluster
	// that's itself just another cluster hyve already knows about. Optional;
	// see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's "A typed mgmtCluster
	// module requirement" section. Checked at reconcile pre-flight (see
	// internal/reconcile) — a missing/wrong mgmtCluster otherwise only ever
	// surfaces as a script failure deep inside create.yaml.
	MgmtCluster string `yaml:"mgmtCluster,omitempty" json:"mgmtCluster,omitempty"`
}

type EnvRequirement struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Injected    bool   `yaml:"injected,omitempty" json:"injected,omitempty"`
}

type ToolRequirement struct {
	Name        string `yaml:"name" json:"name"`
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// LockFile represents hyve.lock.
type LockFile struct {
	Version   int                        `yaml:"version" json:"version"`
	Modules   map[string]*LockedModule   `yaml:"modules" json:"modules"`
	Workflows map[string]*LockedWorkflow `yaml:"workflows,omitempty" json:"workflows,omitempty"`
	Resources map[string]*LockedResource `yaml:"resources,omitempty" json:"resources,omitempty"`
}

type LockedModule struct {
	Source   string       `yaml:"source" json:"source"`
	Resolved string       `yaml:"resolved" json:"resolved"`
	SHA256   string       `yaml:"sha256" json:"sha256"`
	Runner   LockedRunner `yaml:"runner,omitempty" json:"runner,omitempty"`
}

// LockedWorkflow is one resolved, content-hashed remote workflow file. Unlike
// LockedModule, it carries a Name — `hyve workflow run <name>` must be able
// to find a locked entry by bare name, which modules never need since a
// cluster's driver is always referenced by an explicit source+version.
type LockedWorkflow struct {
	Name     string `yaml:"name" json:"name"`
	Source   string `yaml:"source" json:"source"`     // canonical "host/org/repo//path/file.yaml" — never a directory
	Resolved string `yaml:"resolved" json:"resolved"` // full download URL for this exact file at the pinned ref
	SHA256   string `yaml:"sha256" json:"sha256"`     // sha256 of this file's raw bytes only
}

type LockedRunner struct {
	Image  string `yaml:"image,omitempty" json:"image,omitempty"`
	Digest string `yaml:"digest,omitempty" json:"digest,omitempty"`
}

// LockedResource is one resolved, content-hashed remote resource manifest.
// Unlike LockedModule, it carries a Name for the same reason LockedWorkflow
// does — a resource is referenced by Name after install, not by
// source+version. Unlike LockedWorkflow, Name comes from the ResourceRef
// itself rather than being derived from file content: a raw K8s manifest
// has no single canonical "hyve name" the way a Workflow file's own
// metadata.name does, especially once multi-document.
type LockedResource struct {
	Name     string `yaml:"name" json:"name"`
	Source   string `yaml:"source" json:"source"`     // canonical "host/org/repo//path/file.yaml" — never a directory
	Resolved string `yaml:"resolved" json:"resolved"` // full download URL for this exact file at the pinned ref
	SHA256   string `yaml:"sha256" json:"sha256"`     // sha256 of this file's raw bytes only
}

// ClusterAuth is parsed from auth.yaml (kind: ClusterAuth).
type ClusterAuth struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   ClusterAuthMeta `yaml:"metadata"`
	Spec       ClusterAuthSpec `yaml:"spec"`
}

type ClusterAuthMeta struct {
	Name string `yaml:"name"`
}

type ClusterAuthSpec struct {
	// Legacy single-method fields (kept for backward compat)
	Bootstrap BootstrapSpec `yaml:"bootstrap"`
	Verify    *VerifySpec   `yaml:"verify,omitempty"`
	// Multi-method list — takes precedence over Bootstrap/Verify when present
	Methods []AuthMethod `yaml:"methods,omitempty"`
}

type BootstrapSpec struct {
	Script string `yaml:"script"`
}

type VerifySpec struct {
	Command string `yaml:"command"`
}

// AuthMethod is a single named auth path within a ClusterAuth manifest.
type AuthMethod struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description,omitempty"`
	Deps        []string      `yaml:"deps,omitempty"`
	Auth        BootstrapSpec `yaml:"auth"`
	Exports     string        `yaml:"exports,omitempty"`
}

// OperationType identifies which module operation to run.
type OperationType string

const (
	OperationCreate   OperationType = "create"
	OperationDelete   OperationType = "delete"
	OperationStatus   OperationType = "status"
	OperationAuth     OperationType = "auth"
	OperationScale    OperationType = "scale"
	OperationDescribe OperationType = "describe"
)

// ModuleTypeAuthOnly marks a module that only manages authentication for an
// already-existing, non-provisionable cluster (e.g. k3d, a pre-existing
// cloud cluster). Such modules typically have no create/delete/status —
// the reconciler treats an empty HYVE_CLUSTER_STATUS as ACTIVE for them
// rather than requiring a status op to say so explicitly.
const ModuleTypeAuthOnly = "authOnly"

// OperationResult holds HYVE_KEY=value outputs captured from an operation.
type OperationResult struct {
	Outputs  map[string]string
	ExitCode int
}
