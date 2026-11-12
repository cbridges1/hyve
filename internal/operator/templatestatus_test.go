package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Fleet waits on a template's observedGeneration: it's kept up, and rc.10's
// resources rows go.
func TestTemplateStatus(t *testing.T) {
	tpl := templateObj()
	tpl.SetGeneration(3)
	tpl.Object["status"] = map[string]any{"observedGeneration": int64(1), "resources": []any{map[string]any{"name": "old"}}}
	cl := fake.NewClientBuilder().WithObjects(tpl).WithStatusSubresource(tpl).Build()
	r := &TemplateStatus{Client: cl}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "capi-clusters", Name: "civo"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	got := object(templateGVK)
	require.NoError(t, cl.Get(context.Background(), req.NamespacedName, got))
	gen, _, _ := unstructured.NestedInt64(got.Object, "status", "observedGeneration")
	assert.Equal(t, got.GetGeneration(), gen)
	_, rows, _ := unstructured.NestedFieldNoCopy(got.Object, "status", "resources")
	assert.False(t, rows)
	conds, _, _ := unstructured.NestedSlice(got.Object, "status", "conditions")
	require.Len(t, conds, 1)
	c := conds[0].(map[string]any)
	assert.Equal(t, "Ready", c["type"])
	assert.Equal(t, "True", c["status"])

	// Nothing to do the second time.
	rv := got.GetResourceVersion()
	_, err = r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.NoError(t, cl.Get(context.Background(), req.NamespacedName, got))
	assert.Equal(t, rv, got.GetResourceVersion())
}
