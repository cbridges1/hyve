package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeLocalModule creates repoRoot/modules/<name>/ with the given files and
// returns the local source string ("./modules/<name>") to pass to
// ValidateModule — mirrors how a real repo references a local-dev module.
func writeLocalModule(t *testing.T, repoRoot, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(repoRoot, "modules", name)
	require.NoError(t, os.MkdirAll(dir, 0755))
	for rel, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644))
	}
	return "./modules/" + name
}

func TestValidateModule_AuthOnly(t *testing.T) {
	const manifestNoAuth = `apiVersion: v1
kind: Module
metadata:
  name: k3d-auth
  version: 0.1.0
  type: authOnly
`
	const manifestWithAuthYAML = manifestNoAuth
	const authYAML = `apiVersion: v1
kind: ClusterAuth
metadata:
  name: k3d-auth
spec:
  methods:
    - name: local
      auth:
        script: "k3d kubeconfig merge test"
      exports: KUBECONFIG
`

	t.Run("authOnly module missing auth.yaml/auth.sh fails with a targeted error", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "no-auth", map[string]string{
			"module.yaml": manifestNoAuth,
		})

		errs, err := ValidateModule(repo, source, "local", nil)
		require.NoError(t, err) // infra-level error, not a validation failure
		require.NotEmpty(t, errs)
		joined := strings.Join(errs, "; ")
		assert.Contains(t, joined, "authOnly")
		assert.Contains(t, joined, "auth.yaml")
	})

	t.Run("authOnly module with auth.yaml passes", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "with-auth-yaml", map[string]string{
			"module.yaml": manifestWithAuthYAML,
			"auth.yaml":   authYAML,
		})

		errs, err := ValidateModule(repo, source, "local", nil)
		require.NoError(t, err)
		assert.Empty(t, errs)
	})

	t.Run("authOnly module with auth.sh (no .yaml) passes", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "with-auth-sh", map[string]string{
			"module.yaml": manifestNoAuth,
			"auth.sh":     "#!/bin/sh\necho ok\n",
		})

		errs, err := ValidateModule(repo, source, "local", nil)
		require.NoError(t, err)
		assert.Empty(t, errs)
	})

	t.Run("non-authOnly module is unaffected by the authOnly check", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "full-provider", map[string]string{
			"module.yaml": `apiVersion: v1
kind: Module
metadata:
  name: full-provider
  version: 0.1.0
`,
			"create.sh": "#!/bin/sh\necho 'HYVE_CLUSTER_STATUS=ACTIVE'\n",
		})

		errs, err := ValidateModule(repo, source, "local", nil)
		require.NoError(t, err)
		assert.Empty(t, errs, "a create-only module with no auth.yaml must still pass when it's not authOnly")
	})
}

func TestValidateModule_MgmtCluster(t *testing.T) {
	const manifestWithMgmtCluster = `apiVersion: v1
kind: Module
metadata:
  name: capi
  version: 0.1.0
spec:
  requirements:
    mgmtCluster: mgmt
`

	t.Run("mgmtCluster naming a nonexistent cluster fails validation", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "capi", map[string]string{
			"module.yaml": manifestWithMgmtCluster,
			"create.sh":   "#!/bin/sh\necho 'HYVE_CLUSTER_STATUS=ACTIVE'\n",
		})

		errs, err := ValidateModule(repo, source, "local", []string{"other-cluster"})
		require.NoError(t, err)
		require.NotEmpty(t, errs)
		assert.Contains(t, strings.Join(errs, "; "), `mgmtCluster "mgmt"`)
	})

	t.Run("mgmtCluster naming an existing cluster passes", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "capi", map[string]string{
			"module.yaml": manifestWithMgmtCluster,
			"create.sh":   "#!/bin/sh\necho 'HYVE_CLUSTER_STATUS=ACTIVE'\n",
		})

		errs, err := ValidateModule(repo, source, "local", []string{"mgmt"})
		require.NoError(t, err)
		assert.Empty(t, errs)
	})

	t.Run("no mgmtCluster requirement is unaffected regardless of existing clusters", func(t *testing.T) {
		repo := t.TempDir()
		source := writeLocalModule(t, repo, "plain", map[string]string{
			"module.yaml": "apiVersion: v1\nkind: Module\nmetadata:\n  name: plain\n  version: 0.1.0\n",
			"create.sh":   "#!/bin/sh\necho 'HYVE_CLUSTER_STATUS=ACTIVE'\n",
		})

		errs, err := ValidateModule(repo, source, "local", nil)
		require.NoError(t, err)
		assert.Empty(t, errs)
	})
}

func TestInitModuleSkeleton_AuthOnly(t *testing.T) {
	t.Run("authOnly scaffolds only module.yaml and auth.sh", func(t *testing.T) {
		repo := t.TempDir()
		dir, err := InitModuleSkeleton(repo, "k3d-auth", true)
		require.NoError(t, err)

		manifestData, err := os.ReadFile(filepath.Join(dir, "module.yaml"))
		require.NoError(t, err)
		assert.Contains(t, string(manifestData), "type: authOnly")

		assert.FileExists(t, filepath.Join(dir, "auth.sh"))
		assert.NoFileExists(t, filepath.Join(dir, "create.sh"))
		assert.NoFileExists(t, filepath.Join(dir, "delete.sh"))
		assert.NoFileExists(t, filepath.Join(dir, "status.sh"))
		assert.NoFileExists(t, filepath.Join(dir, "scale.sh"))

		info, err := os.Stat(filepath.Join(dir, "auth.sh"))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0111, "auth.sh should be executable")
	})

	t.Run("non-authOnly scaffolds all five stub scripts, no type field", func(t *testing.T) {
		repo := t.TempDir()
		dir, err := InitModuleSkeleton(repo, "full-provider", false)
		require.NoError(t, err)

		manifestData, err := os.ReadFile(filepath.Join(dir, "module.yaml"))
		require.NoError(t, err)
		assert.NotContains(t, string(manifestData), "type:")

		for _, f := range []string{"create.sh", "delete.sh", "status.sh", "auth.sh", "scale.sh"} {
			assert.FileExists(t, filepath.Join(dir, f))
		}
	})

	t.Run("existing directory errors regardless of authOnly", func(t *testing.T) {
		repo := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "modules", "taken"), 0755))

		_, err := InitModuleSkeleton(repo, "taken", true)
		assert.Error(t, err)
	})
}
