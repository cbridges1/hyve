package operator

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFleetCopyDownstreamTurnsItOn(t *testing.T) {
	ctx := context.Background()
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "cattle-fleet-system", Name: "fleet-controller"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "fleet-controller", Env: []corev1.EnvVar{{Name: "OTHER", Value: "1"}}},
			{Name: "fleet-agentmanagement", Env: []corev1.EnvVar{{Name: CopyDownstreamEnv, Value: "true"}}},
		}}}},
	}
	cl := fake.NewClientBuilder().WithObjects(d).Build()
	f := &FleetCopyDownstream{Reader: cl, Client: cl}
	if err := f.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	var got appsv1.Deployment
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "cattle-fleet-system", Name: "fleet-controller"}, &got); err != nil {
		t.Fatal(err)
	}
	for _, c := range got.Spec.Template.Spec.Containers {
		on, other := 0, false
		for _, e := range c.Env {
			if e.Name == CopyDownstreamEnv && e.Value == "true" {
				on++
			}
			other = other || e.Name == "OTHER"
		}
		if on != 1 {
			t.Errorf("%s: %s set %d times: %v", c.Name, CopyDownstreamEnv, on, c.Env)
		}
		if c.Name == "fleet-controller" && !other {
			t.Errorf("fleet-controller lost its other env: %v", c.Env)
		}
	}
	// No Fleet at all is fine.
	if err := (&FleetCopyDownstream{Reader: fake.NewClientBuilder().Build(), Client: cl}).Ensure(ctx); err != nil {
		t.Error(err)
	}
}
