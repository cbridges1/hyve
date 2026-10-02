package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderClusterDefinitionSpec_CopiesRunnerImage(t *testing.T) {
	tpl := TemplateSpec{
		Driver: DriverRef{Source: "github.com/org/mod", Version: "v1"},
		Runner: RunnerSpec{Image: "ghcr.io/org/mod-runner:1.0.0"},
	}
	spec := RenderClusterDefinitionSpec(tpl, "", nil)
	assert.Equal(t, "ghcr.io/org/mod-runner:1.0.0", spec.Runner.Image)
}

func TestRenderClusterDefinitionSpec_EmptyRunnerStaysEmpty(t *testing.T) {
	tpl := TemplateSpec{Driver: DriverRef{Source: "github.com/org/mod", Version: "v1"}}
	spec := RenderClusterDefinitionSpec(tpl, "", nil)
	assert.Empty(t, spec.Runner.Image)
}

// TestRenderClusterDefinitionSpec_CopiesAccess is a milestone 6
// regression test for a real gap found live: a Template's spec.access
// (including spec.access.agent) was silently dropped at the CRD schema
// level — TemplateSpec had no Access field at all — so a cluster created
// from a Template with spec.access.agent.enabled/proxy set never actually
// got hyve-agent installed, regardless of --set, -f a file, or a direct
// kubectl apply to create the Template.
func TestRenderClusterDefinitionSpec_CopiesAccess(t *testing.T) {
	tpl := TemplateSpec{
		Driver: DriverRef{Source: "github.com/org/mod", Version: "v1"},
		Access: AccessSpec{Agent: &AgentSpec{Enabled: true, Proxy: true}},
	}
	spec := RenderClusterDefinitionSpec(tpl, "", nil)
	require.NotNil(t, spec.Access.Agent)
	assert.True(t, spec.Access.Agent.Enabled)
	assert.True(t, spec.Access.Agent.Proxy)
}
