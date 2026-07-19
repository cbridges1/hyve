package reconcile

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/types"
)

func TestHelmConfigHash_Deterministic(t *testing.T) {
	h := &types.HelmSpec{
		Chart:   "cert-manager",
		Repo:    "https://charts.jetstack.io",
		Version: "v1.14.0",
	}
	values := map[string]string{"installCRDs": "true", "replicaCount": "2"}
	assert.Equal(t, helmConfigHash(h, values), helmConfigHash(h, values))
}

func TestHelmConfigHash_MapKeyOrderDoesNotMatter(t *testing.T) {
	a := &types.HelmSpec{Chart: "c"}
	valuesA := map[string]string{"a": "1", "b": "2", "c": "3"}
	valuesB := map[string]string{"c": "3", "a": "1", "b": "2"}
	assert.Equal(t, helmConfigHash(a, valuesA), helmConfigHash(a, valuesB))
}

func TestHelmConfigHash_SensitiveToChanges(t *testing.T) {
	base := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0"}
	baseValues := map[string]string{"installCRDs": "true"}
	baseHash := helmConfigHash(base, baseValues)

	t.Run("chart change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "other-chart", Version: "v1.14.0"}
		assert.NotEqual(t, baseHash, helmConfigHash(h, baseValues))
	})

	t.Run("version change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.15.0"}
		assert.NotEqual(t, baseHash, helmConfigHash(h, baseValues))
	})

	t.Run("values change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0"}
		assert.NotEqual(t, baseHash, helmConfigHash(h, map[string]string{"installCRDs": "false"}))
	})

	t.Run("namespace change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0", Namespace: "cert-manager"}
		assert.NotEqual(t, baseHash, helmConfigHash(h, baseValues))
	})

	t.Run("resolved value change with identical raw values: map is still drift", func(t *testing.T) {
		// Simulates an env-var-backed value (e.g. ${PANGOLIN_ORG_ID}) whose
		// underlying value changed — the hash must be computed over the
		// resolved value, not the literal "${...}" reference, or a real
		// config change would go undetected.
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0"}
		assert.NotEqual(t,
			helmConfigHash(h, map[string]string{"orgId": "org-old"}),
			helmConfigHash(h, map[string]string{"orgId": "org-new"}),
		)
	})
}

func TestHelmChartArgs(t *testing.T) {
	h := &types.HelmSpec{
		Chart:     "cert-manager",
		Repo:      "https://charts.jetstack.io",
		Version:   "v1.14.0",
		Namespace: "cert-manager",
	}
	args := helmChartArgs(h, map[string]string{"b": "2", "a": "1"})
	assert.Equal(t, []string{
		"cert-manager",
		"--repo", "https://charts.jetstack.io",
		"--version", "v1.14.0",
		"-n", "cert-manager",
		"--set", "a=1",
		"--set", "b=2",
	}, args)
}

func TestHelmChartArgs_MinimalSpec(t *testing.T) {
	h := &types.HelmSpec{Chart: "cert-manager"}
	assert.Equal(t, []string{"cert-manager"}, helmChartArgs(h, nil))
}

func TestResolveHelmValues_NoReferences(t *testing.T) {
	h := &types.HelmSpec{Values: map[string]string{"replicaCount": "2"}}
	resolved, err := resolveHelmValues(h)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"replicaCount": "2"}, resolved)
}

func TestResolveHelmValues_ExpandsSetVariable(t *testing.T) {
	t.Setenv("HYVE_TEST_ORG_ID", "org_abc123")
	h := &types.HelmSpec{Values: map[string]string{"pangolin.orgId": "${HYVE_TEST_ORG_ID}"}}
	resolved, err := resolveHelmValues(h)
	require.NoError(t, err)
	assert.Equal(t, "org_abc123", resolved["pangolin.orgId"])
}

func TestResolveHelmValues_ExpandsMultipleReferencesInOneValue(t *testing.T) {
	t.Setenv("HYVE_TEST_HOST", "pangolin.example.com")
	t.Setenv("HYVE_TEST_SCHEME", "https")
	h := &types.HelmSpec{Values: map[string]string{"url": "${HYVE_TEST_SCHEME}://${HYVE_TEST_HOST}/v1"}}
	resolved, err := resolveHelmValues(h)
	require.NoError(t, err)
	assert.Equal(t, "https://pangolin.example.com/v1", resolved["url"])
}

func TestResolveHelmValues_MissingVariableIsHardError(t *testing.T) {
	os.Unsetenv("HYVE_TEST_DEFINITELY_UNSET")
	h := &types.HelmSpec{Values: map[string]string{"pangolin.orgId": "${HYVE_TEST_DEFINITELY_UNSET}"}}
	_, err := resolveHelmValues(h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HYVE_TEST_DEFINITELY_UNSET")
}

func TestResolveHelmValues_ReportsAllMissingVariables(t *testing.T) {
	os.Unsetenv("HYVE_TEST_MISSING_A")
	os.Unsetenv("HYVE_TEST_MISSING_B")
	h := &types.HelmSpec{Values: map[string]string{
		"one": "${HYVE_TEST_MISSING_A}",
		"two": "${HYVE_TEST_MISSING_B}",
	}}
	_, err := resolveHelmValues(h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HYVE_TEST_MISSING_A")
	assert.Contains(t, err.Error(), "HYVE_TEST_MISSING_B")
}

func TestResolveHelmValues_LiteralDollarSignUntouched(t *testing.T) {
	// Bare $ or unbraced $VAR must never be treated as a reference — only
	// the ${VAR} braced form is, so a value that legitimately contains a
	// literal '$' (a generated password, say) round-trips unchanged.
	h := &types.HelmSpec{Values: map[string]string{"password": "p$ssw0rd$123"}}
	resolved, err := resolveHelmValues(h)
	require.NoError(t, err)
	assert.Equal(t, "p$ssw0rd$123", resolved["password"])
}

func TestResolveHelmValues_EmptyValues(t *testing.T) {
	h := &types.HelmSpec{}
	resolved, err := resolveHelmValues(h)
	require.NoError(t, err)
	assert.Empty(t, resolved)
}
