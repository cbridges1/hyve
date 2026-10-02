package api

import (
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBinding creates identity's binding directly in store, within
// orgName's own default environment — the Store-backed equivalent of the
// retired newBinding/newBindingInNamespace CRD fixtures.
func newTestBinding(t *testing.T, store *orgdb.Store, orgName, identity, role string) {
	t.Helper()
	org, err := store.GetOrganizationByName(t.Context(), orgName)
	require.NoError(t, err)
	env, err := store.GetEnvironmentByName(t.Context(), org.ID, orgdb.DefaultEnvironmentName)
	require.NoError(t, err)
	_, err = store.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: orgName, OrganizationID: &org.ID, EnvironmentID: &env.ID, SubjectType: orgdb.SubjectTypeLocal,
		Identity: identity, Role: role, ServiceAccountName: orgdb.ServiceAccountNameForRole(role), ServiceAccountNamespace: orgName,
	})
	require.NoError(t, err)
}

func TestFindBindingBySubject_Match(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, orgdb.DefaultEnvironmentName)
	newTestBinding(t, store, testNamespace, "cedric", hyvev1alpha1.RoleAdmin)
	s := &Server{OrgStore: store, Namespace: "hyve-control-plane"}

	b, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	assert.Equal(t, "cedric", b.Identity)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, b.Role)
}

func TestFindBindingBySubject_NoMatch(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, testNamespace, orgdb.DefaultEnvironmentName)
	newTestBinding(t, store, testNamespace, "cedric", hyvev1alpha1.RoleAdmin)
	s := &Server{OrgStore: store, Namespace: "hyve-control-plane"}

	_, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "someone-else")
	assert.Error(t, err)
}

func TestFindBindingBySubject_NoMatch_NoOrganization(t *testing.T) {
	s := &Server{OrgStore: newTestOrgStore(t), Namespace: "hyve-control-plane"}

	_, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	assert.Error(t, err, "a namespace with no matching organization at all must still fail closed, not panic or silently succeed")
}

func TestFindBindingBySubject_TypeMustAlsoMatch(t *testing.T) {
	store := newTestOrgStore(t)
	org, envs := newOrgWithEnvironments(t, store, testNamespace, orgdb.DefaultEnvironmentName)
	env := envs[orgdb.DefaultEnvironmentName]
	// Same identity value, different SubjectType — must not match a local lookup.
	_, err := store.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: testNamespace, OrganizationID: &org.ID, EnvironmentID: &env.ID, SubjectType: orgdb.SubjectTypeOIDC,
		Identity: "cedric", Role: hyvev1alpha1.RoleAdmin, ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: testNamespace,
	})
	require.NoError(t, err)
	s := &Server{OrgStore: store, Namespace: "hyve-control-plane"}

	_, err = s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	assert.Error(t, err, "an oidc-typed binding must not satisfy a local subject lookup")
}

// TestFindBindingBySubject_MultipleEnvironments_HighestRoleWins proves the
// resolution rule for an identity with more than one environment-scoped
// binding in the same organization — this is the coarse, org-level lookup
// used by login and the ordinary role gate (RequireRole), not a
// per-environment-resource authorization check, so it can't error on
// "ambiguous" the way the retired CRD-based version did (that version only
// ever had one binding per (namespace, identity) at all, since environment
// scoping didn't exist yet).
func TestFindBindingBySubject_MultipleEnvironments_HighestRoleWins(t *testing.T) {
	store := newTestOrgStore(t)
	org, envs := newOrgWithEnvironments(t, store, testNamespace, "dev", "staging")
	for env, role := range map[string]string{"dev": hyvev1alpha1.RoleReadOnly, "staging": hyvev1alpha1.RoleAdmin} {
		e := envs[env]
		_, err := store.CreateBinding(t.Context(), orgdb.Binding{
			Namespace: testNamespace, OrganizationID: &org.ID, EnvironmentID: &e.ID, SubjectType: orgdb.SubjectTypeLocal,
			Identity: "cedric", Role: role, ServiceAccountName: orgdb.ServiceAccountNameForRole(role), ServiceAccountNamespace: testNamespace,
		})
		require.NoError(t, err)
	}
	s := &Server{OrgStore: store, Namespace: "hyve-control-plane"}

	b, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, b.Role, "admin (staging) must win over read-only (dev)")
}

// TestFindBindingBySubject_OtherOrganizationIsInvisible is the regression
// test for the cross-tenant leak namespace/organization scoping exists to
// close: a binding that matches by identity but lives in a different
// organization must never be found.
func TestFindBindingBySubject_OtherOrganizationIsInvisible(t *testing.T) {
	store := newTestOrgStore(t)
	newOrgWithEnvironments(t, store, "tenant-b", orgdb.DefaultEnvironmentName)
	newTestBinding(t, store, "tenant-b", "cedric", hyvev1alpha1.RoleAdmin)
	s := &Server{OrgStore: store, Namespace: "hyve-control-plane"}

	_, err := s.findBindingBySubject(t.Context(), testNamespace, orgdb.SubjectTypeLocal, "cedric")
	assert.Error(t, err, "a binding in another tenant's organization must not be visible to this namespace's lookup")
}

// TestFindBindingBySubject_ControlPlaneScope proves namespace == s.Namespace
// resolves to organizationID=nil (the superadmin scope), not a lookup
// against some organization literally named/namespaced the control plane's
// own value (which validateOrganizationName already forbids creating).
func TestFindBindingBySubject_ControlPlaneScope(t *testing.T) {
	store := newTestOrgStore(t)
	_, err := store.CreateBinding(t.Context(), orgdb.Binding{
		Namespace: "hyve-control-plane", SubjectType: orgdb.SubjectTypeLocal, Identity: "root", Role: hyvev1alpha1.RoleSuperadmin,
		ServiceAccountName: "hyve-access-admin", ServiceAccountNamespace: "hyve-control-plane",
	})
	require.NoError(t, err)
	s := &Server{OrgStore: store, Namespace: "hyve-control-plane"}

	b, err := s.findBindingBySubject(t.Context(), "hyve-control-plane", orgdb.SubjectTypeLocal, "root")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleSuperadmin, b.Role)
}
