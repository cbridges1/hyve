package template

import "github.com/cbridges1/hyve/internal/types"

// TemplateMetadata represents template metadata
type TemplateMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// TemplateWorkflowsSpec defines workflows to run on cluster lifecycle events
type TemplateWorkflowsSpec struct {
	BeforeCreate []types.WorkflowRef `yaml:"beforeCreate,omitempty"` // Workflows to run before cluster creation (no kubeconfig)
	OnCreate     []types.WorkflowRef `yaml:"onCreate,omitempty"`     // Workflows to run after cluster creation
	OnDelete     []types.WorkflowRef `yaml:"onDelete,omitempty"`     // Workflows to run before cluster deletion
	AfterDelete  []types.WorkflowRef `yaml:"afterDelete,omitempty"`  // Workflows to run after cluster deletion (no kubeconfig)
}

// UnmarshalYAML migrates the deprecated onDestroy key to onDelete transparently.
func (ws *TemplateWorkflowsSpec) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type raw struct {
		BeforeCreate []types.WorkflowRef `yaml:"beforeCreate,omitempty"`
		OnCreate     []types.WorkflowRef `yaml:"onCreate,omitempty"`
		OnDelete     []types.WorkflowRef `yaml:"onDelete,omitempty"`
		OnDestroy    []types.WorkflowRef `yaml:"onDestroy,omitempty"` // deprecated: rename to onDelete in YAML
		AfterDelete  []types.WorkflowRef `yaml:"afterDelete,omitempty"`
	}
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	ws.BeforeCreate = r.BeforeCreate
	ws.OnCreate = r.OnCreate
	ws.OnDelete = r.OnDelete
	ws.AfterDelete = r.AfterDelete
	if len(ws.OnDelete) == 0 && len(r.OnDestroy) > 0 {
		ws.OnDelete = r.OnDestroy
	}
	return nil
}

// TemplateDriverRef identifies the module a template should use.
type TemplateDriverRef struct {
	Source  string `yaml:"source"`
	Version string `yaml:"version"`
}

// TemplateRunnerConfig configures how the module should be executed.
type TemplateRunnerConfig struct {
	Image string `yaml:"image,omitempty"`
}

// TemplateSpec represents the template specification — driver-based.
type TemplateSpec struct {
	Driver    TemplateDriverRef     `yaml:"driver"`
	Runner    TemplateRunnerConfig  `yaml:"runner,omitempty"`
	Params    map[string]string     `yaml:"params,omitempty"`
	Region    string                `yaml:"region,omitempty"`
	Workflows TemplateWorkflowsSpec `yaml:"workflows,omitempty"`

	// Schedule is a 5-field cron expression (e.g. "0 20 * * 5").
	// At template execution time the next occurrence is calculated and written
	// to the generated cluster's spec.expiresAt field.
	Schedule string `yaml:"schedule,omitempty"`

	// LockParams prevents users from overriding default param values at
	// template execution time. When true, the TUI skips the param override
	// step and the --set flag on `hyve template execute` is silently ignored.
	// Intended for admins who want to enforce standard cluster configurations.
	LockParams bool `yaml:"lockParams,omitempty"`
}

// Template represents a complete cluster template definition
type Template struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   TemplateMetadata `yaml:"metadata"`
	Spec       TemplateSpec     `yaml:"spec"`
	// Filename is the on-disk filename (e.g. "my-template.yaml"). It is
	// populated at runtime by the manager and never written to the YAML file.
	Filename string `yaml:"-"`
}
