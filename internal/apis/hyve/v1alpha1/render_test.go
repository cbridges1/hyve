package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
