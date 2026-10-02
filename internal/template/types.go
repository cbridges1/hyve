package template

import "github.com/cbridges1/hyve/internal/types"

// TemplateMetadata represents template metadata
type TemplateMetadata struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// TemplateRunnerConfig configures how the module should be executed.
type TemplateRunnerConfig struct {
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
}

// TemplateSpec represents the template specification — driver-based. Driver
// and Workflows reuse the exact same types.DriverRef/types.WorkflowsSpec a
// ClusterDefinition's own spec uses (rather than template-local duplicates
// of identical shape) — Workflows gains PreReconcile support this way,
// which the old template-local TemplateWorkflowsSpec never had.
type TemplateSpec struct {
	Driver    types.DriverRef      `yaml:"driver" json:"driver"`
	Runner    TemplateRunnerConfig `yaml:"runner,omitempty" json:"runner,omitempty"`
	Params    map[string]string    `yaml:"params,omitempty" json:"params,omitempty"`
	Region    string               `yaml:"region,omitempty" json:"region,omitempty"`
	Workflows types.WorkflowsSpec  `yaml:"workflows,omitempty" json:"workflows,omitempty"`
	Resources []types.ResourceRef  `yaml:"resources,omitempty" json:"resources,omitempty"`

	// Schedule is a 5-field cron expression (e.g. "0 20 * * 5").
	// When a cluster is created from this template, the next occurrence is
	// calculated and written to the generated cluster's spec.expiresAt field.
	Schedule string `yaml:"schedule,omitempty" json:"schedule,omitempty"`

	// LockParams prevents users from overriding default param values when
	// creating a cluster from this template. When true, the TUI skips the
	// param override step and the --set flag on `hyve cluster create` is
	// silently ignored. Intended for admins who want to enforce standard
	// cluster configurations.
	LockParams bool `yaml:"lockParams,omitempty" json:"lockParams,omitempty"`
}

// Template represents a complete cluster template definition
type Template struct {
	APIVersion string           `yaml:"apiVersion" json:"apiVersion"`
	Kind       string           `yaml:"kind" json:"kind"`
	Metadata   TemplateMetadata `yaml:"metadata" json:"metadata"`
	Spec       TemplateSpec     `yaml:"spec" json:"spec"`
	// Filename is the on-disk filename (e.g. "my-template.yaml"). It is
	// populated at runtime by the manager and never written to the YAML file.
	Filename string `yaml:"-" json:"filename,omitempty"`
}
