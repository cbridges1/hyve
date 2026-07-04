package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemoveKubeconfigContext tests removing a context from kubeconfig
func TestRemoveKubeconfigContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")

	originalConfig := `apiVersion: v1
kind: Config
current-context: context-to-remove
clusters:
- name: cluster-to-remove
  cluster:
    server: https://remove.example.com
- name: cluster-to-keep
  cluster:
    server: https://keep.example.com
contexts:
- name: context-to-remove
  context:
    cluster: cluster-to-remove
    user: user-to-remove
- name: context-to-keep
  context:
    cluster: cluster-to-keep
    user: user-to-keep
users:
- name: user-to-remove
  user:
    token: remove-token
- name: user-to-keep
  user:
    token: keep-token
`

	err := os.WriteFile(configPath, []byte(originalConfig), 0600)
	require.NoError(t, err)

	err = RemoveKubeconfigContext(originalConfig, "context-to-remove", configPath)
	require.NoError(t, err)

	modifiedData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	modified := string(modifiedData)

	assert.NotContains(t, modified, "context-to-remove")
	assert.NotContains(t, modified, "cluster-to-remove")
	assert.NotContains(t, modified, "user-to-remove")
	assert.Contains(t, modified, "context-to-keep")
	assert.Contains(t, modified, "cluster-to-keep")
	assert.Contains(t, modified, "user-to-keep")
	assert.NotContains(t, modified, "current-context: context-to-remove")
}

// TestRemoveNonExistentContext tests removing a context that doesn't exist
func TestRemoveNonExistentContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")

	originalConfig := `apiVersion: v1
kind: Config
clusters:
- name: my-cluster
  cluster:
    server: https://example.com
contexts:
- name: my-context
  context:
    cluster: my-cluster
    user: my-user
users:
- name: my-user
  user:
    token: my-token
`

	err := os.WriteFile(configPath, []byte(originalConfig), 0600)
	require.NoError(t, err)

	err = RemoveKubeconfigContext(originalConfig, "non-existent", configPath)
	require.NoError(t, err)

	modifiedData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	modified := string(modifiedData)

	assert.Contains(t, modified, "my-cluster")
	assert.Contains(t, modified, "my-context")
	assert.Contains(t, modified, "my-user")
}

// TestRemoveItemByName tests the removeItemByName helper function
func TestRemoveItemByName(t *testing.T) {
	items := []map[string]interface{}{
		{"name": "item1", "data": "value1"},
		{"name": "item2", "data": "value2"},
		{"name": "item3", "data": "value3"},
	}

	result := removeItemByName(items, "item2")
	require.Len(t, result, 2)

	names := make([]string, 0, len(result))
	for _, item := range result {
		if name, ok := item["name"].(string); ok {
			names = append(names, name)
		}
	}
	assert.Contains(t, names, "item1")
	assert.Contains(t, names, "item3")
	assert.NotContains(t, names, "item2")
}

// TestMultipleContextRemoval tests removing multiple contexts sequentially
func TestMultipleContextRemoval(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")

	originalConfig := `apiVersion: v1
kind: Config
clusters:
- name: cluster1
  cluster:
    server: https://cluster1.example.com
- name: cluster2
  cluster:
    server: https://cluster2.example.com
- name: cluster3
  cluster:
    server: https://cluster3.example.com
contexts:
- name: context1
  context:
    cluster: cluster1
    user: user1
- name: context2
  context:
    cluster: cluster2
    user: user2
- name: context3
  context:
    cluster: cluster3
    user: user3
users:
- name: user1
  user:
    token: token1
- name: user2
  user:
    token: token2
- name: user3
  user:
    token: token3
`

	err := os.WriteFile(configPath, []byte(originalConfig), 0600)
	require.NoError(t, err)

	err = RemoveKubeconfigContext(originalConfig, "context1", configPath)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	err = RemoveKubeconfigContext(string(data), "context2", configPath)
	require.NoError(t, err)

	finalData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	final := string(finalData)

	assert.NotContains(t, final, "context1")
	assert.NotContains(t, final, "context2")
	assert.Contains(t, final, "context3")
}
