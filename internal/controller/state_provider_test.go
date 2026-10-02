package controller

import (
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "hyve-system"

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, hyvev1alpha1.AddToScheme(scheme))
	return scheme
}

func newFakeProvider(t *testing.T, objs ...client.Object) *CRDStateProvider {
	t.Helper()
	builder := clientfake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{}, &hyvev1alpha1.HyveConfig{})
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	return &CRDStateProvider{
		Client:         builder.Build(),
		Namespace:      testNamespace,
		ConfigName:     "hyve-config",
		ModulesDirPath: "/var/lib/hyve/modules",
	}
}

func TestCRDStateProvider_LocalPath(t *testing.T) {
	p := newFakeProvider(t)
	assert.Equal(t, "/var/lib/hyve/modules", p.LocalPath())
}

func TestCRDStateProvider_LoadRepoConfig(t *testing.T) {
	t.Run("missing HyveConfig returns a default, not an error", func(t *testing.T) {
		p := newFakeProvider(t)
		cfg, err := p.LoadRepoConfig()
		require.NoError(t, err)
		assert.False(t, cfg.Reconcile.StrictResourceDelete)
	})

	t.Run("present HyveConfig is read through", func(t *testing.T) {
		cfg := &hyvev1alpha1.HyveConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "hyve-config", Namespace: testNamespace},
			Spec:       hyvev1alpha1.HyveConfigSpec{StrictResourceDelete: true, DefaultWorkflowImage: "alpine/k8s:1.29"},
		}
		p := newFakeProvider(t, cfg)
		repoCfg, err := p.LoadRepoConfig()
		require.NoError(t, err)
		assert.True(t, repoCfg.Reconcile.StrictResourceDelete)

		img, err := p.DefaultWorkflowImage(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "alpine/k8s:1.29", img)
	})
}

func TestCRDStateProvider_LoadClusterDefinitions(t *testing.T) {
	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Region: "nyc1",
			Driver: hyvev1alpha1.DriverRef{Source: "./module", Version: "latest"},
			Params: map[string]string{"node_count": "3"},
		},
		Status: hyvev1alpha1.ClusterDefinitionStatus{
			DriverOutputs: map[string]string{"HYVE_CLUSTER_ID": "abc123"},
		},
	}
	p := newFakeProvider(t, cr)

	defs, err := p.LoadClusterDefinitions()
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, "demo", defs[0].Metadata.Name)
	assert.Equal(t, "nyc1", defs[0].Metadata.Region)
	assert.Equal(t, "3", defs[0].Spec.Params["node_count"])
	assert.Equal(t, "abc123", defs[0].Spec.DriverOutputs["HYVE_CLUSTER_ID"])
}

func TestCRDStateProvider_LoadClusterDefinitions_DeletionTimestampBecomesSpecDelete(t *testing.T) {
	now := metav1.Now()
	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo", Namespace: testNamespace,
			Finalizers:        []string{hyvev1alpha1.ClusterDefinitionFinalizer},
			DeletionTimestamp: &now,
		},
	}
	p := newFakeProvider(t, cr)

	defs, err := p.LoadClusterDefinitions()
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.True(t, defs[0].Spec.Delete, "a real kubectl delete (deletionTimestamp set) must convert to Spec.Delete=true, same as file mode's spec.delete:true")
}

func TestCRDStateProvider_SaveClusterDefinition(t *testing.T) {
	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace},
		Spec: hyvev1alpha1.ClusterDefinitionSpec{
			Region: "nyc1",
			// A spec-level field a reconcile cycle might mutate in file
			// mode (resources.go pruning) — must NOT be touched by Save.
			Resources: []hyvev1alpha1.ResourceRef{{Name: "keep-me", Source: "./x.yaml"}},
		},
	}
	p := newFakeProvider(t, cr)

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "demo"},
		Spec: types.ClusterSpec{
			DriverOutputs: map[string]string{"HYVE_LAST_PARAMS_HASH": "deadbeef"},
			AppliedResources: map[string]*types.AppliedResource{
				"web": {SourceSHA256: "abc", AppliedAt: "2026-01-01T00:00:00Z"},
			},
		},
	}
	require.NoError(t, p.SaveClusterDefinition(def))

	var updated hyvev1alpha1.ClusterDefinition
	require.NoError(t, p.Client.Get(t.Context(), k8stypes.NamespacedName{Namespace: testNamespace, Name: "demo"}, &updated))
	assert.Equal(t, "deadbeef", updated.Status.DriverOutputs["HYVE_LAST_PARAMS_HASH"])
	require.Contains(t, updated.Status.AppliedResources, "web")
	assert.Equal(t, "abc", updated.Status.AppliedResources["web"].SourceSHA256)

	// Spec must be untouched — SaveClusterDefinition only ever writes status.
	require.Len(t, updated.Spec.Resources, 1)
	assert.Equal(t, "keep-me", updated.Spec.Resources[0].Name)
}

func TestCRDStateProvider_RemoveClusterFile(t *testing.T) {
	t.Run("deletes an existing object", func(t *testing.T) {
		cr := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace}}
		p := newFakeProvider(t, cr)
		require.NoError(t, p.RemoveClusterFile("demo"))

		var check hyvev1alpha1.ClusterDefinition
		err := p.Client.Get(t.Context(), k8stypes.NamespacedName{Namespace: testNamespace, Name: "demo"}, &check)
		assert.Error(t, err, "object should be gone")
	})

	t.Run("missing object is not an error — idempotent delete", func(t *testing.T) {
		p := newFakeProvider(t)
		assert.NoError(t, p.RemoveClusterFile("does-not-exist"))
	})
}

func TestCRDStateProvider_HasStateSidecar(t *testing.T) {
	t.Run("no status populated is false", func(t *testing.T) {
		cr := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace}}
		p := newFakeProvider(t, cr)
		assert.False(t, p.HasStateSidecar("demo"))
	})

	t.Run("driverOutputs populated is true", func(t *testing.T) {
		cr := &hyvev1alpha1.ClusterDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace},
			Status:     hyvev1alpha1.ClusterDefinitionStatus{DriverOutputs: map[string]string{"HYVE_CLUSTER_ID": "x"}},
		}
		p := newFakeProvider(t, cr)
		assert.True(t, p.HasStateSidecar("demo"))
	})

	t.Run("nonexistent object is false, not an error", func(t *testing.T) {
		p := newFakeProvider(t)
		assert.False(t, p.HasStateSidecar("does-not-exist"))
	})
}
