// Package v1alpha1 contains the hyve.io/v1alpha1 CRD API — ClusterDefinition
// and HyveConfig — used only by controller mode (internal/controller,
// cmd/controller). Deliberately separate from internal/types, which stays
// free of any k8s.io/apimachinery dependency: local/CLI mode's YAML files
// are unmarshaled straight into internal/types.ClusterDefinition, and that
// path's binary size/dependency graph shouldn't change just because
// controller mode exists elsewhere in the same module. The sub-types here
// (WorkflowsSpec, ResourceRef, ...) intentionally duplicate internal/types'
// field lists rather than importing them directly, so controller-gen's
// deepcopy generation has everything it needs within one package — see each
// type's own doc comment for the internal/types equivalent it mirrors.
//
// +kubebuilder:object:generate=true
// +groupName=hyve.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "hyve.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
