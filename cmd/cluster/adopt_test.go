package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
)

func TestSplitDescribeOutputs_ParamsAndDriverOutputs(t *testing.T) {
	params, driverOutputs := splitDescribeOutputs(map[string]string{
		"HYVE_PARAM_NODE_COUNT": "3",
		"HYVE_PARAM_NODE_SIZE":  "g4s.kube.medium",
		"HYVE_CLUSTER_ID":       "abc123",
	})

	assert.Equal(t, map[string]string{"node_count": "3", "node_size": "g4s.kube.medium"}, params)
	assert.Equal(t, map[string]string{"HYVE_CLUSTER_ID": "abc123"}, driverOutputs)
}

func TestSplitDescribeOutputs_Empty(t *testing.T) {
	params, driverOutputs := splitDescribeOutputs(map[string]string{})
	assert.Empty(t, params)
	assert.Empty(t, driverOutputs)
}

func TestMergeAdoptOverrides_SetWinsOverDescribe(t *testing.T) {
	merged := mergeAdoptOverrides(
		map[string]string{"node_count": "3", "node_size": "g4s.kube.medium"},
		map[string]string{"node_count": "5"},
	)
	assert.Equal(t, map[string]string{"node_count": "5", "node_size": "g4s.kube.medium"}, merged)
}

func TestMergeAdoptOverrides_NoSet(t *testing.T) {
	merged := mergeAdoptOverrides(map[string]string{"node_count": "3"}, map[string]string{})
	assert.Equal(t, map[string]string{"node_count": "3"}, merged)
}

func TestMergeAdoptOverrides_NoDescribe(t *testing.T) {
	merged := mergeAdoptOverrides(map[string]string{}, map[string]string{"node_count": "5"})
	assert.Equal(t, map[string]string{"node_count": "5"}, merged)
}

// TestAdoptPrecedence_TemplateDescribeSet exercises the full precedence
// chain (template default < describe < --set) through the same
// GenerateClusterDefinition call adopt.go itself makes, using a fake
// template rather than a fake StateProvider/module — the merge logic under
// test is pure and doesn't touch disk, so there's nothing a heavier fake
// would add here.
func TestAdoptPrecedence_TemplateDescribeSet(t *testing.T) {
	tmpl := &template.Template{
		Metadata: template.TemplateMetadata{Name: "civo-k3s"},
		Spec: template.TemplateSpec{
			Driver: types.DriverRef{Source: "example.com/org/hyve-civo-module", Version: "v1.0.0"},
			Region: "NYC1",
			Params: map[string]string{
				"node_count": "1",
				"node_size":  "g4s.kube.small",
				"cni_plugin": "flannel",
			},
		},
	}

	describeOutputs := map[string]string{
		"HYVE_PARAM_NODE_COUNT": "3",
		"HYVE_PARAM_NODE_SIZE":  "g4s.kube.medium",
	}
	explicitSet := map[string]string{"node_count": "5"}

	describeParams, driverOutputs := splitDescribeOutputs(describeOutputs)
	renderOverrides := mergeAdoptOverrides(describeParams, explicitSet)

	clusterDef := tmpl.GenerateClusterDefinition("del-clust", "", renderOverrides)
	if clusterDef.Spec.DriverOutputs == nil {
		clusterDef.Spec.DriverOutputs = make(map[string]string)
	}
	for k, v := range driverOutputs {
		clusterDef.Spec.DriverOutputs[k] = v
	}
	clusterDef.Spec.DriverOutputs["HYVE_LAST_PARAMS_HASH"] = shared.ParamsHash(clusterDef.Spec.Params)

	// --set (5) beats describe (3) beats template default (1).
	assert.Equal(t, "5", clusterDef.Spec.Params["node_count"])
	// describe (medium) beats template default (small); nothing in --set.
	assert.Equal(t, "g4s.kube.medium", clusterDef.Spec.Params["node_size"])
	// Untouched by either describe or --set: template default survives.
	assert.Equal(t, "flannel", clusterDef.Spec.Params["cni_plugin"])
	// Template's own region default applies since adopt was called with "".
	assert.Equal(t, "NYC1", clusterDef.Metadata.Region)

	assert.Equal(t, shared.ParamsHash(clusterDef.Spec.Params), clusterDef.Spec.DriverOutputs["HYVE_LAST_PARAMS_HASH"])
	assert.NotEmpty(t, clusterDef.Spec.DriverOutputs["HYVE_LAST_PARAMS_HASH"])
}

func TestAdoptPrecedence_NoDescribeFallsBackToTemplateAndSet(t *testing.T) {
	tmpl := &template.Template{
		Metadata: template.TemplateMetadata{Name: "unraid-k3s"},
		Spec: template.TemplateSpec{
			Driver: types.DriverRef{Source: "example.com/org/hyve-unraid-k3s-module", Version: "v1.0.0"},
			Params: map[string]string{"ip_address": "10.0.0.5", "k3s_version": "latest"},
		},
	}

	// No describe output at all (module doesn't implement it).
	describeParams, driverOutputs := splitDescribeOutputs(map[string]string{})
	renderOverrides := mergeAdoptOverrides(describeParams, map[string]string{"k3s_version": "v1.34.1+k3s1"})

	clusterDef := tmpl.GenerateClusterDefinition("unraid-box", "", renderOverrides)

	assert.Empty(t, driverOutputs)
	assert.Equal(t, "10.0.0.5", clusterDef.Spec.Params["ip_address"])
	assert.Equal(t, "v1.34.1+k3s1", clusterDef.Spec.Params["k3s_version"])
}
