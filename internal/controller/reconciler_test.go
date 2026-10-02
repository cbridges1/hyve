package controller

import (
	"context"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestSchemeWithCore(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := newTestScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func TestFetchCLISecrets_MissingSecretReturnsNil(t *testing.T) {
	c := clientfake.NewClientBuilder().WithScheme(newTestSchemeWithCore(t)).Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}

	got := r.fetchCLISecrets(context.Background())
	assert.Nil(t, got)
}

// TestSetConditions_ReadyAndErrorStayMutuallyExclusive is the regression
// test for a real, live-confirmed bug: setCondition (singular) only ever
// upserts the one condition Type it's given, so a caller that constructs
// just a Ready condition on a successful pass, or just an Error condition
// on a failed one — Reconcile's own original code did exactly this — never
// touches the *other* type at all. A cluster that had ever genuinely
// succeeded once and later started failing every pass ended up with
// Ready: true and Error: true simultaneously, forever, since nothing ever
// flips the stale one back. setConditions (plural) closes this by always
// upserting both as one pair in the same call, confirmed here across a
// success -> failure transition.
func TestSetConditions_ReadyAndErrorStayMutuallyExclusive(t *testing.T) {
	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: testNamespace},
	}
	c := clientfake.NewClientBuilder().
		WithScheme(newTestSchemeWithCore(t)).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{}).
		WithObjects(cr).
		Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}
	ctx := context.Background()
	key := k8stypes.NamespacedName{Namespace: testNamespace, Name: "web"}

	successConds := []metav1.Condition{
		{Type: hyvev1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: "Reconciled", Message: "last reconcile succeeded"},
		{Type: hyvev1alpha1.ConditionTypeError, Status: metav1.ConditionFalse, Reason: "Reconciled", Message: "no error"},
	}
	require.NoError(t, r.setConditions(ctx, cr, 1, successConds))

	var afterSuccess hyvev1alpha1.ClusterDefinition
	require.NoError(t, c.Get(ctx, key, &afterSuccess))
	require.Len(t, afterSuccess.Status.Conditions, 2)
	assertConditionStatus(t, afterSuccess.Status.Conditions, hyvev1alpha1.ConditionTypeReady, metav1.ConditionTrue)
	assertConditionStatus(t, afterSuccess.Status.Conditions, hyvev1alpha1.ConditionTypeError, metav1.ConditionFalse)

	failureConds := []metav1.Condition{
		{Type: hyvev1alpha1.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: "ReconcileFailed", Message: "boom"},
		{Type: hyvev1alpha1.ConditionTypeError, Status: metav1.ConditionTrue, Reason: "ReconcileFailed", Message: "boom"},
	}
	require.NoError(t, r.setConditions(ctx, &afterSuccess, 1, failureConds))

	var afterFailure hyvev1alpha1.ClusterDefinition
	require.NoError(t, c.Get(ctx, key, &afterFailure))
	require.Len(t, afterFailure.Status.Conditions, 2, "must still be exactly one Ready + one Error, never a stale extra")
	assertConditionStatus(t, afterFailure.Status.Conditions, hyvev1alpha1.ConditionTypeReady, metav1.ConditionFalse)
	assertConditionStatus(t, afterFailure.Status.Conditions, hyvev1alpha1.ConditionTypeError, metav1.ConditionTrue)
}

func assertConditionStatus(t *testing.T, conds []metav1.Condition, condType string, want metav1.ConditionStatus) {
	t.Helper()
	for _, c := range conds {
		if c.Type == condType {
			assert.Equal(t, want, c.Status, "condition %q", condType)
			return
		}
	}
	t.Fatalf("condition %q not found", condType)
}

// TestResolveWorkflowIfNeeded_NoRemoteRefsIsANoOp confirms
// resolveWorkflowIfNeeded never touches the network (or hyve.lock) for a
// ClusterDefinition whose lifecycle hooks name workflows locally — the
// common case (see reconciler.go's own comment: nexus-config's own
// workflows are all local-path refs) — by using a StateProvider whose
// LocalPath() panics if ever called, so the test fails loudly if this
// no-remote-refs short-circuit regresses.
func TestResolveWorkflowIfNeeded_NoRemoteRefsIsANoOp(t *testing.T) {
	r := &ClusterDefinitionReconciler{StateProvider: nil}
	lf := &module.LockFile{Version: 1}
	def := types.ClusterDefinition{
		Spec: types.ClusterSpec{Workflows: types.WorkflowsSpec{
			OnCreate: []types.WorkflowRef{{Name: "local-workflow"}}, // Source unset: not remote
		}},
	}

	got := r.resolveWorkflowIfNeeded(context.Background(), lf, def, "")
	assert.Same(t, lf, got, "must return the same lf, untouched, with no remote refs to resolve")
}

// TestResolveResourceIfNeeded_NoRemoteRefsIsANoOp mirrors
// TestResolveWorkflowIfNeeded_NoRemoteRefsIsANoOp exactly, one tier below
// it: a ClusterDefinition whose resources are all local-path or Name-only
// refs (IsRemote() false) must short-circuit with no StateProvider access.
func TestResolveResourceIfNeeded_NoRemoteRefsIsANoOp(t *testing.T) {
	r := &ClusterDefinitionReconciler{StateProvider: nil}
	lf := &module.LockFile{Version: 1}
	def := types.ClusterDefinition{
		Spec: types.ClusterSpec{Resources: []types.ResourceRef{
			{Name: "local-resource"}, // Source unset: not remote
		}},
	}

	got := r.resolveResourceIfNeeded(context.Background(), lf, def, "")
	assert.Same(t, lf, got, "must return the same lf, untouched, with no remote refs to resolve")
}

func TestFetchCLISecrets_ReturnsSecretDataAsStrings(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: testNamespace},
		Data: map[string][]byte{
			"GITHUB_TOKEN": []byte("ghp_example"),
			"CIVO_TOKEN":   []byte("civo_example"),
		},
	}
	c := clientfake.NewClientBuilder().WithScheme(newTestSchemeWithCore(t)).WithObjects(secret).Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}

	got := r.fetchCLISecrets(context.Background())
	assert.Equal(t, "ghp_example", got["GITHUB_TOKEN"])
	assert.Equal(t, "civo_example", got["CIVO_TOKEN"])
}

func TestFetchCLISecrets_WrongNamespaceIsTreatedAsMissing(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cliSecretsName, Namespace: "other-namespace"},
		Data:       map[string][]byte{"GITHUB_TOKEN": []byte("ghp_example")},
	}
	c := clientfake.NewClientBuilder().WithScheme(newTestSchemeWithCore(t)).WithObjects(secret).Build()
	r := &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}

	got := r.fetchCLISecrets(context.Background())
	assert.Nil(t, got)
}
