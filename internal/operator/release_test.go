package operator

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/tunnel"
)

func TestReleaseAgents(t *testing.T) {
	agent := func(manager string) *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: "cattle-cluster-agent", Namespace: "cattle-system",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: manager, Operation: metav1.ManagedFieldsOperationApply}},
		}}
	}
	for _, tc := range []struct {
		name    string
		manager string
		removed bool
	}{
		{"hyve's agent goes", kube.FieldManager, true},
		{"Rancher's stays", "rancher", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			c := capiCluster("dev", "s1", nil, nil, nil)
			c.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "ControlPlaneAvailable", "status": "True"}}}
			reg := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "hyve-system", Name: tunnel.SecretName(tunnel.ClusterID("dev", "s1"))}}
			kc := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "s1-kubeconfig"}, Data: map[string][]byte{"value": []byte("x")}}
			cl := fake.NewClientBuilder().WithObjects(c, reg, kc).Build()
			child := k8sfake.NewClientset(agent(tc.manager))
			r := &ReleaseAgents{Client: cl, Namespace: "hyve-system", NewChild: func([]byte) (kubernetes.Interface, error) { return child, nil }}
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "dev", Name: "s1"}}); err != nil {
				t.Fatal(err)
			}
			_, err := child.AppsV1().Deployments("cattle-system").Get(ctx, "cattle-cluster-agent", metav1.GetOptions{})
			if gone := err != nil; gone != tc.removed {
				t.Errorf("agent removed = %v, want %v", gone, tc.removed)
			}
			if err := cl.Get(ctx, types.NamespacedName{Namespace: "hyve-system", Name: reg.Name}, &corev1.Secret{}); err == nil {
				t.Error("the agent Secret should be forgotten")
			}
		})
	}
}
