package crdconv

import (
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/types"
)

// FromTypesClusterDefinitionSpec converts def's user-owned fields into the
// CRD's Spec shape — the inverse of ToTypesClusterDefinition's spec half.
// DriverOutputs/AppliedResources are deliberately excluded (they belong on
// Status — see FromTypesClusterDefinitionStatus), matching a real
// ClusterDefinition CR's own spec/status split.
func FromTypesClusterDefinitionSpec(def *types.ClusterDefinition) hyvev1alpha1.ClusterDefinitionSpec {
	return hyvev1alpha1.ClusterDefinitionSpec{
		Region:    def.Metadata.Region,
		Driver:    FromTypesDriverRef(def.Spec.Driver),
		Runner:    hyvev1alpha1.RunnerSpec{Image: def.Spec.Runner.Image},
		Params:    def.Spec.Params,
		Workflows: FromTypesWorkflowsSpec(def.Spec.Workflows),
		Resources: FromTypesResourceRefs(def.Spec.Resources),
		Delete:    def.Spec.Delete,
		Pause:     def.Spec.Pause,
		ExpiresAt: def.Spec.ExpiresAt,
		DependsOn: def.Spec.DependsOn,
		Access: hyvev1alpha1.AccessSpec{
			AccessMethodRef:       def.Spec.AccessMethodRef,
			AccessMethodClusterID: def.Spec.AccessMethodClusterID,
		},
	}
}

// FromTypesClusterDefinitionStatus converts def's reconciler-owned fields
// into the CRD's Status shape — used by file mode to build the
// cluster-state/<name>.state.yaml sidecar (the file-mode analogue of a real
// ClusterDefinition's status subresource).
func FromTypesClusterDefinitionStatus(def *types.ClusterDefinition) hyvev1alpha1.ClusterDefinitionStatus {
	driverOutputs, applied := FromTypesStatus(def)
	return hyvev1alpha1.ClusterDefinitionStatus{
		DriverOutputs:    driverOutputs,
		AppliedResources: applied,
	}
}

func FromTypesDriverRef(d types.DriverRef) hyvev1alpha1.DriverRef {
	return hyvev1alpha1.DriverRef{Source: d.Source, Version: d.Version}
}

func FromTypesWorkflowRef(r types.WorkflowRef) hyvev1alpha1.WorkflowRef {
	return hyvev1alpha1.WorkflowRef{Name: r.Name, Source: r.Source, Path: r.Path}
}

func FromTypesWorkflowRefs(rs []types.WorkflowRef) []hyvev1alpha1.WorkflowRef {
	if rs == nil {
		return nil
	}
	out := make([]hyvev1alpha1.WorkflowRef, len(rs))
	for i, r := range rs {
		out[i] = FromTypesWorkflowRef(r)
	}
	return out
}

func FromTypesWorkflowsSpec(w types.WorkflowsSpec) hyvev1alpha1.WorkflowsSpec {
	return hyvev1alpha1.WorkflowsSpec{
		BeforeCreate: FromTypesWorkflowRefs(w.BeforeCreate),
		OnCreate:     FromTypesWorkflowRefs(w.OnCreate),
		AfterCreate:  FromTypesWorkflowRefs(w.AfterCreate),
		OnDelete:     FromTypesWorkflowRefs(w.OnDelete),
		AfterDelete:  FromTypesWorkflowRefs(w.AfterDelete),
		PreReconcile: FromTypesWorkflowRefs(w.PreReconcile),
	}
}

func FromTypesResourceRefs(rs []types.ResourceRef) []hyvev1alpha1.ResourceRef {
	if rs == nil {
		return nil
	}
	out := make([]hyvev1alpha1.ResourceRef, len(rs))
	for i, r := range rs {
		out[i] = hyvev1alpha1.ResourceRef{
			Name:      r.Name,
			Source:    r.Source,
			Namespace: r.Namespace,
			Delete:    r.Delete,
			Helm:      FromTypesHelmSpec(r.Helm),
			Secret:    FromTypesSecretSpec(r.Secret),
		}
	}
	return out
}

func FromTypesHelmSpec(h *types.HelmSpec) *hyvev1alpha1.HelmSpec {
	if h == nil {
		return nil
	}
	return &hyvev1alpha1.HelmSpec{
		Chart:     h.Chart,
		Repo:      h.Repo,
		Version:   h.Version,
		Namespace: h.Namespace,
		Values:    h.Values,
	}
}

func FromTypesSecretSpec(s *types.SecretSpec) *hyvev1alpha1.SecretSpec {
	if s == nil {
		return nil
	}
	keys := make([]hyvev1alpha1.SecretKeyRef, len(s.Keys))
	for i, k := range s.Keys {
		keys[i] = hyvev1alpha1.SecretKeyRef{Env: k.Env, Key: k.Key}
	}
	return &hyvev1alpha1.SecretSpec{Namespace: s.Namespace, Type: s.Type, Keys: keys}
}
