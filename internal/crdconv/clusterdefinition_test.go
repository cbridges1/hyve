package crdconv

import (
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/types"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestRunnerImage_RoundTripsBetweenCRDAndTypesShapes guards the wiring
// module.JobRunner's cluster-mode image resolution depends on:
// ClusterDefinitionSpec.Runner.Image must survive both conversion
// directions unchanged — CRD-to-internal (what internal/reconcile actually
// reads) and internal-to-CRD (what CRDStateProvider.SaveClusterDefinition
// and file-mode's own CRD-shaped YAML persistence write back out).
func TestRunnerImage_RoundTripsBetweenCRDAndTypesShapes(t *testing.T) {
	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Runner: hyvev1alpha1.RunnerSpec{Image: "ghcr.io/org/mod-runner:1.0.0"},
		},
	}

	def := ToTypesClusterDefinition(cr)
	assert.Equal(t, "ghcr.io/org/mod-runner:1.0.0", def.Spec.Runner.Image)

	back := FromTypesClusterDefinitionSpec(&def)
	assert.Equal(t, "ghcr.io/org/mod-runner:1.0.0", back.Runner.Image)
}

func TestRunnerImage_EmptyStaysEmpty(t *testing.T) {
	cr := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	def := ToTypesClusterDefinition(cr)
	assert.Empty(t, def.Spec.Runner.Image)

	def2 := &types.ClusterDefinition{}
	back := FromTypesClusterDefinitionSpec(def2)
	assert.Empty(t, back.Runner.Image)
}
