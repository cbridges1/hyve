package reconcile

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/types"
)

func TestSecretType_DefaultsToOpaque(t *testing.T) {
	assert.Equal(t, "Opaque", secretType(&types.SecretSpec{}))
}

func TestSecretType_CustomType(t *testing.T) {
	assert.Equal(t, "kubernetes.io/tls", secretType(&types.SecretSpec{Type: "kubernetes.io/tls"}))
}

func TestSecretConfigHash_Deterministic(t *testing.T) {
	s := &types.SecretSpec{Namespace: "default"}
	resolved := map[string]string{"A": "1", "B": "2"}
	assert.Equal(t, secretConfigHash(s, resolved), secretConfigHash(s, resolved))
}

func TestSecretConfigHash_MapKeyOrderDoesNotMatter(t *testing.T) {
	s := &types.SecretSpec{Namespace: "default"}
	a := map[string]string{"A": "1", "B": "2", "C": "3"}
	b := map[string]string{"C": "3", "A": "1", "B": "2"}
	assert.Equal(t, secretConfigHash(s, a), secretConfigHash(s, b))
}

func TestSecretConfigHash_SensitiveToChanges(t *testing.T) {
	base := &types.SecretSpec{Namespace: "default"}
	baseResolved := map[string]string{"A": "1"}
	baseHash := secretConfigHash(base, baseResolved)

	t.Run("namespace change", func(t *testing.T) {
		s := &types.SecretSpec{Namespace: "other"}
		assert.NotEqual(t, baseHash, secretConfigHash(s, baseResolved))
	})

	t.Run("type change", func(t *testing.T) {
		s := &types.SecretSpec{Namespace: "default", Type: "kubernetes.io/tls"}
		assert.NotEqual(t, baseHash, secretConfigHash(s, baseResolved))
	})

	t.Run("value change with same key set — the anti-outage-regression case", func(t *testing.T) {
		resolved := map[string]string{"A": "2"}
		assert.NotEqual(t, baseHash, secretConfigHash(base, resolved))
	})

	t.Run("key added", func(t *testing.T) {
		resolved := map[string]string{"A": "1", "B": "2"}
		assert.NotEqual(t, baseHash, secretConfigHash(base, resolved))
	})
}

func TestResolveSecretKeys_AllPresent(t *testing.T) {
	t.Setenv("HYVE_TEST_SECRET_ENDPOINT", "https://example.com")
	t.Setenv("HYVE_TEST_SECRET_ID", "abc123")

	s := &types.SecretSpec{Keys: []types.SecretKeyRef{
		{Env: "HYVE_TEST_SECRET_ENDPOINT", Key: "HYVE_TEST_SECRET_ENDPOINT"},
		{Env: "HYVE_TEST_SECRET_ID", Key: "HYVE_TEST_SECRET_ID"},
	}}
	resolved, err := resolveSecretKeys(s)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"HYVE_TEST_SECRET_ENDPOINT": "https://example.com",
		"HYVE_TEST_SECRET_ID":       "abc123",
	}, resolved)
}

func TestResolveSecretKeys_MissingKeyIsHardError(t *testing.T) {
	s := &types.SecretSpec{Keys: []types.SecretKeyRef{
		{Env: "HYVE_TEST_SECRET_DOES_NOT_EXIST_XYZ", Key: "HYVE_TEST_SECRET_DOES_NOT_EXIST_XYZ"},
	}}
	_, err := resolveSecretKeys(s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HYVE_TEST_SECRET_DOES_NOT_EXIST_XYZ")
}

func TestResolveSecretKeys_EmptyButSetValueIsAccepted(t *testing.T) {
	t.Setenv("HYVE_TEST_SECRET_EMPTY", "")

	s := &types.SecretSpec{Keys: []types.SecretKeyRef{{Env: "HYVE_TEST_SECRET_EMPTY", Key: "HYVE_TEST_SECRET_EMPTY"}}}
	resolved, err := resolveSecretKeys(s)
	require.NoError(t, err)
	assert.Equal(t, "", resolved["HYVE_TEST_SECRET_EMPTY"])
}

func TestResolveSecretKeys_Renaming(t *testing.T) {
	t.Setenv("HYVE_TEST_SECRET_PASSWORD", "hunter2")

	s := &types.SecretSpec{Keys: []types.SecretKeyRef{{Env: "HYVE_TEST_SECRET_PASSWORD", Key: "password"}}}
	resolved, err := resolveSecretKeys(s)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"password": "hunter2"}, resolved)
}

func TestResolveSecretKeys_DuplicateOutputKeyIsError(t *testing.T) {
	t.Setenv("HYVE_TEST_SECRET_A", "1")
	t.Setenv("HYVE_TEST_SECRET_B", "2")

	s := &types.SecretSpec{Keys: []types.SecretKeyRef{
		{Env: "HYVE_TEST_SECRET_A", Key: "shared"},
		{Env: "HYVE_TEST_SECRET_B", Key: "shared"},
	}}
	_, err := resolveSecretKeys(s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared")
}

func TestRenderSecretManifest_DefaultsTypeOpaqueAndUsesBase64Data(t *testing.T) {
	t.Setenv("HYVE_TEST_SECRET_ENDPOINT", "https://example.com")

	s := &types.SecretSpec{
		Namespace: "default",
		Keys:      []types.SecretKeyRef{{Env: "HYVE_TEST_SECRET_ENDPOINT", Key: "HYVE_TEST_SECRET_ENDPOINT"}},
	}
	manifestBytes, resolved, err := renderSecretManifest("github-secrets", s)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"HYVE_TEST_SECRET_ENDPOINT": "https://example.com"}, resolved)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(manifestBytes, &doc))
	assert.Equal(t, "v1", doc["apiVersion"])
	assert.Equal(t, "Secret", doc["kind"])
	assert.Equal(t, "Opaque", doc["type"])
	metadata := doc["metadata"].(map[string]interface{})
	assert.Equal(t, "github-secrets", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])
	// Must be data (base64), not stringData: stringData is a write-only,
	// additive convenience the API server merges into data but never prunes
	// from — confirmed empirically that a dropped stringData key leaves the
	// live object's data untouched. data is the actual SSA-tracked field.
	_, hasStringData := doc["stringData"]
	assert.False(t, hasStringData)
	encData := doc["data"].(map[string]interface{})
	decoded, err := base64.StdEncoding.DecodeString(encData["HYVE_TEST_SECRET_ENDPOINT"].(string))
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", string(decoded))
}

func TestRenderSecretManifest_CustomType(t *testing.T) {
	s := &types.SecretSpec{Namespace: "default", Type: "kubernetes.io/tls", Keys: nil}
	data, _, err := renderSecretManifest("tls-cert", s)
	require.NoError(t, err)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	assert.Equal(t, "kubernetes.io/tls", doc["type"])
}

func TestRenderSecretManifest_KeyRenaming(t *testing.T) {
	t.Setenv("HYVE_TEST_SECRET_PORTAINER_PASSWORD", "hunter2")

	s := &types.SecretSpec{
		Namespace: "portainer",
		Keys:      []types.SecretKeyRef{{Env: "HYVE_TEST_SECRET_PORTAINER_PASSWORD", Key: "password"}},
	}
	manifestBytes, resolved, err := renderSecretManifest("portainer-admin-password", s)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"password": "hunter2"}, resolved)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(manifestBytes, &doc))
	encData := doc["data"].(map[string]interface{})
	decoded, err := base64.StdEncoding.DecodeString(encData["password"].(string))
	require.NoError(t, err)
	assert.Equal(t, "hunter2", string(decoded))
	_, hasEnvNameKey := encData["HYVE_TEST_SECRET_PORTAINER_PASSWORD"]
	assert.False(t, hasEnvNameKey, "rendered Secret must be keyed by the renamed key, not the env var name")
}

func TestRenderSecretManifest_PropagatesMissingKeyError(t *testing.T) {
	s := &types.SecretSpec{Keys: []types.SecretKeyRef{{Env: "HYVE_TEST_SECRET_DOES_NOT_EXIST_XYZ", Key: "x"}}}
	_, _, err := renderSecretManifest("some-secret", s)
	require.Error(t, err)
}
