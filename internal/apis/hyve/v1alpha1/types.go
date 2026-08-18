package v1alpha1

// DriverRef identifies the module (driver) that manages a cluster. Mirrors
// internal/types.DriverRef.
type DriverRef struct {
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
}

// WorkflowRef identifies one workflow to run for a lifecycle hook.
// Structured-only in the CRD form (unlike internal/types.WorkflowRef, which
// also accepts a bare YAML scalar as shorthand for {name: "..."} in file
// mode) — a CRD's spec goes through the Kubernetes API's standard JSON
// decoding, which has no equivalent to that YAML-level custom-unmarshal
// trick, so the local-name form must always be spelled out as {name: "..."}
// here. Set exactly one of Name or Source.
type WorkflowRef struct {
	// Name is a local workflow name (workflows/<name>.yaml in the baked-in
	// modules dir). Mutually exclusive with Source.
	Name string `json:"name,omitempty"`

	// Source is a remote workflow ref ("github.com/org/repo//path@version").
	// Mutually exclusive with Name.
	Source string `json:"source,omitempty"`
	Path   string `json:"path,omitempty"`
}

// WorkflowsSpec defines workflows to run on cluster lifecycle events.
// Mirrors internal/types.WorkflowsSpec (minus the deprecated onDestroy
// migration, which is a file-format compatibility concern that doesn't
// apply to a CRD with no prior YAML history).
type WorkflowsSpec struct {
	BeforeCreate []WorkflowRef `json:"beforeCreate,omitempty"`
	OnCreate     []WorkflowRef `json:"onCreate,omitempty"`
	AfterCreate  []WorkflowRef `json:"afterCreate,omitempty"`
	OnDelete     []WorkflowRef `json:"onDelete,omitempty"`
	AfterDelete  []WorkflowRef `json:"afterDelete,omitempty"`
	PreReconcile []WorkflowRef `json:"preReconcile,omitempty"`
}

// HelmSpec declares a Helm chart release to install/upgrade and drift-check.
// Mirrors internal/types.HelmSpec.
type HelmSpec struct {
	Chart     string            `json:"chart,omitempty"`
	Repo      string            `json:"repo,omitempty"`
	Version   string            `json:"version,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Values    map[string]string `json:"values,omitempty"`
}

// SecretKeyRef is one entry in SecretSpec.Keys — always the structured form
// in the CRD (mirrors internal/types.SecretKeyRef's {env, key} mapping; that
// type's bare-scalar shorthand is a YAML-only convenience, same caveat as
// WorkflowRef above).
type SecretKeyRef struct {
	Env string `json:"env"`
	Key string `json:"key,omitempty"`
}

// SecretSpec declares a Kubernetes v1/Secret rendered from the controller's
// own process environment at reconcile time. Mirrors internal/types.SecretSpec.
type SecretSpec struct {
	Namespace string         `json:"namespace,omitempty"`
	Type      string         `json:"type,omitempty"`
	Keys      []SecretKeyRef `json:"keys"`
}

// ResourceRef declares one Kubernetes resource hyve should own, drift-check,
// and re-apply on every reconcile cycle. Mirrors internal/types.ResourceRef.
// Exactly one of Source, Helm, or Secret must be set.
type ResourceRef struct {
	Name      string      `json:"name"`
	Source    string      `json:"source,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
	Delete    bool        `json:"delete,omitempty"`
	Helm      *HelmSpec   `json:"helm,omitempty"`
	Secret    *SecretSpec `json:"secret,omitempty"`
}

// AppliedObject is enough identity to `kubectl delete <kind> <name> [-n ns]`
// later without needing the original manifest bytes. Mirrors
// internal/types.AppliedObject.
type AppliedObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

// AppliedResource is the reconciler-owned record of what hyve currently
// believes it applied for one Resources entry. Mirrors
// internal/types.AppliedResource.
type AppliedResource struct {
	SourceSHA256 string          `json:"sourceSHA256"`
	Helm         bool            `json:"helm,omitempty"`
	Namespace    string          `json:"namespace,omitempty"`
	AppliedAt    string          `json:"appliedAt"`
	Objects      []AppliedObject `json:"objects,omitempty"`
}
