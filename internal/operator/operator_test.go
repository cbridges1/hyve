package operator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	jsonpatch5 "github.com/evanphx/json-patch/v5"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

var testNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func capiCluster(ns, name string, ann, labels map[string]string, vars map[string]any) *unstructured.Unstructured {
	c := clusters.New(name, ns)
	c.SetGroupVersionKind(clusterGVK)
	c.SetAnnotations(ann)
	c.SetLabels(labels)
	_ = clusters.SetVariables(c, vars)
	return c
}

func templateObj() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hyve.io/v1alpha1", "kind": "ClusterTemplate",
		"metadata": map[string]any{"name": "civo", "namespace": "capi-clusters", "uid": "tpl-uid"},
		"spec": map[string]any{
			"clusterClassRef":   map[string]any{"name": "civo"},
			"kubernetesVersion": "v1.35.0",
			"variables":         map[string]any{"region": "LON1", "disableTraefik": true},
			"lockedVariables":   []any{"disableTraefik"},
			"resources": []any{
				map[string]any{"name": "traefik", "namespace": "traefik", "helmOp": map[string]any{"helm": map[string]any{"repo": "https://traefik.github.io/charts", "chart": "traefik", "version": "37.1.0"}}},
				map[string]any{"name": "dns", "ref": "external-dns", "secrets": []any{"cloudflare-dns"}},
			},
		},
	}}
}

func classObj() *unstructured.Unstructured {
	c := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta2", "kind": "ClusterClass",
		"metadata": map[string]any{"name": "civo", "namespace": "capi-clusters"},
		"spec":     map[string]any{"workers": map[string]any{"machineDeployments": []any{map[string]any{"class": "default-worker"}}}},
	}}
	return c
}

func TestExpiredClusterIsDeleted(t *testing.T) {
	cl := fake.NewClientBuilder().WithObjects(capiCluster("ci", "old", map[string]string{templates.AnnotationExpiresAt: "2026-10-10T11:00:00Z"}, nil, nil)).Build()
	r := &Expiry{Client: cl, Now: func() time.Time { return testNow }}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ci", Name: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ci", Name: "old"}, object(clusterGVK)); err == nil {
		t.Error("expired cluster still exists")
	}
}

func TestLiveAndNeverExpiringClusters(t *testing.T) {
	cl := fake.NewClientBuilder().WithObjects(
		capiCluster("ci", "new", map[string]string{templates.AnnotationExpiresAt: "2026-10-10T12:20:00Z"}, nil, nil),
		capiCluster("ci", "kept", map[string]string{templates.AnnotationExpiresAt: templates.Never}, nil, nil),
	).Build()
	r := &Expiry{Client: cl, Now: func() time.Time { return testNow }}
	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ci", Name: "new"}})
	if err != nil || res.RequeueAfter != 20*time.Minute {
		t.Errorf("RequeueAfter = %s, %v", res.RequeueAfter, err)
	}
	res, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ci", Name: "kept"}})
	if err != nil || res.RequeueAfter != 0 {
		t.Errorf("never: RequeueAfter = %s, %v", res.RequeueAfter, err)
	}
}

func review(h admission.Handler, op admissionv1.Operation, old, new *unstructured.Unstructured) admission.Response {
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: op, Namespace: new.GetNamespace()}}
	req.Object.Raw, _ = new.MarshalJSON()
	if old != nil {
		req.OldObject.Raw, _ = old.MarshalJSON()
	}
	return h.Handle(context.Background(), req)
}

func TestFillWebhookCompletesALabelledCluster(t *testing.T) {
	h := &FillCluster{Client: fake.NewClientBuilder().WithObjects(templateObj(), classObj()).Build(), Now: func() time.Time { return testNow }}
	c := capiCluster("prod", "web", nil, map[string]string{templates.LabelTemplate: "civo", templates.LabelTemplateNamespace: "capi-clusters"}, map[string]any{"region": "NYC1"})
	resp := review(h, admissionv1.Create, nil, c)
	if !resp.Allowed || len(resp.Patches) == 0 {
		t.Fatalf("resp = %+v", resp.Result)
	}
	raw, _ := c.MarshalJSON()
	patch, _ := json.Marshal(resp.Patches)
	filled := applyPatch(t, raw, patch)
	v, _, _ := unstructured.NestedString(filled.Object, "spec", "topology", "version")
	ns, _, _ := unstructured.NestedString(filled.Object, "spec", "topology", "classRef", "namespace")
	vars := clusters.Variables(filled)
	if v != "v1.35.0" || ns != "capi-clusters" || vars["region"] != "NYC1" || vars["disableTraefik"] != true {
		t.Errorf("filled = %v", filled.Object["spec"])
	}

	locked := capiCluster("prod", "bad", nil, map[string]string{templates.LabelTemplate: "civo", templates.LabelTemplateNamespace: "capi-clusters"}, map[string]any{"disableTraefik": false})
	if review(h, admissionv1.Create, nil, locked).Allowed {
		t.Error("changing a locked variable at create must be denied")
	}
	missing := capiCluster("prod", "x", nil, map[string]string{templates.LabelTemplate: "nope"}, nil)
	if review(h, admissionv1.Create, nil, missing).Allowed {
		t.Error("a missing template must be denied")
	}
	plain := capiCluster("prod", "plain", nil, nil, nil)
	if r := review(h, admissionv1.Create, nil, plain); !r.Allowed || len(r.Patches) != 0 {
		t.Error("a cluster without a template is left alone")
	}
}

func applyPatch(t *testing.T, doc, patch []byte) *unstructured.Unstructured {
	t.Helper()
	p, err := jsonpatch5.DecodePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Apply(doc)
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(out); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLockedWebhookOnlyRefusesChanges(t *testing.T) {
	h := &LockedVariables{Client: fake.NewClientBuilder().WithObjects(templateObj()).Build()}
	l := map[string]string{templates.LabelTemplate: "civo", templates.LabelTemplateNamespace: "capi-clusters"}
	old := capiCluster("prod", "c", nil, l, nil)
	if !review(h, admissionv1.Update, old, capiCluster("prod", "c", nil, l, map[string]any{"region": "X"})).Allowed {
		t.Error("a cluster made before the lock may still be updated")
	}
	old = capiCluster("prod", "c", nil, l, map[string]any{"disableTraefik": true})
	if review(h, admissionv1.Update, old, capiCluster("prod", "c", nil, l, map[string]any{"disableTraefik": false})).Allowed {
		t.Error("changing a locked variable must be denied")
	}
}

func TestTemplateResourcesMigrateToDeployments(t *testing.T) {
	shared := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hyve.io/v1alpha1", "kind": "Resource",
		"metadata": map[string]any{"name": "external-dns", "namespace": "capi-clusters"},
		"spec":     map[string]any{"namespace": "external-dns", "helmOp": map[string]any{"helm": map[string]any{"chart": "external-dns", "repo": "https://x"}}},
	}}
	tpl := templateObj()
	cl := fake.NewClientBuilder().WithObjects(tpl, shared).WithStatusSubresource(tpl).Build()
	m := &Migrate{Client: cl}
	if _, err := m.Template(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "capi-clusters", Name: "civo"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tpl-civo-traefik", "tpl-civo-dns"} {
		h := object(resources.HelmOpGVR.GroupVersion().WithKind("HelmOp"))
		if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "capi-clusters", Name: name}, h); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		targets, _, _ := unstructured.NestedSlice(h.Object, "spec", "targets")
		if len(targets) != 1 || targets[0].(map[string]any)["clusterGroup"] != "template-civo" {
			t.Errorf("%s targets = %v", name, targets)
		}
		if len(h.GetOwnerReferences()) != 0 || h.GetLabels()[resources.LabelOwner] != "" {
			t.Errorf("%s is still hyve's: %v %v", name, h.GetOwnerReferences(), h.GetLabels())
		}
		if name == "tpl-civo-dns" {
			ds, _, _ := unstructured.NestedSlice(h.Object, "spec", "downstreamResources")
			ns, _, _ := unstructured.NestedString(h.Object, "spec", "defaultNamespace")
			if len(ds) != 1 || ns != "external-dns" {
				t.Errorf("dns HelmOp spec = %v", h.Object["spec"])
			}
		}
	}
	got := object(templateGVK)
	_ = cl.Get(context.Background(), client.ObjectKey{Namespace: "capi-clusters", Name: "civo"}, got)
	if rs, found, _ := unstructured.NestedSlice(got.Object, "spec", "resources"); found && len(rs) > 0 {
		t.Errorf("the template still lists resources: %v", rs)
	}
}

func TestClusterResourceMigratesToADeployment(t *testing.T) {
	cr := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hyve.io/v1alpha1", "kind": "ClusterResource",
		"metadata": map[string]any{"name": "web-hello", "namespace": "dev", "labels": map[string]any{resources.LabelResource: "hello"}},
		"spec":     map[string]any{"clusterName": "web", "helmOp": map[string]any{"helm": map[string]any{"chart": "hello", "repo": "https://x"}}},
	}}
	cl := fake.NewClientBuilder().WithObjects(cr).Build()
	m := &Migrate{Client: cl}
	if _, err := m.ClusterResource(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "dev", Name: "web-hello"}}); err != nil {
		t.Fatal(err)
	}
	h := object(resources.HelmOpGVR.GroupVersion().WithKind("HelmOp"))
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "dev", Name: "web-hello"}, h); err != nil {
		t.Fatal(err)
	}
	if rn, _, _ := unstructured.NestedString(h.Object, "spec", "helm", "releaseName"); rn != "hello" {
		t.Errorf("release name = %q", rn)
	}
	targets, _, _ := unstructured.NestedSlice(h.Object, "spec", "targets")
	if len(targets) != 1 || targets[0].(map[string]any)["clusterName"] != "web" {
		t.Errorf("targets = %v", targets)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "dev", Name: "web-hello"}, object(clusterResourceGVK)); err == nil {
		t.Error("the ClusterResource is still there")
	}
}

func TestGroupsForTemplatesAndEnvironments(t *testing.T) {
	env := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod", Labels: map[string]string{"hyve.io/environment": ""}}}
	stale := group("capi-clusters", "template-gone", delivery.GroupKindTemplate, map[string]any{"x": "y"})
	gold := object(clusterGroupGVK)
	gold.SetNamespace("capi-clusters")
	gold.SetName("gold")
	gold.Object["spec"] = map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "gold"}}}
	cl := fake.NewClientBuilder().WithObjects(templateObj(), env, stale, gold).Build()
	g := &Groups{Client: cl, Workspace: "capi-clusters"}
	if _, err := g.Reconcile(context.Background(), groupsKey); err != nil {
		t.Fatal(err)
	}
	// Fleet looks groups up in each cluster's namespace: they're in the
	// environment too, with copies of the template namespace's own.
	for _, ns := range []string{"capi-clusters", "prod"} {
		for name, label := range map[string]string{"template-civo": templates.LabelTemplate, "env-prod": delivery.LabelEnvironment, "gold": "tier"} {
			if ns == "capi-clusters" && name == "gold" {
				continue
			}
			u := object(clusterGroupGVK)
			if err := cl.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, u); err != nil {
				t.Fatalf("%s/%s: %v", ns, name, err)
			}
			if ml, _, _ := unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels"); ml[label] == "" {
				t.Errorf("%s/%s selector = %v", ns, name, ml)
			}
		}
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "capi-clusters", Name: "template-gone"}, object(clusterGroupGVK)); err == nil {
		t.Error("a group for a deleted template is still there")
	}

	c := capiCluster("prod", "web", nil, nil, nil)
	cl = fake.NewClientBuilder().WithObjects(c).Build()
	if _, err := (&ClusterLabels{Client: cl}).Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "prod", Name: "web"}}); err != nil {
		t.Fatal(err)
	}
	got := object(clusterGVK)
	_ = cl.Get(context.Background(), client.ObjectKey{Namespace: "prod", Name: "web"}, got)
	if l := got.GetLabels(); l[delivery.LabelEnvironment] != "prod" || l[delivery.LabelCluster] != "web" {
		t.Errorf("labels = %v", l)
	}
}
