package workflow

import (
	"time"

	"github.com/cbridges1/hyve/internal/secretsfrom"
)

// Runtime values for WorkflowSpec.Runtime — see WorkflowSpec.Runtime's doc
// comment.
const (
	RuntimeClient  = "client"
	RuntimeCluster = "cluster" // also the default when Runtime is unset
)

// Workflow represents a workflow definition
type Workflow struct {
	APIVersion string           `yaml:"apiVersion" json:"apiVersion"`
	Kind       string           `yaml:"kind" json:"kind"`
	Metadata   WorkflowMetadata `yaml:"metadata" json:"metadata"`
	Spec       WorkflowSpec     `yaml:"spec" json:"spec"`
}

// WorkflowMetadata contains workflow metadata. Created maps to the real
// Workflow CR's ObjectMeta.CreationTimestamp (server/manager-set once, at
// creation) — there's no clean CRD equivalent for a separate "Updated"
// field (closest is resourceVersion/generation, different semantics), so
// unlike the pre-unification file format this no longer tracks it.
type WorkflowMetadata struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Created     time.Time         `yaml:"created,omitempty" json:"created,omitempty"`
}

// WorkflowInput declares a variable that must be present before the workflow runs.
// When used as a lifecycle hook the value is injected automatically by the reconciler.
// When run ad-hoc via `hyve workflow run`, missing inputs are prompted in the TUI or
// must be supplied with --set KEY=VALUE on the CLI.
type WorkflowInput struct {
	Name        string `yaml:"name" json:"name"`                                   // Environment variable name (e.g. HYVE_CLUSTER_NAME)
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // Shown as the prompt label in the TUI
	Default     string `yaml:"default,omitempty" json:"default,omitempty"`         // Used when not provided; empty means no default
}

// WorkflowPreFlight controls the cluster pre-flight check run before the workflow.
type WorkflowPreFlight struct {
	// Cluster controls whether hyve calls EKS DescribeCluster and syncs kubeconfig
	// before the first step. Accepted values:
	//   "full" (default) — run EKS describe + kubeconfig sync (current behaviour)
	//   "skip"           — inject HYVE_CLUSTER_NAME etc. from the on-disk YAML
	//                      but skip all AWS calls; use for auth-bootstrap workflows
	//                      whose purpose is to obtain credentials in the first place.
	Cluster string `yaml:"cluster,omitempty" json:"cluster,omitempty"`
}

// WorkflowSpec defines the workflow specification
type WorkflowSpec struct {
	Inputs       []WorkflowInput       `yaml:"inputs,omitempty" json:"inputs,omitempty"` // Variables required at runtime
	Requirements *WorkflowRequirements `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	PreFlight    WorkflowPreFlight     `yaml:"preFlight,omitempty" json:"preFlight,omitempty"`
	Triggers     []WorkflowTrigger     `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	Jobs         []WorkflowJob         `yaml:"jobs" json:"jobs"`
	Env          map[string]string     `yaml:"env,omitempty" json:"env,omitempty"`

	// Runtime is either "client" (this workflow always runs as a local
	// subprocess on the invoking machine, never scheduled as a Job — see
	// Executor.AllowClientRuntime) or unset/"cluster" (today's behavior:
	// local in CLI mode, a container Job in controller mode). container:
	// has no effect on a runtime: client workflow — same "informational,
	// ignored" treatment it already gets in plain CLI/local mode, not a
	// validation error (see internal/workflow/job_runner.go's executeStep
	// and Validate's lint warning for this combination).
	Runtime string `yaml:"runtime,omitempty" json:"runtime,omitempty"`

	// SecretsFrom declares Kubernetes Secrets to resolve into env vars
	// before this workflow's steps run — e.g. Harbor registry credentials
	// already sitting in a cluster the user has legitimate access to. Each
	// entry's Cluster is resolved via whatever kubeconfig the caller
	// already has for it (see secretsfrom.KubeconfigLocator); the fetch
	// grants no access beyond what that kubeconfig's own credentials
	// already have.
	SecretsFrom []secretsfrom.SecretSource `yaml:"secretsFrom,omitempty" json:"secretsFrom,omitempty"`
}

// WorkflowRequirements defines prerequisites for workflow execution
type WorkflowRequirements struct {
	Tools   []ToolRequirement   `yaml:"tools,omitempty" json:"tools,omitempty"`     // CLI tools that must be available
	Secrets []SecretRequirement `yaml:"secrets,omitempty" json:"secrets,omitempty"` // Secrets that must be configured
}

// ToolRequirement specifies a required CLI tool
type ToolRequirement struct {
	Name        string `yaml:"name" json:"name"`                                   // Tool name (e.g., "kubectl", "helm", "docker")
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`         // Minimum version (optional)
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // Human-readable description
}

// SecretRequirement specifies a required secret or credential
type SecretRequirement struct {
	Name        string `yaml:"name" json:"name"`                                   // Environment variable name (e.g., "DOCKER_TOKEN")
	Provider    string `yaml:"provider,omitempty" json:"provider,omitempty"`       // Provider name for database lookup (e.g., "docker", "github")
	Required    bool   `yaml:"required" json:"required"`                           // Whether this secret is mandatory
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // Human-readable description
}

// WorkflowTrigger defines when the workflow should run
type WorkflowTrigger struct {
	Type   string                 `yaml:"type" json:"type"` // manual, schedule, webhook
	Config map[string]interface{} `yaml:"config,omitempty" json:"config,omitempty"`
}

// WorkflowJob represents a job within a workflow
type WorkflowJob struct {
	Name        string               `yaml:"name" json:"name"`
	Description string               `yaml:"description,omitempty" json:"description,omitempty"`
	If          string               `yaml:"if,omitempty" json:"if,omitempty"`               // Condition to run this job
	DependsOn   []string             `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"` // Job dependencies
	Cluster     string               `yaml:"cluster,omitempty" json:"cluster,omitempty"`     // Specific cluster to run on
	Env         map[string]string    `yaml:"env,omitempty" json:"env,omitempty"`             // Job-specific environment variables
	Steps       []WorkflowStep       `yaml:"steps" json:"steps"`
	Timeout     string               `yaml:"timeout,omitempty" json:"timeout,omitempty"` // e.g., "5m", "1h"
	Retry       *WorkflowRetryPolicy `yaml:"retry,omitempty" json:"retry,omitempty"`

	// Container is the image every step in this job runs in when executed
	// via KubernetesJobStepRunner (controller mode) — meaningless, and
	// ignored rather than validated, under LocalStepRunner (plain CLI/local
	// mode, or any runtime: client workflow regardless of hyve's mode — see
	// Phase 5). A step's own Container overrides this job-level default;
	// if neither is set, HyveConfig.spec.defaultWorkflowImage is the last
	// fallback before a hard pre-flight failure. See StepRunner.RequiresContainer.
	Container string `yaml:"container,omitempty" json:"container,omitempty"`
}

// WorkflowStep represents a single step in a job
type WorkflowStep struct {
	Name            string            `yaml:"name" json:"name"`
	Description     string            `yaml:"description,omitempty" json:"description,omitempty"`
	If              string            `yaml:"if,omitempty" json:"if,omitempty"`                           // Condition to run this step
	Command         string            `yaml:"command,omitempty" json:"command,omitempty"`                 // Command to execute
	Script          string            `yaml:"script,omitempty" json:"script,omitempty"`                   // Multi-line script
	Action          string            `yaml:"action,omitempty" json:"action,omitempty"`                   // Pre-defined action
	With            map[string]string `yaml:"with,omitempty" json:"with,omitempty"`                       // Parameters for action
	Env             map[string]string `yaml:"env,omitempty" json:"env,omitempty"`                         // Step-specific environment variables
	WorkingDir      string            `yaml:"workingDir,omitempty" json:"workingDir,omitempty"`           // Working directory for this step
	Timeout         string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`                 // Step timeout
	ContinueOnError bool              `yaml:"continueOnError,omitempty" json:"continueOnError,omitempty"` // Continue even if step fails

	// Container overrides WorkflowJob.Container for this step only. See
	// WorkflowJob.Container's doc comment for the full resolution order.
	Container string `yaml:"container,omitempty" json:"container,omitempty"`
}

// WorkflowRetryPolicy defines retry behavior for jobs
type WorkflowRetryPolicy struct {
	MaxAttempts int    `yaml:"maxAttempts" json:"maxAttempts"`
	Delay       string `yaml:"delay,omitempty" json:"delay,omitempty"` // e.g., "30s", "1m"
}

// WorkflowExecution represents a workflow execution instance
type WorkflowExecution struct {
	ID           string                  `json:"id"`
	WorkflowName string                  `json:"workflowName"`
	Cluster      string                  `json:"cluster,omitempty"`
	Status       WorkflowExecutionStatus `json:"status"`
	StartTime    time.Time               `json:"startTime"`
	EndTime      *time.Time              `json:"endTime,omitempty"`
	Duration     time.Duration           `json:"duration"`
	Trigger      string                  `json:"trigger"`
	JobResults   map[string]*JobResult   `json:"jobResults"`
	Logs         []WorkflowLogEntry      `json:"logs"`
	Variables    map[string]string       `json:"variables,omitempty"`
}

// WorkflowExecutionStatus represents the status of a workflow execution
type WorkflowExecutionStatus string

const (
	StatusPending   WorkflowExecutionStatus = "pending"
	StatusRunning   WorkflowExecutionStatus = "running"
	StatusCompleted WorkflowExecutionStatus = "completed"
	StatusFailed    WorkflowExecutionStatus = "failed"
	StatusCancelled WorkflowExecutionStatus = "cancelled"
	StatusTimeout   WorkflowExecutionStatus = "timeout"
)

// JobResult represents the result of a job execution
type JobResult struct {
	Status    JobStatus              `json:"status"`
	StartTime time.Time              `json:"startTime"`
	EndTime   *time.Time             `json:"endTime,omitempty"`
	Duration  time.Duration          `json:"duration"`
	ExitCode  int                    `json:"exitCode,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Steps     map[string]*StepResult `json:"steps"`
}

// JobStatus represents the status of a job
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusSkipped   JobStatus = "skipped"
	JobStatusCancelled JobStatus = "cancelled"
)

// StepResult represents the result of a step execution
type StepResult struct {
	Status    JobStatus     `json:"status"`
	StartTime time.Time     `json:"startTime"`
	EndTime   *time.Time    `json:"endTime,omitempty"`
	Duration  time.Duration `json:"duration"`
	ExitCode  int           `json:"exitCode,omitempty"`
	Error     string        `json:"error,omitempty"`
	Output    string        `json:"output,omitempty"`
}

// WorkflowLogEntry represents a log entry during workflow execution
type WorkflowLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // INFO, WARN, ERROR, DEBUG
	Job       string    `json:"job,omitempty"`
	Step      string    `json:"step,omitempty"`
	Message   string    `json:"message"`
}
