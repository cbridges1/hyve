package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/clusters"
)

const importedKubeconfig = `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://k8s.example.com:6551"}}]
users: [{name: u, user: {token: t}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`

func TestImportedControlPlane(t *testing.T) {
	ctx := context.Background()
	cp := object(importedControlPlaneGVK)
	cp.SetName("legacy")
	cp.SetNamespace("prod")
	cp.SetLabels(map[string]string{"cluster.x-k8s.io/cluster-name": "legacy"})
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "legacy-kubeconfig", Namespace: "prod"}, Data: map[string][]byte{"value": []byte(importedKubeconfig)}}
	c := clusters.NewImported("legacy", "prod")
	c.SetLabels(map[string]string{"hyve.io/template": "base", "hyve.io/template-namespace": "capi-clusters"})
	cl := fake.NewClientBuilder().WithObjects(cp, sec, c).WithStatusSubresource(cp).Build()
	var probeErr error
	detached := 0
	r := &ImportedControlPlane{Client: cl, Now: func() time.Time { return testNow },
		Probe:  func(context.Context, []byte) (string, error) { return "v1.31.5+k3s1", probeErr },
		Detach: func(context.Context, []byte) error { detached++; return nil }}
	key := types.NamespacedName{Namespace: "prod", Name: "legacy"}
	get := func() *unstructured.Unstructured {
		o := object(importedControlPlaneGVK)
		if err := cl.Get(ctx, key, o); err != nil {
			t.Fatal(err)
		}
		return o
	}

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	o := get()
	host, _, _ := unstructured.NestedString(o.Object, "spec", "controlPlaneEndpoint", "host")
	port, _, _ := unstructured.NestedInt64(o.Object, "spec", "controlPlaneEndpoint", "port")
	init, _, _ := unstructured.NestedBool(o.Object, "status", "initialization", "controlPlaneInitialized")
	version, _, _ := unstructured.NestedString(o.Object, "status", "version")
	if host != "k8s.example.com" || port != 6551 || !init || version != "v1.31.5+k3s1" || clusters.ConditionStatus(o, "Available") != "True" {
		t.Fatalf("after a reachable pass: %v", o.Object)
	}

	// Its template's bundles, in capi-clusters, reach prod.
	m := object(bundleNamespaceMappingGVK)
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "capi-clusters", Name: "prod"}, m); err != nil {
		t.Fatalf("no BundleNamespaceMapping: %v", err)
	}

	// Unreachable: not available, but still initialized.
	probeErr = errors.New("connection refused")
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	o = get()
	init, _, _ = unstructured.NestedBool(o.Object, "status", "initialization", "controlPlaneInitialized")
	if !init || clusters.ConditionStatus(o, "Available") != "False" {
		t.Fatalf("after an unreachable pass: %v", o.Object["status"])
	}

	// Deleting it detaches the cluster, once, and lets it go.
	if err := cl.Delete(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if detached != 1 {
		t.Errorf("detached %d times", detached)
	}
	if err := cl.Get(ctx, key, object(importedControlPlaneGVK)); err == nil {
		t.Error("finalizer not removed")
	}
}
