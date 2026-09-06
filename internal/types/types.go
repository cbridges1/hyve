package types

import "fmt"

// WorkflowRef identifies one workflow to run for a lifecycle hook. Accepts
// two YAML forms in the same list:
//   - a plain scalar string: a local workflow name (existing behavior)
//     e.g. "provision-network"
//   - a mapping: {source: "github.com/org/repo//path@version", path: "override.yaml"}
//     for a remote workflow. `path` is equivalent to an inline "//path" in
//     Source and takes precedence, with a warning, if both are given.
type WorkflowRef struct {
	Name   string `yaml:"-" json:"name,omitempty"`                  // set for the local-name (string) form; empty for the remote form
	Source string `yaml:"source,omitempty" json:"source,omitempty"` // set for the remote (mapping) form; empty for the local form
	Path   string `yaml:"path,omitempty" json:"path,omitempty"`
}

// IsRemote reports whether this ref names a remote source rather than a
// local workflow name.
func (r WorkflowRef) IsRemote() bool { return r.Source != "" }

// String returns a human-readable label — the local name, or the source
// string (with path noted if set) for a remote ref.
func (r WorkflowRef) String() string {
	if !r.IsRemote() {
		return r.Name
	}
	if r.Path != "" {
		return r.Source + " (path: " + r.Path + ")"
	}
	return r.Source
}

// UnmarshalYAML mirrors WorkflowsSpec's existing old-style (func-based)
// interface below.
func (r *WorkflowRef) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		*r = WorkflowRef{Name: name}
		return nil
	}
	type raw struct {
		Source string `yaml:"source"`
		Path   string `yaml:"path,omitempty"`
	}
	var rw raw
	if err := unmarshal(&rw); err != nil {
		return fmt.Errorf("workflows entry must be a string (local workflow name) or a mapping with 'source': %w", err)
	}
	if rw.Source == "" {
		return fmt.Errorf("workflows entry mapping must set 'source'")
	}
	*r = WorkflowRef{Source: rw.Source, Path: rw.Path}
	return nil
}

// MarshalYAML renders a local ref as a bare string and a remote ref as a
// {source, path} mapping.
func (r WorkflowRef) MarshalYAML() (interface{}, error) {
	if !r.IsRemote() {
		return r.Name, nil
	}
	type raw struct {
		Source string `yaml:"source"`
		Path   string `yaml:"path,omitempty"`
	}
	return raw{Source: r.Source, Path: r.Path}, nil
}

// WorkflowsSpec defines workflows to run on cluster lifecycle events
type WorkflowsSpec struct {
	BeforeCreate []WorkflowRef `yaml:"beforeCreate,omitempty" json:"beforeCreate,omitempty"` // Workflows to run before cluster creation (no kubeconfig)
	OnCreate     []WorkflowRef `yaml:"onCreate,omitempty" json:"onCreate,omitempty"`         // Workflows to run after cluster creation, before spec.resources applies
	AfterCreate  []WorkflowRef `yaml:"afterCreate,omitempty" json:"afterCreate,omitempty"`   // Workflows to run after cluster creation, after spec.resources has applied
	OnDelete     []WorkflowRef `yaml:"onDelete,omitempty" json:"onDelete,omitempty"`         // Workflows to run before cluster deletion
	AfterDelete  []WorkflowRef `yaml:"afterDelete,omitempty" json:"afterDelete,omitempty"`   // Workflows to run after cluster deletion (no kubeconfig)
	PreReconcile []WorkflowRef `yaml:"preReconcile,omitempty" json:"preReconcile,omitempty"` // Workflows to run before reconcile pre-flight (no kubeconfig)
}

// UnmarshalYAML migrates the deprecated onDestroy key to onDelete transparently.
func (ws *WorkflowsSpec) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type raw struct {
		BeforeCreate []WorkflowRef `yaml:"beforeCreate,omitempty"`
		OnCreate     []WorkflowRef `yaml:"onCreate,omitempty"`
		AfterCreate  []WorkflowRef `yaml:"afterCreate,omitempty"`
		OnDelete     []WorkflowRef `yaml:"onDelete,omitempty"`
		OnDestroy    []WorkflowRef `yaml:"onDestroy,omitempty"` // deprecated: use onDelete
		AfterDelete  []WorkflowRef `yaml:"afterDelete,omitempty"`
		PreReconcile []WorkflowRef `yaml:"preReconcile,omitempty"`
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
	ws.PreReconcile = r.PreReconcile
	if len(ws.OnDelete) == 0 && len(r.OnDestroy) > 0 {
		ws.OnDelete = r.OnDestroy
	}
	return nil
}

// PendingWorkflow represents a one-off workflow queued for execution
type PendingWorkflow struct {
	Workflow string `yaml:"workflow" json:"workflow"`
	RunAt    string `yaml:"runAt,omitempty" json:"runAt,omitempty"` // RFC 3339; absent = run immediately
}

// WorkflowSchedule maps a workflow name to a cron expression for recurring execution
type WorkflowSchedule struct {
	Workflow string `yaml:"workflow" json:"workflow"`
	Schedule string `yaml:"schedule" json:"schedule"` // 5-field cron expression
}

// DriverRef identifies the module (driver) that owns a cluster.
type DriverRef struct {
	Source  string `yaml:"source" json:"source"`
	Version string `yaml:"version" json:"version"`
}

// RunnerSpec configures the container image cluster mode dispatches this
// cluster's module create/status/delete/auth operations to as a Kubernetes
// Job (see module.JobRunner) — set here (or on the Template a cluster was
// created from, which this is rendered from — see
// hyvev1alpha1.RenderClusterDefinitionSpec) rather than on the module
// itself: a module's own module.yaml can document/recommend a suitable
// image (its requirements.tools entries' description field), but doesn't
// choose one, since the same module may run under different images across
// different deployments (a private registry mirror, extra bundled tools,
// a hardened base). Ignored entirely in local/CLI mode, where modules
// always run inline.
type RunnerSpec struct {
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
}

// ClusterSpec represents the desired cluster configuration.
// The module identified by Driver is responsible for translating Params into
// cloud API calls; the reconciler is provider-agnostic and only orchestrates.
type ClusterSpec struct {
	// Driver identifies the module that manages this cluster (e.g. github.com/hyve-modules/aws-eks).
	Driver DriverRef `yaml:"driver,omitempty" json:"driver,omitempty"`

	// Runner configures the image cluster mode's Job dispatch uses for this
	// cluster's module operations — see RunnerSpec.
	Runner RunnerSpec `yaml:"runner,omitempty" json:"runner,omitempty"`

	// Params are arbitrary key/value pairs passed to the driver as HYVE_PARAM_<KEY>
	// environment variables when running module operations.
	Params map[string]string `yaml:"params,omitempty" json:"params,omitempty"`

	// DriverOutputs captures HYVE_* outputs produced by the driver's create/status
	// operations. These are persisted to the cluster YAML so subsequent operations
	// can reference them and so the reconciler can detect param drift via the stored
	// HYVE_LAST_PARAMS_HASH entry.
	DriverOutputs map[string]string `yaml:"driverOutputs,omitempty" json:"driverOutputs,omitempty"`

	Workflows WorkflowsSpec `yaml:"workflows,omitempty" json:"workflows,omitempty"`

	// Resources declares Kubernetes manifests hyve should own, drift-check, and
	// re-apply on every reconcile cycle for an ACTIVE cluster — unconditionally,
	// not gated by param drift. See ResourceRef.
	Resources []ResourceRef `yaml:"resources,omitempty" json:"resources,omitempty"`

	// AppliedResources is reconciler-owned state tracking what Resources entries
	// are currently applied, keyed by ResourceRef.Name. Mirrors DriverOutputs —
	// never hand-edit.
	AppliedResources map[string]*AppliedResource `yaml:"appliedResources,omitempty" json:"appliedResources,omitempty"`

	// PendingWorkflows is a Git-audited queue of one-off workflow runs. Entries without
	// a runAt execute immediately on the next reconcile; entries with a runAt execute
	// when the current time is at or past that timestamp. The reconciler removes entries
	// after executing them and commits the cleared YAML.
	PendingWorkflows []PendingWorkflow `yaml:"pendingWorkflows,omitempty" json:"pendingWorkflows,omitempty"`

	// WorkflowSchedules maps workflow names to cron expressions. On every reconcile the
	// reconciler evaluates each schedule and appends due entries to PendingWorkflows.
	WorkflowSchedules []WorkflowSchedule `yaml:"workflowSchedules,omitempty" json:"workflowSchedules,omitempty"`

	// Delete marks this cluster for deletion. When true, the reconciler runs any
	// onDelete workflows, deletes the cluster from the cloud provider, and removes
	// this YAML file from the repository.
	Delete bool `yaml:"delete,omitempty" json:"delete,omitempty"`

	// Pause skips reconciliation for this cluster while keeping its definition in
	// the repository.
	Pause bool `yaml:"pause,omitempty" json:"pause,omitempty"`

	// ExpiresAt is an optional RFC 3339 timestamp. When the current time is past this
	// value the reconciler treats the cluster as if delete: true is set.
	ExpiresAt string `yaml:"expiresAt,omitempty" json:"expiresAt,omitempty"`

	// DependsOn names other clusters this one depends on — e.g. a Cluster
	// API-backed workload cluster that needs its CAPI management cluster
	// ACTIVE first. Optional; see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
	// "Optional dependsOn ordering" section. ReconcileOne skips (does not
	// fail) a reconcile cycle for this cluster while any named dependency
	// isn't ACTIVE.
	DependsOn []string `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`

	// AccessMethodRef names an AccessMethod (internal/apis/hyve/v1alpha1's
	// AccessMethod CRD) that `hyve cluster auth` resolves via a live
	// cluster-mode API to mint a kubeconfig — its driver module's auth
	// operation always runs server-side, never locally, so this field
	// only does anything once the cluster it's set on is actually managed
	// through a live cluster-mode API (a local-only ClusterDefinition can
	// still declare it — e.g. before `hyve migrate to-cluster` — but
	// `hyve cluster auth` on it locally errors clearly instead of
	// attempting anything). Independent of the CRD-only Access.Method/
	// Tunnel fields, which are separate server-mediated concepts. See
	// HYVE-ACCESS-METHOD-DESIGN.md.
	AccessMethodRef string `yaml:"accessMethodRef,omitempty" json:"accessMethodRef,omitempty"`

	// AccessMethodClusterID is this cluster's own identifier within the
	// referenced AccessMethod's provider (e.g. Rancher's internal cluster
	// ID) — required when AccessMethodRef is set. An admin sets this
	// directly today; auto-populating it (e.g. via a CAPI module's
	// afterCreate hook writing it into DriverOutputs) is a natural later
	// addition, not required for this to work.
	AccessMethodClusterID string `yaml:"accessMethodClusterID,omitempty" json:"accessMethodClusterID,omitempty"`

	// AccessMethod mirrors the CRD-only AccessSpec.Method (module-auth/
	// tunnel/primary) — despite the doc comment above this struct once
	// saying that field never needed to reach internal/types, "primary"
	// specifically does: it's the one access method with no driver at all
	// (see HYVE-MULTI-TENANCY-PLAN.md's "Host cluster access" section), and
	// this same reconcile code (internal/reconcile) is what enforces "a
	// cluster must have a driver" — confirmed live, a primary-access
	// ClusterDefinition otherwise sits permanently in an error Condition
	// ("no driver specified") even though it's not misconfigured at all.
	// module-auth/tunnel still don't need this — they keep a real driver,
	// so ordinary reconciliation is correct for them.
	AccessMethod string `yaml:"accessMethod,omitempty" json:"accessMethod,omitempty"`
}

// AccessMethodPrimary mirrors hyvev1alpha1.AccessMethodPrimary — duplicated
// rather than imported, same "internal/types stays independent of the CRD
// package" precedent as every other mirrored constant/type in this file.
const AccessMethodPrimary = "primary"

// ClusterMetadata represents cluster metadata
type ClusterMetadata struct {
	Name   string `yaml:"name" json:"name"`
	Region string `yaml:"region" json:"region"`
}

// ClusterDefinition represents a complete cluster definition
type ClusterDefinition struct {
	APIVersion string          `yaml:"apiVersion" json:"apiVersion"`
	Kind       string          `yaml:"kind" json:"kind"`
	Metadata   ClusterMetadata `yaml:"metadata" json:"metadata"`
	Spec       ClusterSpec     `yaml:"spec" json:"spec"`
}

// ReconcileAction represents the type of action to take on a cluster
type ReconcileAction int

const (
	ActionNone ReconcileAction = iota
	ActionCreate
	ActionUpdate
	ActionDelete
)
