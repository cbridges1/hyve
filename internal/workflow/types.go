package workflow

import (
	"time"
)

// Workflow represents a workflow definition
type Workflow struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   WorkflowMetadata `yaml:"metadata"`
	Spec       WorkflowSpec     `yaml:"spec"`
}

// WorkflowMetadata contains workflow metadata
type WorkflowMetadata struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Created     time.Time         `yaml:"created,omitempty"`
	Updated     time.Time         `yaml:"updated,omitempty"`
}

// WorkflowSpec defines the workflow specification
type WorkflowSpec struct {
	Requirements *WorkflowRequirements `yaml:"requirements,omitempty"`
	Triggers     []WorkflowTrigger     `yaml:"triggers,omitempty"`
	Jobs         []WorkflowJob         `yaml:"jobs"`
	Env          map[string]string     `yaml:"env,omitempty"`
}

// WorkflowRequirements defines prerequisites for workflow execution
type WorkflowRequirements struct {
	Tools   []ToolRequirement   `yaml:"tools,omitempty"`   // CLI tools that must be available
	Secrets []SecretRequirement `yaml:"secrets,omitempty"` // Secrets that must be configured
}

// ToolRequirement specifies a required CLI tool
type ToolRequirement struct {
	Name        string `yaml:"name"`                  // Tool name (e.g., "kubectl", "helm", "docker")
	Version     string `yaml:"version,omitempty"`     // Minimum version (optional)
	Description string `yaml:"description,omitempty"` // Human-readable description
}

// SecretRequirement specifies a required secret or credential
type SecretRequirement struct {
	Name        string `yaml:"name"`                  // Environment variable name (e.g., "DOCKER_TOKEN")
	Provider    string `yaml:"provider,omitempty"`    // Provider name for database lookup (e.g., "docker", "github")
	Required    bool   `yaml:"required"`              // Whether this secret is mandatory
	Description string `yaml:"description,omitempty"` // Human-readable description
}

// WorkflowTrigger defines when the workflow should run
type WorkflowTrigger struct {
	Type   string                 `yaml:"type"` // manual, schedule, webhook
	Config map[string]interface{} `yaml:"config,omitempty"`
}

// WorkflowJob represents a job within a workflow
type WorkflowJob struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description,omitempty"`
	If          string               `yaml:"if,omitempty"`        // Condition to run this job
	DependsOn   []string             `yaml:"dependsOn,omitempty"` // Job dependencies
	Cluster     string               `yaml:"cluster,omitempty"`   // Specific cluster to run on
	Env         map[string]string    `yaml:"env,omitempty"`       // Job-specific environment variables
	Steps       []WorkflowStep       `yaml:"steps"`
	Timeout     string               `yaml:"timeout,omitempty"` // e.g., "5m", "1h"
	Retry       *WorkflowRetryPolicy `yaml:"retry,omitempty"`
}

// WorkflowStep represents a single step in a job
type WorkflowStep struct {
	Name            string            `yaml:"name"`
	Description     string            `yaml:"description,omitempty"`
	If              string            `yaml:"if,omitempty"`              // Condition to run this step
	Command         string            `yaml:"command,omitempty"`         // Command to execute
	Script          string            `yaml:"script,omitempty"`          // Multi-line script
	Action          string            `yaml:"action,omitempty"`          // Pre-defined action
	With            map[string]string `yaml:"with,omitempty"`            // Parameters for action
	Env             map[string]string `yaml:"env,omitempty"`             // Step-specific environment variables
	WorkingDir      string            `yaml:"workingDir,omitempty"`      // Working directory for this step
	Timeout         string            `yaml:"timeout,omitempty"`         // Step timeout
	ContinueOnError bool              `yaml:"continueOnError,omitempty"` // Continue even if step fails
}

// WorkflowRetryPolicy defines retry behavior for jobs
type WorkflowRetryPolicy struct {
	MaxAttempts int    `yaml:"maxAttempts"`
	Delay       string `yaml:"delay,omitempty"` // e.g., "30s", "1m"
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
