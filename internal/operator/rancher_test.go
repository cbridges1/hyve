package operator

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/templates"
)

func imported(name, capiNS, capiName string) *unstructured.Unstructured {
	rc := object(rancherClusterGVK)
	rc.SetName(name)
	rc.SetLabels(map[string]string{turtlesOwnerLabel: capiName, turtlesOwnerNSLabel: capiNS})
	return rc
}

func TestCreatorBecomesClusterOwner(t *testing.T) {
	c := capiCluster("prod", "web", map[string]string{AnnotationCreator: "u-abc12"}, nil, nil)
	cl := fake.NewClientBuilder().WithObjects(c).Build()
	r := &Rancher{Client: cl}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "web"}}
	res, err := r.ReconcileCreator(context.Background(), req)
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("before import: res=%v err=%v (want a requeue)", res, err)
	}
	if err := cl.Create(context.Background(), imported("c-x1", "prod", "web")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReconcileCreator(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(crtbGVK.GroupVersion().WithKind("ClusterRoleTemplateBindingList"))
	if err := cl.List(context.Background(), list, client.InNamespace("c-x1")); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Object["userName"] != "u-abc12" || list.Items[0].Object["roleTemplateName"] != "cluster-owner" {
		t.Errorf("CRTBs = %v", list.Items)
	}
}

func TestCRTBReachesTheCAPICluster(t *testing.T) {
	crtb := object(crtbGVK)
	crtb.SetNamespace("c-x1")
	crtb.SetName("b1")
	crtb.Object["roleTemplateName"] = "cluster-member"
	crtb.Object["userName"] = "u-bob"
	cr := object(clusterResourceGVK)
	cr.SetNamespace("prod")
	cr.SetName("web-podinfo")
	cr.SetLabels(map[string]string{"hyve.io/cluster": "web"})
	cl := fake.NewClientBuilder().WithObjects(crtb, imported("c-x1", "prod", "web"), cr).Build()
	r := &Rancher{Client: cl}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "c-x1", Name: "b1"}}
	if _, err := r.ReconcileCRTB(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	roles := &rbacv1.RoleList{}
	_ = cl.List(context.Background(), roles, client.InNamespace("prod"))
	if len(roles.Items) != 1 {
		t.Fatalf("roles = %v", roles.Items)
	}
	rules := roles.Items[0].Rules
	if rules[0].ResourceNames[0] != "web" || len(rules[0].Verbs) != 2 || rules[1].ResourceNames[0] != "web-podinfo" {
		t.Errorf("rules = %+v", rules)
	}
	rbs := &rbacv1.RoleBindingList{}
	_ = cl.List(context.Background(), rbs, client.InNamespace("prod"))
	if len(rbs.Items) != 1 || rbs.Items[0].Subjects[0].Name != "u-bob" {
		t.Errorf("bindings = %+v", rbs.Items)
	}
	// The CRTB goes: so do the Role and its binding.
	if err := cl.Delete(context.Background(), crtb); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReconcileCRTB(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = cl.List(context.Background(), roles, client.InNamespace("prod"))
	_ = cl.List(context.Background(), rbs, client.InNamespace("prod"))
	if len(roles.Items) != 0 || len(rbs.Items) != 0 {
		t.Errorf("left behind: %d roles, %d bindings", len(roles.Items), len(rbs.Items))
	}
}

func TestEnvironmentGetsWorkspaceAndProject(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod", Labels: map[string]string{"hyve.io/environment": "true"}}}
	cl := fake.NewClientBuilder().WithObjects(ns).Build()
	r := &Rancher{Client: cl}
	if _, err := r.ReconcileNamespace(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod"}}); err != nil {
		t.Fatal(err)
	}
	fw := object(fleetWorkspaceGVK)
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "prod"}, fw); err != nil {
		t.Errorf("FleetWorkspace: %v", err)
	}
	got := &corev1.Namespace{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: "prod"}, got)
	p := got.Annotations[annotProject]
	if p == "" || got.Annotations["field.cattle.io/projectId"] != "local:"+p {
		t.Errorf("namespace annotations = %v", got.Annotations)
	}
}

func TestRecordCreatorTakesRancherUsers(t *testing.T) {
	if !IsRancherUser("u-abc12") || !IsRancherUser("user-x7z") || IsRancherUser("system:admin") || IsRancherUser("u-") {
		t.Error("IsRancherUser")
	}
	_ = templates.AnnotationExpiresAt
}
