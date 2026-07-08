package template

import "github.com/cbridges1/hyve/internal/types"

// TemplateMetadata represents template metadata
type TemplateMetadata struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// TemplateWorkflowsSpec defines workflows to run on cluster lifecycle events
type TemplateWorkflowsSpec struct {
	BeforeCreate []types.WorkflowRef `yaml:"beforeCreate,omitempty" json:"beforeCreate,omitempty"` // Workflows to run before cluster creation (no kubeconfig)
	OnCreate     []types.WorkflowRef `yaml:"onCreate,omitempty" json:"onCreate,omitempty"`         // Workflows to run after cluster creation, before spec.resources applies
	AfterCreate  []types.WorkflowRef `yaml:"afterCreate,omitempty" json:"afterCreate,omitempty"`   // Workflows to run after cluster creation, after spec.resources has applied
	OnDelete     []types.WorkflowRef `yaml:"onDelete,omitempty" json:"onDelete,omitempty"`         // Workflows to run before cluster deletion
	AfterDelete  []types.WorkflowRef `yaml:"afterDelete,omitempty" json:"afterDelete,omitempty"`   // Workflows to run after cluster deletion (no kubeconfig)
}

// UnmarshalYAML migrates the deprecated onDestroy key to onDelete transparently.
func (ws *TemplateWorkflowsSpec) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type raw struct {
		BeforeCreate []types.WorkflowRef `yaml:"beforeCreate,omitempty"`
		OnCreate     []types.WorkflowRef `yaml:"onCreate,omitempty"`
		AfterCreate  []types.WorkflowRef `yaml:"afterCreate,omitempty"`
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
	ws.AfterCreate = r.AfterCreate
	ws.OnDelete = r.OnDelete
	ws.AfterDelete = r.AfterDelete
	if len(ws.OnDelete) == 0 && len(r.OnDestroy) > 0 {
		ws.OnDelete = r.OnDestroy
	}
	return nil
}

// TemplateDriverRef identifies the module a template should use.
type TemplateDriverRef struct {
	Source  string `yaml:"source" json:"source"`
	Version string `yaml:"version" json:"version"`
}

// TemplateRunnerConfig configures how the module should be executed.
type TemplateRunnerConfig struct {
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
}

// TemplateSpec represents the template specification — driver-based.
type TemplateSpec struct {
	Driver    TemplateDriverRef     `yaml:"driver" json:"driver"`
	Runner    TemplateRunnerConfig  `yaml:"runner,omitempty" json:"runner,omitempty"`
	Params    map[string]string     `yaml:"params,omitempty" json:"params,omitempty"`
	Region    string                `yaml:"region,omitempty" json:"region,omitempty"`
	Workflows TemplateWorkflowsSpec `yaml:"workflows,omitempty" json:"workflows,omitempty"`
	Resources []types.ResourceRef   `yaml:"resources,omitempty" json:"resources,omitempty"`

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
