package types

import "fmt"

// ResourceRef declares one Kubernetes resource hyve should own, drift-check,
// and re-apply on every reconcile cycle. See ClusterSpec.Resources. Exactly
// one of Source, Helm, or Secret must be set.
type ResourceRef struct {
	// Name identifies this resource within Resources / AppliedResources.
	// Must be unique within a cluster's resource list. For a Helm resource
	// this also doubles as the Helm release name; for a Secret resource,
	// the resulting Secret object's name.
	Name string `yaml:"name" json:"name"`

	// Source is a local path ("./resource-files/x.yaml") or a remote ref
	// ("github.com/org/repo//path/to/file.yaml[@version]"), the same
	// convention as spec.driver.source. Always resolves to exactly one
	// file — no directory-expansion form. Mutually exclusive with Helm/Secret.
	Source string `yaml:"source,omitempty" json:"source,omitempty"`

	// Namespace is applied via `kubectl apply/diff -n <namespace>` as a
	// default; an object's own metadata.namespace still wins per normal
	// kubectl behavior. Only meaningful when Source is set — a Helm
	// resource uses HelmSpec.Namespace instead, and a Secret resource uses
	// SecretSpec.Namespace instead. Optional.
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	// Delete marks this resource entry for removal. On the next reconcile,
	// hyve deletes the tracked objects (or uninstalls the Helm release),
	// removes the AppliedResources entry, and removes this entry from
	// Resources itself (self-cleaning). Always honored regardless of
	// strictResourceDelete.
	Delete bool `yaml:"delete,omitempty" json:"delete,omitempty"`

	// Helm declares this resource as a Helm chart release instead of a raw
	// manifest. Mutually exclusive with Source/Secret.
	Helm *HelmSpec `yaml:"helm,omitempty" json:"helm,omitempty"`

	// Secret declares this resource as a v1/Secret rendered from hyve's own
	// process environment at reconcile time, applied through the same
	// generic manifest path a Source resource uses. Mutually exclusive with
	// Source/Helm.
	Secret *SecretSpec `yaml:"secret,omitempty" json:"secret,omitempty"`
}

// HelmSpec declares a Helm chart release to install/upgrade and drift-check.
type HelmSpec struct {
	Chart     string            `yaml:"chart" json:"chart"`
	Repo      string            `yaml:"repo,omitempty" json:"repo,omitempty"`
	Version   string            `yaml:"version,omitempty" json:"version,omitempty"`
	Namespace string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Values    map[string]string `yaml:"values,omitempty" json:"values,omitempty"`
}

// SecretKeyRef is one entry in SecretSpec.Keys. Accepts two YAML forms in
// the same list, mirroring WorkflowRef's string-or-mapping pattern:
//   - a plain scalar string: the process environment variable name, which
//     doubles as the resulting Secret data key (identity mapping) — e.g.
//     "PANGOLIN_ENDPOINT"
//   - a mapping: {env: "PORTAINER_PASSWORD", key: "password"} to resolve
//     from one environment variable but store under a different Secret key,
//     e.g. to satisfy a chart's fixed expected key name.
type SecretKeyRef struct {
	// Env is the process environment variable name to resolve at reconcile
	// time. Always required.
	Env string `yaml:"-" json:"env"`

	// Key is the resulting Secret data key name. Defaults to Env when set
	// via the bare-string form (or when the mapping form omits it).
	Key string `yaml:"-" json:"key"`
}

// UnmarshalYAML mirrors WorkflowRef's existing string-or-mapping pattern.
func (k *SecretKeyRef) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		*k = SecretKeyRef{Env: name, Key: name}
		return nil
	}
	type raw struct {
		Env string `yaml:"env"`
		Key string `yaml:"key,omitempty"`
	}
	var rw raw
	if err := unmarshal(&rw); err != nil {
		return fmt.Errorf("secret key entry must be a string (environment variable name) or a mapping with 'env': %w", err)
	}
	if rw.Env == "" {
		return fmt.Errorf("secret key entry mapping must set 'env'")
	}
	key := rw.Key
	if key == "" {
		key = rw.Env
	}
	*k = SecretKeyRef{Env: rw.Env, Key: key}
	return nil
}

// MarshalYAML renders an identity mapping as a bare string and a rename as
// an {env, key} mapping.
func (k SecretKeyRef) MarshalYAML() (interface{}, error) {
	if k.Key == "" || k.Key == k.Env {
		return k.Env, nil
	}
	type raw struct {
		Env string `yaml:"env"`
		Key string `yaml:"key"`
	}
	return raw{Env: k.Env, Key: k.Key}, nil
}

// SecretSpec declares a Kubernetes v1/Secret rendered from hyve's own
// process environment at reconcile time (os.Environ() semantics — the same
// mechanism used elsewhere in this codebase, e.g. workflow step execution)
// and applied through the exact same generic manifest path a Source
// resource uses (kubectlDiff/kubectlApply/kubectlDeleteObjects). A key
// listed here whose environment variable is unset is a hard reconcile
// error — deliberately the opposite of silently skipping, which let a
// previous hand-rolled CI workflow doing this same job silently no-op
// instead of failing loudly.
type SecretSpec struct {
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	// Type is the Kubernetes Secret type (e.g. "kubernetes.io/tls").
	// Defaults to "Opaque" when empty, matching `kubectl create secret
	// generic`.
	Type string `yaml:"type,omitempty" json:"type,omitempty"`

	// Keys are the environment variables to resolve at reconcile time and
	// the Secret data keys they populate. See SecretKeyRef.
	Keys []SecretKeyRef `yaml:"keys" json:"keys"`
}

// AppliedObject is enough identity to `kubectl delete <kind> <name> [-n ns]`
// later without needing the original manifest bytes. For a Helm-applied
// resource this is retained for inspection (`hyve cluster resources`) parity
// with manifest resources, but is not used for deletion — see
// AppliedResource.Helm.
type AppliedObject struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Namespace  string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Name       string `yaml:"name" json:"name"`
}

// AppliedResource is the reconciler-owned record of what hyve currently
// believes it applied for one Resources entry — never hand-edit. Mirrors
// the DriverOutputs pattern at resource granularity.
type AppliedResource struct {
	// SourceSHA256 is a content hash used to detect config drift: for a
	// manifest resource, the sha256 of the resolved manifest bytes; for a
	// Helm resource, the sha256 of a canonical (sorted-key) serialization
	// of the HelmSpec (chart/repo/version/values).
	SourceSHA256 string `yaml:"sourceSHA256" json:"sourceSHA256"`

	// Helm is true if this resource was applied via `helm upgrade
	// --install` rather than `kubectl apply`. Determines whether removal
	// uses helmUninstall or per-object kubectl delete — needed because the
	// original ResourceRef may already be gone by the time this entry is
	// pruned (e.g. an orphan with no matching Resources entry at all).
	Helm bool `yaml:"helm,omitempty" json:"helm,omitempty"`

	// Namespace is the namespace this resource was applied into. Needed
	// for `helm uninstall -n <namespace>` (Helm resources) without the
	// original ResourceRef around.
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	AppliedAt string          `yaml:"appliedAt" json:"appliedAt"` // RFC 3339
	Objects   []AppliedObject `yaml:"objects,omitempty" json:"objects,omitempty"`
}
