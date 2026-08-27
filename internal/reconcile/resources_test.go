package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/types"
)

func TestNeedsApply(t *testing.T) {
	assert.True(t, needsApply(true, true))
	// needsApply(true, false) == true is what makes it safe for
	// reconcileResources to skip kubectlDiff entirely on a resource's
	// first-ever apply (AppliedResources[name] == nil forces configChanged
	// true) — liveDiff can never change this outcome, so there's nothing to
	// lose by not computing it, and a Helm chart whose first-install render
	// references a CRD type not yet in the cluster (crds/ hasn't run yet)
	// would otherwise hard-fail kubectl diff before ever reaching the real
	// helm upgrade --install that actually installs crds/ first.
	assert.True(t, needsApply(true, false))
	assert.True(t, needsApply(false, true))
	assert.False(t, needsApply(false, false))
}

func TestDriftReason(t *testing.T) {
	assert.Equal(t, "config changed + live drift", driftReason(true, true))
	assert.Equal(t, "config changed", driftReason(true, false))
	assert.Equal(t, "live drift", driftReason(false, true))
}

func TestFindOrphanedResources_NoOrphans(t *testing.T) {
	resources := []types.ResourceRef{{Name: "a"}, {Name: "b"}}
	applied := map[string]*types.AppliedResource{
		"a": {}, "b": {},
	}
	assert.Empty(t, findOrphanedResources(resources, applied))
}

func TestFindOrphanedResources_SomeOrphaned(t *testing.T) {
	resources := []types.ResourceRef{{Name: "a"}}
	applied := map[string]*types.AppliedResource{
		"a": {}, "b": {}, "c": {},
	}
	assert.Equal(t, []string{"b", "c"}, findOrphanedResources(resources, applied))
}

func TestFindOrphanedResources_EmptyApplied(t *testing.T) {
	resources := []types.ResourceRef{{Name: "a"}}
	assert.Empty(t, findOrphanedResources(resources, map[string]*types.AppliedResource{}))
}

func TestFindOrphanedResources_EmptyResources(t *testing.T) {
	applied := map[string]*types.AppliedResource{"a": {}, "b": {}}
	assert.Equal(t, []string{"a", "b"}, findOrphanedResources(nil, applied))
}

func TestValidateResourceRef_SourceOnly(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Source: "./x.yaml"})
	assert.NoError(t, err)
}

func TestValidateResourceRef_HelmOnly(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Helm: &types.HelmSpec{Chart: "c"}})
	assert.NoError(t, err)
}

func TestValidateResourceRef_BothSet(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Source: "./x.yaml", Helm: &types.HelmSpec{Chart: "c"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most one of source, helm, or secret")
}

// TestValidateResourceRef_NeitherSet: zero of Source/Helm/Secret set is now
// valid — it means "resolve by Name" (a Resource CRD or local
// resources/<name>.yaml file), not an error, unlike the old exactly-one rule.
func TestValidateResourceRef_NeitherSet(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a"})
	assert.NoError(t, err)
}

func TestValidateResourceRef_SecretOnly(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Secret: &types.SecretSpec{Keys: []types.SecretKeyRef{{Env: "X", Key: "X"}}}})
	assert.NoError(t, err)
}

func TestValidateResourceRef_SourceAndSecretBothSet(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Source: "./x.yaml", Secret: &types.SecretSpec{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most one of source, helm, or secret")
}

func TestValidateResourceRef_HelmAndSecretBothSet(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Helm: &types.HelmSpec{Chart: "c"}, Secret: &types.SecretSpec{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most one of source, helm, or secret")
}

func TestValidateResourceRef_AllThreeSet(t *testing.T) {
	err := validateResourceRef(types.ResourceRef{Name: "a", Source: "./x.yaml", Helm: &types.HelmSpec{Chart: "c"}, Secret: &types.SecretSpec{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most one of source, helm, or secret")
}
