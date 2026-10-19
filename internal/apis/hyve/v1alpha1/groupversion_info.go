// Package v1alpha1 holds hyve.io/v1alpha1 types that hyve's Go code uses
// typed: User, for the host endpoint's sign-in. ClusterTemplate, Resource
// and ClusterResource are read as unstructured objects (internal/templates,
// internal/resources); their CRDs are in deploy/helm/hyve/crds.
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
