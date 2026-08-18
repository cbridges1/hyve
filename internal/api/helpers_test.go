package api

import (
	"context"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// contextWithUsername/contextWithRole let a test exercise requireRole or
// RequireRole in isolation, without needing a real token round-trip
// through requireAuth first — same context keys those middleware layers
// use in production, just set directly.
func contextWithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, contextKeyUsername, username)
}

func contextWithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, contextKeyRole, role)
}

const testNamespace = "hyve-system"

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, hyvev1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	builder := clientfake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return builder.Build()
}
