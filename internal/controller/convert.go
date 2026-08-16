package controller

import (
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/types"
)

// toTypesClusterDefinition merges a ClusterDefinition CR's spec and status
// into the same in-memory shape file mode produces by merging a primary
// clusters/<name>.yaml with its cluster-state/<name>.state.yaml sidecar
// (see state.Manager.mergeSidecar) — this is what lets internal/reconcile's
// engine run identically regardless of which StateProvider supplied the
// definition. cr.DeletionTimestamp being set (a real `kubectl delete` was
// issued) is treated exactly like spec.delete: true — both are just two
// ways to ask for the same thing, so internal/reconcile never needs to know
// which one happened.
func toTypesClusterDefinition(cr *hyvev1alpha1.ClusterDefinition) types.ClusterDefinition {
	return types.ClusterDefinition{
		APIVersion: hyvev1alpha1.GroupVersion.String(),
		Kind:       "Cluster",
		Metadata: types.ClusterMetadata{
			Name:   cr.Name,
			Region: cr.Spec.Region,
		},
		Spec: types.ClusterSpec{
			Driver:           toTypesDriverRef(cr.Spec.Driver),
			Params:           cr.Spec.Params,
			DriverOutputs:    cr.Status.DriverOutputs,
			Workflows:        toTypesWorkflowsSpec(cr.Spec.Workflows),
			Resources:        toTypesResourceRefs(cr.Spec.Resources),
			AppliedResources: toTypesAppliedResources(cr.Status.AppliedResources),
			Delete:           cr.Spec.Delete || cr.DeletionTimestamp != nil,
			Pause:            cr.Spec.Pause,
			ExpiresAt:        cr.Spec.ExpiresAt,
			DependsOn:        cr.Spec.DependsOn,
		},
	}
}

func toTypesDriverRef(d hyvev1alpha1.DriverRef) types.DriverRef {
	return types.DriverRef{Source: d.Source, Version: d.Version}
}

func toTypesWorkflowRef(r hyvev1alpha1.WorkflowRef) types.WorkflowRef {
	return types.WorkflowRef{Name: r.Name, Source: r.Source, Path: r.Path}
}

func toTypesWorkflowRefs(rs []hyvev1alpha1.WorkflowRef) []types.WorkflowRef {
	if rs == nil {
		return nil
	}
	out := make([]types.WorkflowRef, len(rs))
	for i, r := range rs {
		out[i] = toTypesWorkflowRef(r)
	}
	return out
}

func toTypesWorkflowsSpec(w hyvev1alpha1.WorkflowsSpec) types.WorkflowsSpec {
	return types.WorkflowsSpec{
		BeforeCreate: toTypesWorkflowRefs(w.BeforeCreate),
		OnCreate:     toTypesWorkflowRefs(w.OnCreate),
		AfterCreate:  toTypesWorkflowRefs(w.AfterCreate),
		OnDelete:     toTypesWorkflowRefs(w.OnDelete),
		AfterDelete:  toTypesWorkflowRefs(w.AfterDelete),
		PreReconcile: toTypesWorkflowRefs(w.PreReconcile),
	}
}

func toTypesResourceRefs(rs []hyvev1alpha1.ResourceRef) []types.ResourceRef {
	if rs == nil {
		return nil
	}
	out := make([]types.ResourceRef, len(rs))
	for i, r := range rs {
		out[i] = types.ResourceRef{
			Name:      r.Name,
			Source:    r.Source,
			Namespace: r.Namespace,
			Delete:    r.Delete,
			Helm:      toTypesHelmSpec(r.Helm),
			Secret:    toTypesSecretSpec(r.Secret),
		}
	}
	return out
}

func toTypesHelmSpec(h *hyvev1alpha1.HelmSpec) *types.HelmSpec {
	if h == nil {
		return nil
	}
	return &types.HelmSpec{
		Chart:     h.Chart,
		Repo:      h.Repo,
		Version:   h.Version,
		Namespace: h.Namespace,
		Values:    h.Values,
	}
}

func toTypesSecretSpec(s *hyvev1alpha1.SecretSpec) *types.SecretSpec {
	if s == nil {
		return nil
	}
	keys := make([]types.SecretKeyRef, len(s.Keys))
	for i, k := range s.Keys {
		keys[i] = types.SecretKeyRef{Env: k.Env, Key: k.Key}
	}
	return &types.SecretSpec{Namespace: s.Namespace, Type: s.Type, Keys: keys}
}

func toTypesAppliedResources(m map[string]*hyvev1alpha1.AppliedResource) map[string]*types.AppliedResource {
	if m == nil {
		return nil
	}
	out := make(map[string]*types.AppliedResource, len(m))
	for k, v := range m {
		objs := make([]types.AppliedObject, len(v.Objects))
		for i, o := range v.Objects {
			objs[i] = types.AppliedObject{APIVersion: o.APIVersion, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name}
		}
		out[k] = &types.AppliedResource{
			SourceSHA256: v.SourceSHA256,
			Helm:         v.Helm,
			Namespace:    v.Namespace,
			AppliedAt:    v.AppliedAt,
			Objects:      objs,
		}
	}
	return out
}

// fromTypesStatus converts def's reconciler-owned fields (DriverOutputs/
// AppliedResources) into the CRD's status shape — the inverse of
// toTypesClusterDefinition's status half. Spec is deliberately not
// converted back: CRDStateProvider.SaveClusterDefinition only ever writes
// the status subresource (see its own doc comment for why), so a caller
// mutating def.Spec (e.g. resources.go pruning a delete:true entry) has
// that change silently not persisted in CRD mode — spec stays external,
// user-owned input, same as any other Kubernetes controller.
func fromTypesStatus(def *types.ClusterDefinition) (map[string]string, map[string]*hyvev1alpha1.AppliedResource) {
	var applied map[string]*hyvev1alpha1.AppliedResource
	if def.Spec.AppliedResources != nil {
		applied = make(map[string]*hyvev1alpha1.AppliedResource, len(def.Spec.AppliedResources))
		for k, v := range def.Spec.AppliedResources {
			objs := make([]hyvev1alpha1.AppliedObject, len(v.Objects))
			for i, o := range v.Objects {
				objs[i] = hyvev1alpha1.AppliedObject{APIVersion: o.APIVersion, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name}
			}
			applied[k] = &hyvev1alpha1.AppliedResource{
				SourceSHA256: v.SourceSHA256,
				Helm:         v.Helm,
				Namespace:    v.Namespace,
				AppliedAt:    v.AppliedAt,
				Objects:      objs,
			}
		}
	}
	return def.Spec.DriverOutputs, applied
}
