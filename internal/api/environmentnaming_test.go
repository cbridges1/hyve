package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func clusterKey(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}

// newOrgWithEnvironments creates an organization (namespace == name, per
// convention) and the given environments in store, returning the org and a
// name->Environment map for convenience.
func newOrgWithEnvironments(t *testing.T, store *orgdb.Store, orgName string, envNames ...string) (orgdb.Organization, map[string]orgdb.Environment) {
	t.Helper()
	org, err := store.CreateOrganization(t.Context(), orgdb.Organization{Name: orgName, Namespace: orgName})
	require.NoError(t, err)
	envs := make(map[string]orgdb.Environment, len(envNames))
	for _, name := range envNames {
		env, err := store.CreateEnvironment(t.Context(), orgdb.Environment{OrganizationID: org.ID, Name: name})
		require.NoError(t, err)
		envs[name] = env
	}
	return org, envs
}

// TestHandleCreateCluster_NoMatchingOrganization_LegacyBehavior proves
// Milestone 3 is additive: a namespace with no Organization row at all
// (every namespace that existed before Milestone 3, and the control-plane
// namespace itself) creates a cluster exactly as it always has — raw name,
// no label, no join.
func TestHandleCreateCluster_NoMatchingOrganization_LegacyBehavior(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{Name: "web"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "web", dto.Name)
	assert.Empty(t, dto.Environment)

	// The real Kubernetes object's own name must be the raw "web", not a
	// joined name — confirms no environment machinery touched it at all.
	var cd hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), clusterKey(testNamespace, "web"), &cd))
	assert.Empty(t, cd.Labels[hyveEnvironmentLabel])
}

// TestHandleCreateCluster_SingleEnvironment_DefaultsWithoutParam proves the
// "org has exactly one environment" default from
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's CLI/API surface section: ?env= can
// be omitted and still resolves.
func TestHandleCreateCluster_SingleEnvironment_DefaultsWithoutParam(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, orgdb.DefaultEnvironmentName)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{Name: "web"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "web", dto.Name, "the DTO must show the short name, not the joined one")
	assert.Equal(t, orgdb.DefaultEnvironmentName, dto.Environment)

	var cd hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), clusterKey(testNamespace, "default-web"), &cd), "the real object name must be the joined name")
	assert.Equal(t, orgdb.DefaultEnvironmentName, cd.Labels[hyveEnvironmentLabel])
}

// TestHandleCreateCluster_MultipleEnvironments_RequiresParam proves an
// ambiguous default (no ?env=, more than one environment) is rejected with
// a clear 400, not a silent guess.
func TestHandleCreateCluster_MultipleEnvironments_RequiresParam(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, "dev", "staging")
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{Name: "web"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "dev")
	assert.Contains(t, rec.Body.String(), "staging")
}

// TestHandleCreateCluster_UnknownEnvironment_Rejected proves a ?env= naming
// an environment the organization doesn't have is a clear error, not
// silently falling back to legacy/default behavior.
func TestHandleCreateCluster_UnknownEnvironment_Rejected(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, orgdb.DefaultEnvironmentName)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters?env=nonexistent", createClusterRequest{Name: "web"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "nonexistent")
}

// TestClusterEnvironments_TwoNamesCollideAcrossEnvironments_NoCollision is
// the direct proof of HYVE-ORGANIZATION-MODEL-PROPOSAL.md's core naming
// claim: two clusters named "web" in different environments of the same
// organization/namespace don't collide, and each is only ever reachable
// (and only ever displayed) through its own environment.
func TestClusterEnvironments_TwoNamesCollideAcrossEnvironments_NoCollision(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, "dev", "staging")
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	for _, env := range []string{"dev", "staging"} {
		rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, fmt.Sprintf("/clusters?env=%s", env), createClusterRequest{Name: "web"})
		require.Equal(t, http.StatusCreated, rec.Code, "creating %q-web must not collide with the other environment's own web", env)
	}

	var devCD, stagingCD hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), clusterKey(testNamespace, "dev-web"), &devCD))
	require.NoError(t, s.Client.Get(t.Context(), clusterKey(testNamespace, "staging-web"), &stagingCD))

	// GET by short name + ?env= must reach the correct one, not the other.
	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/web?env=dev", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "web", dto.Name)
	assert.Equal(t, "dev", dto.Environment)

	// GET with no ?env= at all (ambiguous org) falls back to legacy
	// behavior — treating "web" as a literal metadata.name, which doesn't
	// exist (only "dev-web"/"staging-web" do) — 404, not a guess.
	rec2 := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/web", nil)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
}

// TestHandleDeleteCluster_ScopedToEnvironment proves DELETE resolves
// ?env= the same way GET does, so deleting "web" in "dev" never touches
// "staging"'s own "web".
func TestHandleDeleteCluster_ScopedToEnvironment(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, "dev", "staging")
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}

	for _, env := range []string{"dev", "staging"} {
		rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, fmt.Sprintf("/clusters?env=%s", env), createClusterRequest{Name: "web"})
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/clusters/web?env=dev", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	var devCD, stagingCD hyvev1alpha1.ClusterDefinition
	assert.Error(t, s.Client.Get(t.Context(), clusterKey(testNamespace, "dev-web"), &devCD), "dev-web must be gone")
	assert.NoError(t, s.Client.Get(t.Context(), clusterKey(testNamespace, "staging-web"), &stagingCD), "staging-web must be untouched")
}

func TestJoinAndSplitEnvironmentName_RoundTrip(t *testing.T) {
	real := joinEnvironmentName("dev", "web")
	assert.Equal(t, "dev-web", real)
	assert.Equal(t, "web", splitEnvironmentPrefix(real, "dev"))
}

func TestResolveResourceEnvironment_NilOrgStore_LegacyBehavior(t *testing.T) {
	s := &Server{Namespace: testNamespace}
	env, ok, err := s.resolveResourceEnvironment(t.Context(), testNamespace, "")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, env.ID)
}
