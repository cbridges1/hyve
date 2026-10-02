package template

import (
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// APIVersion/Kind must match the real Template CRD exactly (group
// hyve.io/v1alpha1) — a local templates/<name>.yaml file is real Template
// CR YAML, `kubectl apply -f`-able unmodified once the CRD is installed.
const (
	APIVersion = "hyve.io/v1alpha1"
	Kind       = "Template"
)

// toTemplate converts a Template CR into the in-memory Template shape
// GenerateClusterDefinition and the rest of this package operate on.
// Driver/Workflows/Resources reuse internal/crdconv's existing
// ClusterDefinition converters — types.DriverRef/WorkflowsSpec/ResourceRef
// are the exact same shared types a ClusterDefinition's spec uses.
func toTemplate(cr *hyvev1alpha1.Template) *Template {
	return &Template{
		APIVersion: cr.APIVersion,
		Kind:       cr.Kind,
		Metadata: TemplateMetadata{
			Name:        cr.Name,
			Description: cr.Spec.Description,
		},
		Spec: TemplateSpec{
			Driver:     crdconv.ToTypesDriverRef(cr.Spec.Driver),
			Runner:     TemplateRunnerConfig{Image: cr.Spec.Runner.Image},
			Params:     cr.Spec.Params,
			Region:     cr.Spec.Region,
			Workflows:  crdconv.ToTypesWorkflowsSpec(cr.Spec.Workflows),
			Resources:  crdconv.ToTypesResourceRefs(cr.Spec.Resources),
			Schedule:   cr.Spec.Schedule,
			LockParams: cr.Spec.LockParams,
		},
	}
}

// fromTemplate converts the in-memory Template shape into a Template CR for
// persisting to disk — the inverse of toTemplate.
func fromTemplate(t *Template) *hyvev1alpha1.Template {
	return &hyvev1alpha1.Template{
		TypeMeta:   metav1.TypeMeta{APIVersion: APIVersion, Kind: Kind},
		ObjectMeta: metav1.ObjectMeta{Name: t.Metadata.Name},
		Spec: hyvev1alpha1.TemplateSpec{
			Description: t.Metadata.Description,
			Driver:      crdconv.FromTypesDriverRef(t.Spec.Driver),
			Runner:      hyvev1alpha1.RunnerSpec{Image: t.Spec.Runner.Image},
			Params:      t.Spec.Params,
			Region:      t.Spec.Region,
			Workflows:   crdconv.FromTypesWorkflowsSpec(t.Spec.Workflows),
			Resources:   crdconv.FromTypesResourceRefs(t.Spec.Resources),
			Schedule:    t.Spec.Schedule,
			LockParams:  t.Spec.LockParams,
		},
	}
}
