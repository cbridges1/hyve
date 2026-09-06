package controller

import (
	"context"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/reconcile"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ctrlRequest builds a ctrl.Request for the named object in testNamespace —
// a small local helper since no shared one exists yet in this package's
// tests.
func ctrlRequest(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: k8stypes.NamespacedName{Namespace: testNamespace, Name: name}}
}

// newWorkflowRunReconciler builds a WorkflowRunReconciler backed by one fake
// client shared between the controller-runtime Client and the
// reconcile.StateProvider (CRDStateProvider implements both — same
// arrangement cmd/controller/run.go wires for real) — mirrors
// newFakeProvider's own WithStatusSubresource pattern, extended to
// WorkflowRun since this reconciler writes that kind's status subresource
// too.
func newWorkflowRunReconciler(t *testing.T, objs ...client.Object) *WorkflowRunReconciler {
	t.Helper()
	builder := clientfake.NewClientBuilder().
		WithScheme(newTestSchemeWithCore(t)).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{}, &hyvev1alpha1.WorkflowRun{})
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	c := builder.Build()
	provider := &CRDStateProvider{Client: c, Namespace: testNamespace, ConfigName: "hyve-config", ModulesDirPath: "/var/lib/hyve/modules"}
	return &WorkflowRunReconciler{
		Client:        c,
		Reconciler:    reconcile.NewReconciler(provider),
		StateProvider: provider,
		Namespace:     testNamespace,
	}
}

func TestWorkflowRunReconciler_MissingWorkflowRun_NoOp(t *testing.T) {
	r := newWorkflowRunReconciler(t)
	res, err := r.Reconcile(context.Background(), ctrlRequest("does-not-exist"))
	require.NoError(t, err)
	assert.Zero(t, res)
}

// TestWorkflowRunReconciler_AlreadyTerminalPhase_NoOp confirms a WorkflowRun
// past Pending/"" is never re-run — the one-shot guard at the top of
// Reconcile.
func TestWorkflowRunReconciler_AlreadyTerminalPhase_NoOp(t *testing.T) {
	wr := &hyvev1alpha1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-1", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowRunSpec{WorkflowRef: hyvev1alpha1.WorkflowRef{Name: "install-podinfo"}, ClusterRef: "some-cluster"},
		Status:     hyvev1alpha1.WorkflowRunStatus{Phase: hyvev1alpha1.WorkflowRunPhaseSucceeded, Message: "workflow completed"},
	}
	r := newWorkflowRunReconciler(t, wr)

	_, err := r.Reconcile(context.Background(), ctrlRequest("run-1"))
	require.NoError(t, err)

	var fresh hyvev1alpha1.WorkflowRun
	require.NoError(t, r.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: testNamespace, Name: "run-1"}, &fresh))
	assert.Equal(t, hyvev1alpha1.WorkflowRunPhaseSucceeded, fresh.Status.Phase)
	assert.Equal(t, "workflow completed", fresh.Status.Message)
}

// TestWorkflowRunReconciler_MissingCluster_SetsFailed confirms a WorkflowRun
// referencing a nonexistent ClusterDefinition fails fast with a clear
// message, rather than panicking or hanging.
func TestWorkflowRunReconciler_MissingCluster_SetsFailed(t *testing.T) {
	wr := &hyvev1alpha1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-2", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowRunSpec{WorkflowRef: hyvev1alpha1.WorkflowRef{Name: "install-podinfo"}, ClusterRef: "no-such-cluster"},
	}
	r := newWorkflowRunReconciler(t, wr)

	_, err := r.Reconcile(context.Background(), ctrlRequest("run-2"))
	require.NoError(t, err)

	var fresh hyvev1alpha1.WorkflowRun
	require.NoError(t, r.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: testNamespace, Name: "run-2"}, &fresh))
	assert.Equal(t, hyvev1alpha1.WorkflowRunPhaseFailed, fresh.Status.Phase)
	assert.Contains(t, fresh.Status.Message, "no-such-cluster")
	assert.NotNil(t, fresh.Status.StartedAt)
	assert.NotNil(t, fresh.Status.CompletedAt)
}

// TestWorkflowRunReconciler_UnresolvableDriver_SetsFailed confirms a
// WorkflowRun against a real ClusterDefinition whose driver module can't be
// resolved (no lock entry, no reachable source) surfaces as Failed with the
// underlying error — RunAdHocWorkflow's own first real step
// (module.Resolve) fails deterministically here since ModulesDirPath points
// at a nonexistent directory and the source isn't a valid local/remote ref.
func TestWorkflowRunReconciler_UnresolvableDriver_SetsFailed(t *testing.T) {
	cluster := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "target-cluster", Namespace: testNamespace},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Driver: hyvev1alpha1.DriverRef{Source: "./nonexistent-module", Version: "latest"},
		},
	}
	wr := &hyvev1alpha1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-3", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowRunSpec{WorkflowRef: hyvev1alpha1.WorkflowRef{Name: "install-podinfo"}, ClusterRef: "target-cluster"},
	}
	r := newWorkflowRunReconciler(t, cluster, wr)

	_, err := r.Reconcile(context.Background(), ctrlRequest("run-3"))
	require.NoError(t, err)

	var fresh hyvev1alpha1.WorkflowRun
	require.NoError(t, r.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: testNamespace, Name: "run-3"}, &fresh))
	assert.Equal(t, hyvev1alpha1.WorkflowRunPhaseFailed, fresh.Status.Phase)
	assert.NotEmpty(t, fresh.Status.Message)
	assert.NotNil(t, fresh.Status.StartedAt)
	assert.NotNil(t, fresh.Status.CompletedAt)
}
