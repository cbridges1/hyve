package controller

import (
	"context"
	"errors"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/resourceref"
	"github.com/cbridges1/hyve/internal/workflowref"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newRefStatusReconciler(t *testing.T) *ClusterDefinitionReconciler {
	t.Helper()
	c := clientfake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithStatusSubresource(&hyvev1alpha1.WorkflowRefStatus{}, &hyvev1alpha1.ResourceRefStatus{}).
		Build()
	return &ClusterDefinitionReconciler{Client: c, Namespace: testNamespace}
}

func TestUpsertWorkflowRefStatusCR_CreateThenUpdate(t *testing.T) {
	r := newRefStatusReconciler(t)
	ctx := context.Background()

	res := workflowref.RefResult{
		Name: "hello-world", CanonicalSource: "github.com/org/repo//workflows/hello.yaml",
		RawVersion: "", ResolvedVersion: "main", SHA256: "abc123",
	}
	r.upsertWorkflowRefStatusCR(ctx, res)

	// metadata.name is a derived slug (module.CRName) — don't hardcode it,
	// just confirm exactly one WorkflowRefStatus exists and matches.
	var list hyvev1alpha1.WorkflowRefStatusList
	require.NoError(t, r.Client.List(ctx, &list))
	require.Len(t, list.Items, 1)
	got := list.Items[0]
	assert.Equal(t, "hello-world", got.Spec.Name)
	assert.Equal(t, "github.com/org/repo//workflows/hello.yaml", got.Spec.Source)
	assert.True(t, got.Status.Resolved)
	assert.Equal(t, "abc123", got.Status.SHA256)
	assert.Equal(t, "main", got.Status.ResolvedVersion)
	assert.Empty(t, got.Status.Error)

	// Update-in-place: same (Name, CanonicalSource) pair, new SHA256 —
	// must be one upsert, not a second object.
	res.SHA256 = "def456"
	res.ResolvedVersion = "v1.0.0"
	r.upsertWorkflowRefStatusCR(ctx, res)

	var list2 hyvev1alpha1.WorkflowRefStatusList
	require.NoError(t, r.Client.List(ctx, &list2))
	require.Len(t, list2.Items, 1, "must update the existing CR in place, not create a second one")
	assert.Equal(t, "def456", list2.Items[0].Status.SHA256)
	assert.Equal(t, "v1.0.0", list2.Items[0].Status.ResolvedVersion)
}

// TestUpsertWorkflowRefStatusCR_UnchangedStatusSkipsWrite confirms the
// steady-state case (called every reconcile, identical result) doesn't
// touch the API server on the second call — resolvedAt must not bump, and
// no error should come from a would-be no-op Update.
func TestUpsertWorkflowRefStatusCR_UnchangedStatusSkipsWrite(t *testing.T) {
	r := newRefStatusReconciler(t)
	ctx := context.Background()

	res := workflowref.RefResult{
		Name: "hello-world", CanonicalSource: "github.com/org/repo//workflows/hello.yaml", SHA256: "abc123",
	}
	r.upsertWorkflowRefStatusCR(ctx, res)

	var before hyvev1alpha1.WorkflowRefStatusList
	require.NoError(t, r.Client.List(ctx, &before))
	require.Len(t, before.Items, 1)
	firstResolvedAt := before.Items[0].Status.ResolvedAt

	r.upsertWorkflowRefStatusCR(ctx, res) // identical result — must be a no-op

	var after hyvev1alpha1.WorkflowRefStatusList
	require.NoError(t, r.Client.List(ctx, &after))
	require.Len(t, after.Items, 1)
	assert.Equal(t, firstResolvedAt, after.Items[0].Status.ResolvedAt, "unchanged status must not bump ResolvedAt or write at all")
}

func TestUpsertWorkflowRefStatusCR_ErrorResult(t *testing.T) {
	r := newRefStatusReconciler(t)
	ctx := context.Background()

	res := workflowref.RefResult{
		Name: "broken", CanonicalSource: "github.com/org/repo//workflows/broken.yaml",
		Err: errors.New("file not found"),
	}
	r.upsertWorkflowRefStatusCR(ctx, res)

	var list hyvev1alpha1.WorkflowRefStatusList
	require.NoError(t, r.Client.List(ctx, &list))
	require.Len(t, list.Items, 1)
	assert.False(t, list.Items[0].Status.Resolved)
	assert.Equal(t, "file not found", list.Items[0].Status.Error)
	assert.Empty(t, list.Items[0].Status.SHA256)
}

func TestUpsertResourceRefStatusCR_CreateThenUpdate(t *testing.T) {
	r := newRefStatusReconciler(t)
	ctx := context.Background()

	res := resourceref.RefResult{
		Name: "podinfo", CanonicalSource: "github.com/org/repo//resources/podinfo.yaml", SHA256: "abc123",
	}
	r.upsertResourceRefStatusCR(ctx, res)

	var list hyvev1alpha1.ResourceRefStatusList
	require.NoError(t, r.Client.List(ctx, &list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "podinfo", list.Items[0].Spec.Name)
	assert.True(t, list.Items[0].Status.Resolved)
	assert.Equal(t, "abc123", list.Items[0].Status.SHA256)

	res.SHA256 = "def456"
	r.upsertResourceRefStatusCR(ctx, res)

	var list2 hyvev1alpha1.ResourceRefStatusList
	require.NoError(t, r.Client.List(ctx, &list2))
	require.Len(t, list2.Items, 1, "must update the existing CR in place, not create a second one")
	assert.Equal(t, "def456", list2.Items[0].Status.SHA256)
}

func TestUpsertResourceRefStatusCR_ErrorResult(t *testing.T) {
	r := newRefStatusReconciler(t)
	ctx := context.Background()

	res := resourceref.RefResult{
		Name: "broken", CanonicalSource: "github.com/org/repo//resources/broken.yaml",
		Err: errors.New("resource file not found"),
	}
	r.upsertResourceRefStatusCR(ctx, res)

	var list hyvev1alpha1.ResourceRefStatusList
	require.NoError(t, r.Client.List(ctx, &list))
	require.Len(t, list.Items, 1)
	assert.False(t, list.Items[0].Status.Resolved)
	assert.Equal(t, "resource file not found", list.Items[0].Status.Error)
}
