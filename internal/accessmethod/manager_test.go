package accessmethod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeAccessMethodFile(t *testing.T, dir, filename, name, provider, serverURL string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	content := "apiVersion: hyve.io/v1alpha1\nkind: AccessMethod\nmetadata:\n  name: " + name +
		"\nspec:\n  provider: " + provider + "\n  serverURL: " + serverURL + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644))
}

func TestGetAccessMethod_Found(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "access-methods")
	writeAccessMethodFile(t, dir, "corp-rancher.yaml", "corp-rancher", "rancher", "https://rancher.example.com")

	m := NewManager(root)
	am, err := m.GetAccessMethod("corp-rancher")
	require.NoError(t, err)
	assert.Equal(t, "corp-rancher", am.Name)
	assert.Equal(t, "rancher", am.Spec.Provider)
	assert.Equal(t, "https://rancher.example.com", am.Spec.ServerURL)
}

func TestGetAccessMethod_NotFound(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "access-methods")
	writeAccessMethodFile(t, dir, "other.yaml", "other", "rancher", "https://other.example.com")

	m := NewManager(root)
	_, err := m.GetAccessMethod("corp-rancher")
	assert.Error(t, err)
}

func TestGetAccessMethod_MissingDirectory(t *testing.T) {
	root := t.TempDir() // access-methods/ never created
	m := NewManager(root)
	_, err := m.GetAccessMethod("corp-rancher")
	assert.Error(t, err)
}

func TestGetAccessMethod_RejectsWrongKind(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "access-methods")
	require.NoError(t, os.MkdirAll(dir, 0755))
	// Legacy/wrong apiVersion-kind must be rejected, same as
	// internal/template's decodeTemplate rejects a pre-unification format.
	content := "apiVersion: v1\nkind: Tunnel\nmetadata:\n  name: corp-rancher\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corp-rancher.yaml"), []byte(content), 0644))

	m := NewManager(root)
	_, err := m.GetAccessMethod("corp-rancher")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found", "a decode failure surfaces as not-found with the underlying reason, not a silent skip")
}

func TestListAccessMethods_MissingDirectory_ReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	list, err := m.ListAccessMethods()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestListAccessMethods_MultipleFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "access-methods")
	writeAccessMethodFile(t, dir, "corp-rancher.yaml", "corp-rancher", "rancher", "https://rancher.example.com")
	writeAccessMethodFile(t, dir, "corp-teleport.yaml", "corp-teleport", "teleport", "https://teleport.example.com")

	m := NewManager(root)
	list, err := m.ListAccessMethods()
	require.NoError(t, err)
	require.Len(t, list, 2)

	names := map[string]string{}
	for _, am := range list {
		names[am.Name] = am.Spec.Provider
	}
	assert.Equal(t, "rancher", names["corp-rancher"])
	assert.Equal(t, "teleport", names["corp-teleport"])
}

func TestListAccessMethods_IgnoresNonYAMLFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "access-methods")
	writeAccessMethodFile(t, dir, "corp-rancher.yaml", "corp-rancher", "rancher", "https://rancher.example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# docs"), 0644))

	m := NewManager(root)
	list, err := m.ListAccessMethods()
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// TestAccessMethodFile_IsRealCRYAML confirms a local file is literally
// valid AccessMethod CR YAML — the whole point of the file being
// kubectl apply -f-able unmodified, same guarantee internal/template's own
// decodeTemplate enforces for Template files.
func TestAccessMethodFile_IsRealCRYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "access-methods")
	writeAccessMethodFile(t, dir, "corp-rancher.yaml", "corp-rancher", "rancher", "https://rancher.example.com")

	data, err := os.ReadFile(filepath.Join(dir, "corp-rancher.yaml"))
	require.NoError(t, err)
	am, err := decodeAccessMethod("corp-rancher.yaml", data)
	require.NoError(t, err)
	assert.Equal(t, APIVersion, am.APIVersion)
	assert.Equal(t, Kind, am.Kind)
}
