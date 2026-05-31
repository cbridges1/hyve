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
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Default     string `yaml:"default,omitempty"`
	Required    bool   `yaml:"required,omitempty"`
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
	Version int                      `yaml:"version"`
	Modules map[string]*LockedModule `yaml:"modules"`
}

type LockedModule struct {
	Source   string       `yaml:"source"`
	Resolved string       `yaml:"resolved"`
	SHA256   string       `yaml:"sha256"`
	Runner   LockedRunner `yaml:"runner,omitempty"`
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
	Bootstrap BootstrapSpec `yaml:"bootstrap"`
	Verify    *VerifySpec   `yaml:"verify,omitempty"`
}

type BootstrapSpec struct {
	Script string `yaml:"script"`
}

type VerifySpec struct {
	Command string `yaml:"command"`
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
