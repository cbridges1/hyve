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

func TestWorkflowsSpecUnmarshalYAML_AfterCreate(t *testing.T) {
	doc := `
onCreate:
  - deploy-podinfo
afterCreate:
  - deploy-pangolin-node
  - deploy-wireguard
`
	var ws WorkflowsSpec
	require.NoError(t, yaml.Unmarshal([]byte(doc), &ws))
	require.Len(t, ws.OnCreate, 1)
	require.Len(t, ws.AfterCreate, 2)
	assert.Equal(t, "deploy-pangolin-node", ws.AfterCreate[0].Name)
	assert.Equal(t, "deploy-wireguard", ws.AfterCreate[1].Name)
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

func TestSecretKeyRefUnmarshalYAML_String(t *testing.T) {
	var k SecretKeyRef
	require.NoError(t, yaml.Unmarshal([]byte(`PANGOLIN_ENDPOINT`), &k))
	assert.Equal(t, "PANGOLIN_ENDPOINT", k.Env)
	assert.Equal(t, "PANGOLIN_ENDPOINT", k.Key)
}

func TestSecretKeyRefUnmarshalYAML_Mapping(t *testing.T) {
	var k SecretKeyRef
	require.NoError(t, yaml.Unmarshal([]byte(`env: PORTAINER_PASSWORD
key: password
`), &k))
	assert.Equal(t, "PORTAINER_PASSWORD", k.Env)
	assert.Equal(t, "password", k.Key)
}

func TestSecretKeyRefUnmarshalYAML_MappingOmittedKeyDefaultsToEnv(t *testing.T) {
	var k SecretKeyRef
	require.NoError(t, yaml.Unmarshal([]byte(`env: PANGOLIN_ENDPOINT`), &k))
	assert.Equal(t, "PANGOLIN_ENDPOINT", k.Env)
	assert.Equal(t, "PANGOLIN_ENDPOINT", k.Key)
}

func TestSecretKeyRefUnmarshalYAML_MappingMissingEnv(t *testing.T) {
	var k SecretKeyRef
	err := yaml.Unmarshal([]byte(`key: password`), &k)
	require.Error(t, err)
}

func TestSecretKeyRefMarshalUnmarshalRoundTrip(t *testing.T) {
	keys := []SecretKeyRef{
		{Env: "PANGOLIN_ENDPOINT", Key: "PANGOLIN_ENDPOINT"},
		{Env: "PORTAINER_PASSWORD", Key: "password"},
	}

	data, err := yaml.Marshal(keys)
	require.NoError(t, err)

	var got []SecretKeyRef
	require.NoError(t, yaml.Unmarshal(data, &got))
	assert.Equal(t, keys, got)
}

func TestResourceRefUnmarshalYAML_SecretKind(t *testing.T) {
	doc := `
name: github-secrets
secret:
  namespace: default
  keys:
    - PANGOLIN_ENDPOINT
    - env: PORTAINER_PASSWORD
      key: password
`
	var res ResourceRef
	require.NoError(t, yaml.Unmarshal([]byte(doc), &res))
	require.NotNil(t, res.Secret)
	assert.Equal(t, "default", res.Secret.Namespace)
	require.Len(t, res.Secret.Keys, 2)
	assert.Equal(t, SecretKeyRef{Env: "PANGOLIN_ENDPOINT", Key: "PANGOLIN_ENDPOINT"}, res.Secret.Keys[0])
	assert.Equal(t, SecretKeyRef{Env: "PORTAINER_PASSWORD", Key: "password"}, res.Secret.Keys[1])
}
