package module

// ModuleManifest is parsed from module.yaml inside a module directory.
type ModuleManifest struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   ModuleMetadata `yaml:"metadata"`
	Spec       ModuleSpec     `yaml:"spec"`
}

type ModuleMetadata struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Description string   `yaml:"description,omitempty"`
	Author      string   `yaml:"author,omitempty"`
	License     string   `yaml:"license,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Type        string   `yaml:"type,omitempty"`
}

type ModuleSpec struct {
	Runner         RunnerConfig       `yaml:"runner,omitempty"`
	Params         []ParamSpec        `yaml:"params,omitempty"`
	Requirements   ModuleRequirements `yaml:"requirements,omitempty"`
	StatusCacheTTL string             `yaml:"statusCacheTTL,omitempty"`
}

type RunnerConfig struct {
	Image string `yaml:"image,omitempty"`
}

type ParamSpec struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Default     string   `yaml:"default,omitempty"`
	Required    bool     `yaml:"required,omitempty"`
	Choices     []string `yaml:"choices,omitempty"`
}

type ModuleRequirements struct {
	Env   []EnvRequirement  `yaml:"env,omitempty"`
	Tools []ToolRequirement `yaml:"tools,omitempty"`
}

type EnvRequirement struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Injected    bool   `yaml:"injected,omitempty"`
}

type ToolRequirement struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// LockFile represents hyve.lock.
type LockFile struct {
	Version   int                        `yaml:"version"`
	Modules   map[string]*LockedModule   `yaml:"modules"`
	Workflows map[string]*LockedWorkflow `yaml:"workflows,omitempty"`
}

type LockedModule struct {
	Source   string       `yaml:"source"`
	Resolved string       `yaml:"resolved"`
	SHA256   string       `yaml:"sha256"`
	Runner   LockedRunner `yaml:"runner,omitempty"`
}

// LockedWorkflow is one resolved, content-hashed remote workflow file. Unlike
// LockedModule, it carries a Name — `hyve workflow run <name>` must be able
// to find a locked entry by bare name, which modules never need since a
// cluster's driver is always referenced by an explicit source+version.
type LockedWorkflow struct {
	Name     string `yaml:"name"`
	Source   string `yaml:"source"`   // canonical "host/org/repo//path/file.yaml" — never a directory
	Resolved string `yaml:"resolved"` // full download URL for this exact file at the pinned ref
	SHA256   string `yaml:"sha256"`   // sha256 of this file's raw bytes only
}

type LockedRunner struct {
	Image  string `yaml:"image,omitempty"`
	Digest string `yaml:"digest,omitempty"`
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
	OperationCreate OperationType = "create"
	OperationDelete OperationType = "delete"
	OperationStatus OperationType = "status"
	OperationAuth   OperationType = "auth"
	OperationScale  OperationType = "scale"
)

// OperationResult holds HYVE_KEY=value outputs captured from an operation.
type OperationResult struct {
	Outputs  map[string]string
	ExitCode int
}
