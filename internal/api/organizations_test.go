package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func newTestOrgStore(t *testing.T) *orgdb.Store {
	t.Helper()
	s, err := orgdb.Open("sqlite", filepath.Join(t.TempDir(), "orgdb.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func newOrganizationsTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerOrganizationRoutes(mux)
	return mux
}

func doOrganizationRequest(t *testing.T, s *Server, role string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(data))
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleCreateOrganization_RequiresSuperadmin(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleAdmin, createOrganizationRequest{Name: "acme"})
	assert.Equal(t, http.StatusForbidden, rec.Code, "an ordinary tenant admin must not be able to create a new organization")
}

func TestHandleCreateOrganization_MissingName_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleCreateOrganization_RejectsReservedNames covers both reserved
// names end to end (not just validateOrganizationName in isolation): the
// control plane's own namespace (testNamespace, "hyve-system") and every
// casing/spacing form of "control plane" — the always-present pseudo-
// environment EnvironmentSwitcher shows for "no tenant selected".
func TestHandleCreateOrganization_RejectsReservedNames(t *testing.T) {
	for _, name := range []string{
		"hyve-system", "Hyve-System", "HYVE-SYSTEM",
		"control plane", "Control plane", "CONTROL PLANE", "control-plane",
	} {
		t.Run(name, func(t *testing.T) {
			s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
			rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: name})
			assert.Equal(t, http.StatusBadRequest, rec.Code)

			// Must reject before creating anything — a caller retrying with
			// a real name afterward shouldn't find a half-created "acme"-
			// shaped mess left over from the rejected attempt.
			var ns corev1.Namespace
			err := s.Client.Get(t.Context(), types.NamespacedName{Name: name}, &ns)
			assert.Error(t, err, "no namespace should have been created for a rejected name")
			_, dbErr := s.OrgStore.GetOrganizationByName(t.Context(), name)
			assert.ErrorIs(t, dbErr, orgdb.ErrNotFound, "no organization row should have been created for a rejected name")
		})
	}
}

func TestValidateOrganizationName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"acme", false},
		{"controlling", false}, // must not false-positive-match on a substring
		{"control-planning", false},
		{"hyve-system", true},
		{"HYVE-SYSTEM", true},
		{"control plane", true},
		{"Control Plane", true},
		{"control-plane", true},
		{"CONTROL-PLANE", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOrganizationName(c.name, testNamespace)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHandleCreateOrganization_CreatesNamespaceRBACAndRecord(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code)

	var dto organizationDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "acme", dto.Name)
	assert.Equal(t, "acme", dto.Namespace)

	ctx := t.Context()

	var ns corev1.Namespace
	require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Name: "acme"}, &ns), "the tenant namespace must exist")

	for _, want := range []struct{ sa, clusterRole string }{
		{"hyve-access-admin", "cluster-admin"},
		{"hyve-access-readonly", "view"},
	} {
		var sa corev1.ServiceAccount
		require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Namespace: "acme", Name: want.sa}, &sa),
			"ServiceAccount %s must exist in the new tenant namespace", want.sa)

		var rb rbacv1.RoleBinding
		require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Namespace: "acme", Name: want.sa}, &rb),
			"RoleBinding %s must exist in the new tenant namespace", want.sa)
		assert.Equal(t, want.clusterRole, rb.RoleRef.Name)
		assert.Equal(t, "ClusterRole", rb.RoleRef.Kind, "must scope the ClusterRole via a namespaced RoleBinding, never a ClusterRoleBinding")
		require.Len(t, rb.Subjects, 1)
		assert.Equal(t, "acme", rb.Subjects[0].Namespace, "the RoleBinding must only grant within the new tenant's own namespace")
	}

	org, err := s.OrgStore.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err, "the organization row must exist in the Store")
	assert.Equal(t, "acme", org.Namespace)

	env, err := s.OrgStore.GetEnvironmentByName(ctx, org.ID, orgdb.DefaultEnvironmentName)
	require.NoError(t, err, "a default environment must be created alongside the organization")
	assert.Equal(t, orgdb.DefaultEnvironmentName, env.Name)
}

// TestHandleCreateOrganization_WithAdminIdentity_SeedsBinding proves the
// optional adminIdentity/adminRole fields land as a real Binding row,
// scoped to the org's own default environment (never a wildcard/org-wide
// grant — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "no wildcard grant"
// decision).
func TestHandleCreateOrganization_WithAdminIdentity_SeedsBinding(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{
		Name: "acme", AdminIdentity: "alice", AdminRole: hyvev1alpha1.RoleAdmin,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	ctx := t.Context()
	org, err := s.OrgStore.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)

	binding, err := s.OrgStore.FindBindingBySubject(ctx, "acme", orgdb.SubjectTypeLocal, "alice")
	require.NoError(t, err)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, binding.Role)

	env, err := s.OrgStore.GetEnvironmentByName(ctx, org.ID, orgdb.DefaultEnvironmentName)
	require.NoError(t, err)
	require.NotNil(t, binding.EnvironmentID)
	assert.Equal(t, env.ID, *binding.EnvironmentID, "an unscoped admin grant must resolve to the default environment, never a wildcard")
}

func TestHandleCreateOrganization_MismatchedAdminFields_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme", AdminIdentity: "alice"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "adminIdentity without adminRole must be rejected, not silently create an unscoped/malformed binding")
}

// TestHandleCreateOrganization_IdempotentAfterPartialFailure proves a
// re-POST fills in whatever's missing instead of erroring on "already
// exists" — the whole point of checking existence before creating each
// step (see handleCreateOrganization's own doc comment).
func TestHandleCreateOrganization_IdempotentAfterPartialFailure(t *testing.T) {
	// Simulate a partial failure: the Postgres/SQLite organization (and its
	// default environment) already exist, and so does the namespace/RBAC
	// scaffolding (as if a first POST got that far) — nothing is actually
	// missing here, this proves the fully-idempotent, everything-already-
	// exists case succeeds, not just the "one piece missing" case.
	store := newTestOrgStore(t)
	_, _, err := store.CreateOrganizationWithDefaults(t.Context(), orgdb.Organization{Name: "acme", Namespace: "acme"}, "", "")
	require.NoError(t, err)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	adminSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "hyve-access-admin", Namespace: "acme"}}
	roSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "hyve-access-readonly", Namespace: "acme"}}
	s := &Server{Client: newFakeClient(t, ns, adminSA, roSA), OrgStore: store, Namespace: testNamespace}

	rec := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"})
	require.Equal(t, http.StatusCreated, rec.Code, "re-POST once everything already exists must still succeed, not 409")

	rec2 := doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"})
	assert.Equal(t, http.StatusCreated, rec2.Code, "a second re-POST must also succeed")
}

func doDeleteOrganizationRequest(t *testing.T, s *Server, role, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/organizations/"+name, nil)
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleDeleteOrganization_RequiresSuperadmin(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec := doDeleteOrganizationRequest(t, s, hyvev1alpha1.RoleAdmin, "acme")
	assert.Equal(t, http.StatusForbidden, rec.Code, "an ordinary tenant admin must not be able to delete an organization")
}

func TestHandleDeleteOrganization_UnknownName_404(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doDeleteOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "does-not-exist")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandleDeleteOrganization_PendingDeletionSurvivesRestart_CompletesOnSweep
// proves the crash-safety property HYVE-ORGANIZATION-MODEL-PROPOSAL.md
// claims for organization deletion (Milestone 5, nexus-config/docs): the
// durable state is the organization row's own pending_deletion flag plus
// the Namespace's own existence/finalizer — both survive a process
// restart with no recovery logic beyond "check again," which is exactly
// what Server.SweepPendingOrganizationDeletions does. This test doesn't
// wire up internal/controller's NamespaceReconciler at all (that's
// covered separately, against a real kube-apiserver, by
// internal/controller/namespace_reconciler_envtest_test.go) — it only
// proves the API-server half: the row is not deleted while the Namespace
// still exists, and a later, independent sweep call (standing in for "the
// next process, after a restart") finishes the job once it does.
func TestHandleDeleteOrganization_PendingDeletionSurvivesRestart_CompletesOnSweep(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	ctx := t.Context()
	org, err := s.OrgStore.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)

	rec := doDeleteOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme")
	require.Equal(t, http.StatusAccepted, rec.Code)

	// The row is durably marked pending_deletion — even a process crash
	// right here, before the Namespace ever finishes terminating, leaves
	// something for the next process's sweep to resume from.
	reloaded, err := s.OrgStore.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)
	assert.True(t, reloaded.PendingDeletion)

	// The Namespace itself must still be present — ensureNamespace always
	// sets hyvev1alpha1.OrganizationNamespaceFinalizer, so the fake
	// client's Delete only sets DeletionTimestamp, matching real
	// kube-apiserver finalizer semantics.
	var ns corev1.Namespace
	require.NoError(t, s.Client.Get(ctx, types.NamespacedName{Name: "acme"}, &ns))
	require.NotNil(t, ns.DeletionTimestamp, "Delete must have been issued against the namespace")

	// A sweep run right now must not finish the deletion — the Namespace
	// still exists (Terminating, not gone).
	s.SweepPendingOrganizationDeletions(ctx)
	_, err = s.OrgStore.GetOrganization(ctx, org.ID)
	require.NoError(t, err, "the organization row must survive a sweep while its Namespace still exists")

	// Simulate internal/controller's NamespaceReconciler having finished
	// its own job (every hyve-owned object confirmed gone) by clearing the
	// finalizer directly, the same Update it would issue.
	controllerutil.RemoveFinalizer(&ns, hyvev1alpha1.OrganizationNamespaceFinalizer)
	require.NoError(t, s.Client.Update(ctx, &ns))

	var checkGone corev1.Namespace
	err = s.Client.Get(ctx, types.NamespacedName{Name: "acme"}, &checkGone)
	require.Error(t, err, "the namespace should be fully gone once its finalizer list is empty")

	// This stands in for "the next process, after a restart" — a fresh
	// sweep call, proving the pending_deletion row picked back up
	// correctly with no other state needed.
	s.SweepPendingOrganizationDeletions(ctx)
	_, err = s.OrgStore.GetOrganization(ctx, org.ID)
	assert.ErrorIs(t, err, orgdb.ErrNotFound, "the organization row must be deleted once its namespace is fully gone")

	envs, err := s.OrgStore.ListEnvironments(ctx, org.ID)
	require.NoError(t, err)
	assert.Empty(t, envs, "environments scoped to the deleted organization must be deleted too")
}

// TestHandleDeleteOrganization_Idempotent proves re-issuing DELETE against
// an already-pending_deletion organization is safe — matching every other
// organization-lifecycle handler's re-request-safe design in this file.
func TestHandleDeleteOrganization_Idempotent(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec1 := doDeleteOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme")
	require.Equal(t, http.StatusAccepted, rec1.Code)

	rec2 := doDeleteOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme")
	assert.Equal(t, http.StatusAccepted, rec2.Code, "a second DELETE against an already-pending_deletion organization must not error")
}

func TestHandleListOrganizations_RequiresSuperadmin(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	req := httptest.NewRequest(http.MethodGet, "/organizations", nil)
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleListOrganizations_ReturnsCreatedOrgs(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "globex"}).Code)

	req := httptest.NewRequest(http.MethodGet, "/organizations", nil)
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleSuperadmin))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var out []organizationDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out, 2)
	assert.Equal(t, "acme", out[0].Name)
	assert.Equal(t, "globex", out[1].Name)
	assert.Empty(t, out[0].ReconcilingCluster, "an organization on the home cluster must show no reconciling cluster, not a raw id")
	assert.False(t, out[0].Migrating)
}

// TestOrganizationDTO_ShowsReconcilingClusterNameAndMigratingFlag proves
// the organizationDTO gap flagged during Milestone 6 is closed: a caller
// (the web console, most directly) can now see which reconciling cluster
// an organization is actually on — by name, not a meaningless raw id — and
// whether it's currently mid-migration.
func TestOrganizationDTO_ShowsReconcilingClusterNameAndMigratingFlag(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	ctx := t.Context()
	rc, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", KubeconfigSecretNamespace: testNamespace, KubeconfigSecretName: "cell-a-kubeconfig"})
	require.NoError(t, err)
	org, err := store.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)
	require.NoError(t, store.SetOrganizationReconcilingCluster(ctx, org.ID, &rc.ID))
	migrating := "migrating"
	require.NoError(t, store.SetOrganizationMigrationStatus(ctx, org.ID, &migrating))

	req := httptest.NewRequest(http.MethodGet, "/organizations", nil)
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleSuperadmin))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var out []organizationDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "cell-a", out[0].ReconcilingCluster, "must resolve the cluster's name, not just its id")
	assert.True(t, out[0].Migrating)
}

func strPtr(s string) *string { return &s }

func doPatchOrganizationRequest(t *testing.T, s *Server, role, name string, body patchOrganizationRequest) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/organizations/"+name, bytes.NewReader(data))
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandlePatchOrganization_RequiresSuperadmin(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleAdmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("")})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandlePatchOrganization_MissingReconcilingClusterField_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlePatchOrganization_UnknownOrganization_404(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "does-not-exist", patchOrganizationRequest{ReconcilingCluster: strPtr("")})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlePatchOrganization_UnknownReconcilingCluster_400(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("does-not-exist")})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlePatchOrganization_NoOpWhenAlreadyOnTarget(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	// Already on the home cluster ("") — patching to "" again must be a
	// pure no-op, not attempt (and fail) a real migration.
	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("")})
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandlePatchOrganization_AlreadyMigrating_423(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	migrating := "migrating"
	require.NoError(t, store.SetOrganizationMigrationStatus(t.Context(), org.ID, &migrating))

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("")})
	assert.Equal(t, http.StatusLocked, rec.Code)
}

func TestHandlePatchOrganization_PendingDeletion_409(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	org, err := store.GetOrganizationByName(t.Context(), "acme")
	require.NoError(t, err)
	require.NoError(t, store.MarkOrganizationPendingDeletion(t.Context(), org.ID))

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("cell-a")})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestHandlePatchOrganization_MigratesEverythingAndFlipsReconcilingCluster
// is Milestone 6's own core proof: every namespace-scoped object type
// internal/migrate knows how to copy actually lands on the destination
// cluster, the destination gets its own Namespace/RBAC scaffolding
// (ensureNamespace/ensureAccessRoleScaffolding, not assumed to already
// exist), and the organization row's own reconciling_cluster_id is
// flipped with reconciling_cluster_migration_status cleared in the same
// write. The destination "cluster" is a second, independent fake client
// injected directly into Server.reconcilingClusterClients (bypassing
// reconcilingClusterClientHandle's own kubeconfig-Secret-parsing path,
// which needs a real reachable cluster) — this test is scoped to proving
// the copy/lock/FK-flip logic, not kubeconfig parsing.
func TestHandlePatchOrganization_MigratesEverythingAndFlipsReconcilingCluster(t *testing.T) {
	store := newTestOrgStore(t)
	homeClient := newFakeClient(t)
	s := &Server{Client: homeClient, OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	ctx := t.Context()
	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "acme", Labels: map[string]string{hyveEnvironmentLabel: "staging"}},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Region: "local", Driver: hyvev1alpha1.DriverRef{Source: "./module", Version: "latest"}},
	}
	require.NoError(t, homeClient.Create(ctx, cd))
	tpl := &hyvev1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "base", Namespace: "acme"}}
	require.NoError(t, homeClient.Create(ctx, tpl))
	wf := &hyvev1alpha1.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "deploy", Namespace: "acme"}}
	require.NoError(t, homeClient.Create(ctx, wf))
	res := &hyvev1alpha1.Resource{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "acme"}, Spec: hyvev1alpha1.ResourceSpec{Manifest: "apiVersion: v1\nkind: ConfigMap"}}
	require.NoError(t, homeClient.Create(ctx, res))

	destClient := newFakeClient(t)
	rc, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", KubeconfigSecretNamespace: testNamespace, KubeconfigSecretName: "cell-a-kubeconfig"})
	require.NoError(t, err)
	s.reconcilingClusterClients = map[string]*reconcilingClusterHandle{rc.ID: {Client: destClient}}

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("cell-a")})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var gotNS corev1.Namespace
	require.NoError(t, destClient.Get(ctx, types.NamespacedName{Name: "acme"}, &gotNS), "the destination must get its own Namespace, not assumed to already exist")
	assert.True(t, controllerutil.ContainsFinalizer(&gotNS, hyvev1alpha1.OrganizationNamespaceFinalizer))

	var gotSA corev1.ServiceAccount
	require.NoError(t, destClient.Get(ctx, types.NamespacedName{Namespace: "acme", Name: "hyve-access-admin"}, &gotSA), "the destination must get its own RBAC scaffolding")

	var gotCD hyvev1alpha1.ClusterDefinition
	require.NoError(t, destClient.Get(ctx, types.NamespacedName{Namespace: "acme", Name: "prod"}, &gotCD))
	assert.Equal(t, "./module", gotCD.Spec.Driver.Source)
	assert.Equal(t, "staging", gotCD.Labels[hyveEnvironmentLabel], "the environment label must survive migration, not just the spec")

	var gotTpl hyvev1alpha1.Template
	require.NoError(t, destClient.Get(ctx, types.NamespacedName{Namespace: "acme", Name: "base"}, &gotTpl))

	var gotWf hyvev1alpha1.Workflow
	require.NoError(t, destClient.Get(ctx, types.NamespacedName{Namespace: "acme", Name: "deploy"}, &gotWf))

	var gotRes hyvev1alpha1.Resource
	require.NoError(t, destClient.Get(ctx, types.NamespacedName{Namespace: "acme", Name: "cfg"}, &gotRes))
	assert.Equal(t, "apiVersion: v1\nkind: ConfigMap", gotRes.Spec.Manifest)

	org, err := store.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)
	require.NotNil(t, org.ReconcilingClusterID)
	assert.Equal(t, rc.ID, *org.ReconcilingClusterID)
	assert.Nil(t, org.ReconcilingClusterMigrationStatus, "the migration lock must be cleared once the copy completes")
}

// TestHandlePatchOrganization_FailedMigration_LeavesOrganizationOnOriginalCluster
// proves the "abort cleanly" property handlePatchOrganization's own doc
// comment claims: a destination that can't be provisioned (here, a nil
// Client — every call into it panics, simulating a totally unreachable
// cluster at the client-construction layer) must leave the organization's
// reconciling_cluster_id untouched and clear the migration lock, not leave
// it stuck 'migrating' or half-moved.
func TestHandlePatchOrganization_FailedMigration_LeavesOrganizationOnOriginalCluster(t *testing.T) {
	store := newTestOrgStore(t)
	s := &Server{Client: newFakeClient(t), OrgStore: store, Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	ctx := t.Context()
	_, err := store.CreateReconcilingCluster(ctx, orgdb.ReconcilingCluster{Name: "cell-a", KubeconfigSecretNamespace: testNamespace, KubeconfigSecretName: "cell-a-kubeconfig"})
	require.NoError(t, err)
	// No kubeconfig Secret was ever created for cell-a, and it's not
	// pre-seeded into the client cache — reconcilingClusterClientHandle
	// will fail to build a real client for it (the Secret Get 404s),
	// exactly the "unreachable destination" failure mode this test wants.

	rec := doPatchOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, "acme", patchOrganizationRequest{ReconcilingCluster: strPtr("cell-a")})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	org, err := store.GetOrganizationByName(ctx, "acme")
	require.NoError(t, err)
	assert.Nil(t, org.ReconcilingClusterID, "a failed migration must leave the organization on its original (home) cluster")
	assert.Nil(t, org.ReconcilingClusterMigrationStatus, "a failed migration must clear the lock, not leave it stuck")
}

func doListEnvironmentsRequest(t *testing.T, s *Server, role, callerNamespace, orgName string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/organizations/"+orgName+"/environments", nil)
	req = req.WithContext(contextWithRole(req.Context(), role))
	req = req.WithContext(contextWithNamespace(req.Context(), callerNamespace))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	return rec
}

func doCreateEnvironmentRequest(t *testing.T, s *Server, role, callerNamespace, orgName string, body createOrgEnvironmentRequest) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/organizations/"+orgName+"/environments", bytes.NewReader(data))
	req = req.WithContext(contextWithRole(req.Context(), role))
	req = req.WithContext(contextWithNamespace(req.Context(), callerNamespace))
	rec := httptest.NewRecorder()
	newOrganizationsTestMux(s).ServeHTTP(rec, req)
	return rec
}

// TestOrgEnvironments_AdminCanManageOwnOrganization proves the Milestone
// 6 follow-up: an ordinary admin (not just a superadmin) can list and
// create environments for their own organization, reachable from their
// own tenant view — environments are entirely within one organization's
// own scope, unlike the genuinely cross-namespace endpoints elsewhere in
// this file.
func TestOrgEnvironments_AdminCanManageOwnOrganization(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	listRec := doListEnvironmentsRequest(t, s, hyvev1alpha1.RoleAdmin, "acme", "acme")
	require.Equal(t, http.StatusOK, listRec.Code)
	var envs []organizationEnvironmentDTO
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &envs))
	require.Len(t, envs, 1)
	assert.Equal(t, orgdb.DefaultEnvironmentName, envs[0].Name)

	createRec := doCreateEnvironmentRequest(t, s, hyvev1alpha1.RoleAdmin, "acme", "acme", createOrgEnvironmentRequest{Name: "staging"})
	assert.Equal(t, http.StatusCreated, createRec.Code)
}

// TestOrgEnvironments_AdminCannotReachAnotherOrganization proves the other
// half: an admin naming a *different* organization in the URL — not their
// own TenantNamespace — is rejected, not silently redirected to their own
// org or allowed through.
func TestOrgEnvironments_AdminCannotReachAnotherOrganization(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "globex"}).Code)

	// alice is globex's own admin, trying to reach acme's environments.
	listRec := doListEnvironmentsRequest(t, s, hyvev1alpha1.RoleAdmin, "globex", "acme")
	assert.Equal(t, http.StatusForbidden, listRec.Code)

	createRec := doCreateEnvironmentRequest(t, s, hyvev1alpha1.RoleAdmin, "globex", "acme", createOrgEnvironmentRequest{Name: "staging"})
	assert.Equal(t, http.StatusForbidden, createRec.Code)
}

// TestOrgEnvironments_SuperadminReachesAnyOrganization proves the
// superadmin half is unchanged — cross-organization access, matching
// every other endpoint in this file.
func TestOrgEnvironments_SuperadminReachesAnyOrganization(t *testing.T) {
	s := &Server{Client: newFakeClient(t), OrgStore: newTestOrgStore(t), Namespace: testNamespace}
	require.Equal(t, http.StatusCreated, doOrganizationRequest(t, s, hyvev1alpha1.RoleSuperadmin, createOrganizationRequest{Name: "acme"}).Code)

	rec := doListEnvironmentsRequest(t, s, hyvev1alpha1.RoleSuperadmin, testNamespace, "acme")
	assert.Equal(t, http.StatusOK, rec.Code)
}
