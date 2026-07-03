package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWorkflowRefUnmarshalYAML_String(t *testing.T) {
	var r WorkflowRef
	require.NoError(t, yaml.Unmarshal([]byte(`local-name`), &r))
	assert.Equal(t, "local-name", r.Name)
	assert.Empty(t, r.Source)
	assert.Empty(t, r.Path)
	assert.False(t, r.IsRemote())
}

func TestWorkflowRefUnmarshalYAML_Mapping(t *testing.T) {
	var r WorkflowRef
	require.NoError(t, yaml.Unmarshal([]byte(`source: github.com/org/repo//a.yaml
path: b.yaml
`), &r))
	assert.Empty(t, r.Name)
	assert.Equal(t, "github.com/org/repo//a.yaml", r.Source)
	assert.Equal(t, "b.yaml", r.Path)
	assert.True(t, r.IsRemote())
}

func TestWorkflowRefUnmarshalYAML_MappingMissingSource(t *testing.T) {
	var r WorkflowRef
	err := yaml.Unmarshal([]byte(`path: b.yaml`), &r)
	require.Error(t, err)
}

func TestWorkflowRefMarshalUnmarshalRoundTrip(t *testing.T) {
	refs := []WorkflowRef{
		{Name: "local-a"},
		{Source: "github.com/org/repo//a.yaml", Path: "b.yaml"},
		{Source: "github.com/org/repo//a.yaml@v1.0.0"},
	}

	data, err := yaml.Marshal(refs)
	require.NoError(t, err)

	var got []WorkflowRef
	require.NoError(t, yaml.Unmarshal(data, &got))
	assert.Equal(t, refs, got)
}

func TestWorkflowsSpecUnmarshalYAML_MixedList(t *testing.T) {
	doc := `
onCreate:
  - local-name
  - source: github.com/org/repo//a.yaml
    path: b.yaml
`
	var ws WorkflowsSpec
	require.NoError(t, yaml.Unmarshal([]byte(doc), &ws))
	require.Len(t, ws.OnCreate, 2)
	assert.Equal(t, "local-name", ws.OnCreate[0].Name)
	assert.False(t, ws.OnCreate[0].IsRemote())
	assert.Equal(t, "github.com/org/repo//a.yaml", ws.OnCreate[1].Source)
	assert.Equal(t, "b.yaml", ws.OnCreate[1].Path)
	assert.True(t, ws.OnCreate[1].IsRemote())
}

func TestWorkflowsSpecUnmarshalYAML_OnDestroyMigrationStillWorks(t *testing.T) {
	doc := `
onDestroy:
  - legacy-cleanup
`
	var ws WorkflowsSpec
	require.NoError(t, yaml.Unmarshal([]byte(doc), &ws))
	require.Len(t, ws.OnDelete, 1)
	assert.Equal(t, "legacy-cleanup", ws.OnDelete[0].Name)
}
