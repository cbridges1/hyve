package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cbridges1/hyve/internal/types"
)

func TestHelmConfigHash_Deterministic(t *testing.T) {
	h := &types.HelmSpec{
		Chart:   "cert-manager",
		Repo:    "https://charts.jetstack.io",
		Version: "v1.14.0",
		Values:  map[string]string{"installCRDs": "true", "replicaCount": "2"},
	}
	assert.Equal(t, helmConfigHash(h), helmConfigHash(h))
}

func TestHelmConfigHash_MapKeyOrderDoesNotMatter(t *testing.T) {
	a := &types.HelmSpec{Chart: "c", Values: map[string]string{"a": "1", "b": "2", "c": "3"}}
	bSpec := &types.HelmSpec{Chart: "c", Values: map[string]string{"c": "3", "a": "1", "b": "2"}}
	assert.Equal(t, helmConfigHash(a), helmConfigHash(bSpec))
}

func TestHelmConfigHash_SensitiveToChanges(t *testing.T) {
	base := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0", Values: map[string]string{"installCRDs": "true"}}
	baseHash := helmConfigHash(base)

	t.Run("chart change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "other-chart", Version: "v1.14.0", Values: map[string]string{"installCRDs": "true"}}
		assert.NotEqual(t, baseHash, helmConfigHash(h))
	})

	t.Run("version change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.15.0", Values: map[string]string{"installCRDs": "true"}}
		assert.NotEqual(t, baseHash, helmConfigHash(h))
	})

	t.Run("values change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0", Values: map[string]string{"installCRDs": "false"}}
		assert.NotEqual(t, baseHash, helmConfigHash(h))
	})

	t.Run("namespace change", func(t *testing.T) {
		h := &types.HelmSpec{Chart: "cert-manager", Version: "v1.14.0", Namespace: "cert-manager", Values: map[string]string{"installCRDs": "true"}}
		assert.NotEqual(t, baseHash, helmConfigHash(h))
	})
}

func TestHelmChartArgs(t *testing.T) {
	h := &types.HelmSpec{
		Chart:     "cert-manager",
		Repo:      "https://charts.jetstack.io",
		Version:   "v1.14.0",
		Namespace: "cert-manager",
		Values:    map[string]string{"b": "2", "a": "1"},
	}
	args := helmChartArgs(h)
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
	assert.Equal(t, []string{"cert-manager"}, helmChartArgs(h))
}
