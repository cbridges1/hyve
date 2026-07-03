package types

// ResourceRef declares one Kubernetes resource hyve should own, drift-check,
// and re-apply on every reconcile cycle. See ClusterSpec.Resources. Exactly
// one of Source or Helm must be set.
type ResourceRef struct {
	// Name identifies this resource within Resources / AppliedResources.
	// Must be unique within a cluster's resource list. For a Helm resource
	// this also doubles as the Helm release name.
	Name string `yaml:"name"`

	// Source is a local path ("./resource-files/x.yaml") or a remote ref
	// ("github.com/org/repo//path/to/file.yaml[@version]"), the same
	// convention as spec.driver.source. Always resolves to exactly one
	// file — no directory-expansion form. Mutually exclusive with Helm.
	Source string `yaml:"source,omitempty"`

	// Namespace is applied via `kubectl apply/diff -n <namespace>` as a
	// default; an object's own metadata.namespace still wins per normal
	// kubectl behavior. Only meaningful when Source is set — a Helm
	// resource uses HelmSpec.Namespace instead. Optional.
	Namespace string `yaml:"namespace,omitempty"`

	// Delete marks this resource entry for removal. On the next reconcile,
	// hyve deletes the tracked objects (or uninstalls the Helm release),
	// removes the AppliedResources entry, and removes this entry from
	// Resources itself (self-cleaning). Always honored regardless of
	// strictResourceDelete.
	Delete bool `yaml:"delete,omitempty"`

	// Helm declares this resource as a Helm chart release instead of a raw
	// manifest. Mutually exclusive with Source.
	Helm *HelmSpec `yaml:"helm,omitempty"`
}

// HelmSpec declares a Helm chart release to install/upgrade and drift-check.
type HelmSpec struct {
	Chart     string            `yaml:"chart"`
	Repo      string            `yaml:"repo,omitempty"`
	Version   string            `yaml:"version,omitempty"`
	Namespace string            `yaml:"namespace,omitempty"`
	Values    map[string]string `yaml:"values,omitempty"`
}

// AppliedObject is enough identity to `kubectl delete <kind> <name> [-n ns]`
// later without needing the original manifest bytes. For a Helm-applied
// resource this is retained for inspection (`hyve cluster resources`) parity
// with manifest resources, but is not used for deletion — see
// AppliedResource.Helm.
type AppliedObject struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Namespace  string `yaml:"namespace,omitempty"`
	Name       string `yaml:"name"`
}

// AppliedResource is the reconciler-owned record of what hyve currently
// believes it applied for one Resources entry — never hand-edit. Mirrors
// the DriverOutputs pattern at resource granularity.
type AppliedResource struct {
	// SourceSHA256 is a content hash used to detect config drift: for a
	// manifest resource, the sha256 of the resolved manifest bytes; for a
	// Helm resource, the sha256 of a canonical (sorted-key) serialization
	// of the HelmSpec (chart/repo/version/values).
	SourceSHA256 string `yaml:"sourceSHA256"`

	// Helm is true if this resource was applied via `helm upgrade
	// --install` rather than `kubectl apply`. Determines whether removal
	// uses helmUninstall or per-object kubectl delete — needed because the
	// original ResourceRef may already be gone by the time this entry is
	// pruned (e.g. an orphan with no matching Resources entry at all).
	Helm bool `yaml:"helm,omitempty"`

	// Namespace is the namespace this resource was applied into. Needed
	// for `helm uninstall -n <namespace>` (Helm resources) without the
	// original ResourceRef around.
	Namespace string `yaml:"namespace,omitempty"`

	AppliedAt string          `yaml:"appliedAt"` // RFC 3339
	Objects   []AppliedObject `yaml:"objects,omitempty"`
}
