package cmd

import (
	"context"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newMigrateTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, hyvev1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func newMigrateFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	builder := clientfake.NewClientBuilder().
		WithScheme(newMigrateTestScheme(t)).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return builder.Build()
}

// resetMigrateClusterFlags restores the package-level flag variables
// migrateOneNamespace reads (migrateClusterConfigName/migrateClusterForce/
// migrateClusterSkipAccessBindings) to their cobra-registered defaults —
// these are ordinary Cobra flag vars, not per-call parameters, so tests
// that mutate them must clean up after themselves to avoid bleeding state
// into whichever test runs next in this package.
func resetMigrateClusterFlags(t *testing.T) {
	t.Helper()
	origConfigName := migrateClusterConfigName
	origForce := migrateClusterForce
	origSkipBindings := migrateClusterSkipAccessBindings
	migrateClusterConfigName = "hyve-config"
	t.Cleanup(func() {
		migrateClusterConfigName = origConfigName
		migrateClusterForce = origForce
		migrateClusterSkipAccessBindings = origSkipBindings
	})
}

// TestMigrateOneNamespace_CopiesClusterDefinitionAndCredentialsSecret
// covers the same "moves a tenant's whole namespace" path the retired
// HyveAccessBinding-copying version did, now that bindings are Postgres/
// SQLite rows (see internal/migrate.AccessBindings' own doc comment) —
// what's left to verify here is the credentials Secret sweep, the one
// still-Kubernetes-native half.
func TestMigrateOneNamespace_CopiesClusterDefinitionAndCredentialsSecret(t *testing.T) {
	resetMigrateClusterFlags(t)
	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-cluster", Namespace: "acme"},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Region: "PHX1"},
	}
	credentials := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-admin-credentials", Namespace: "acme"},
		Data:       map[string][]byte{"password-hash": []byte("hash")},
	}
	source := newMigrateFakeClient(t, cd, credentials)
	dest := newMigrateFakeClient(t)

	ok := migrateOneNamespace(context.Background(), source, dest, "acme", false)
	assert.True(t, ok)

	var gotCD hyvev1alpha1.ClusterDefinition
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: "acme", Name: "acme-cluster"}, &gotCD))
	assert.Equal(t, "PHX1", gotCD.Spec.Region)

	var gotSecret corev1.Secret
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: "acme", Name: "acme-admin-credentials"}, &gotSecret))
}

func TestMigrateOneNamespace_SkipAccessBindings(t *testing.T) {
	resetMigrateClusterFlags(t)
	migrateClusterSkipAccessBindings = true

	credentials := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "acme-admin-credentials", Namespace: "acme"}}
	source := newMigrateFakeClient(t, credentials)
	dest := newMigrateFakeClient(t)

	ok := migrateOneNamespace(context.Background(), source, dest, "acme", false)
	assert.True(t, ok)

	var gotSecret corev1.Secret
	err := dest.Get(context.Background(), types.NamespacedName{Namespace: "acme", Name: "acme-admin-credentials"}, &gotSecret)
	assert.Error(t, err, "--skip-access-bindings must mean no credentials secret is copied either")
}

// TestMigrateOneNamespace_ScopesToGivenNamespaceOnly is the regression test
// for the per-environment migration scoping refinement: migrating "acme"
// must never pull in a ClusterDefinition/HyveAccessBinding that lives in a
// different tenant's namespace on the same source cluster.
func TestMigrateOneNamespace_ScopesToGivenNamespaceOnly(t *testing.T) {
	resetMigrateClusterFlags(t)
	mine := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "mine", Namespace: "acme"}}
	notMine := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "not-mine", Namespace: "tenant-b"}}
	source := newMigrateFakeClient(t, mine, notMine)
	dest := newMigrateFakeClient(t)

	ok := migrateOneNamespace(context.Background(), source, dest, "acme", false)
	assert.True(t, ok)

	var list hyvev1alpha1.ClusterDefinitionList
	require.NoError(t, dest.List(context.Background(), &list, client.InNamespace("tenant-b")))
	assert.Empty(t, list.Items, "must not copy another tenant's ClusterDefinition")

	require.NoError(t, dest.List(context.Background(), &list, client.InNamespace("acme")))
	assert.Len(t, list.Items, 1)
}

func TestMigrateOneNamespace_DryRunWritesNothing(t *testing.T) {
	resetMigrateClusterFlags(t)
	cd := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "acme-cluster", Namespace: "acme"}}
	source := newMigrateFakeClient(t, cd)
	dest := newMigrateFakeClient(t)

	ok := migrateOneNamespace(context.Background(), source, dest, "acme", true)
	assert.True(t, ok)

	var list hyvev1alpha1.ClusterDefinitionList
	require.NoError(t, dest.List(context.Background(), &list, client.InNamespace("acme")))
	assert.Empty(t, list.Items, "dry run must not write anything")
}
