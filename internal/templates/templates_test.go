package templates

import (
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
)

func tmpl() *Template {
	three := int64(3)
	return &Template{Name: "gke-small", Namespace: "capi-clusters", Spec: Spec{
		ClusterClassRef: ClassRef{Name: "gke"},
		Version:         "v1.35.8",
		Workers:         &Workers{Type: "machinePool", Count: &three},
		Variables:       map[string]any{"machineType": "e2-small", "project": "p1", "noDefault": nil},
		LockedVariables: []string{"project"},
		Schedule:        "0 17 * * *",
		Labels:          map[string]string{"hyve.io/team": "web"},
	}}
}

func class(machineCP bool) *unstructured.Unstructured {
	spec := map[string]any{
		"workers": map[string]any{
			"machinePools": []any{map[string]any{"class": "default-pool"}},
		},
		"controlPlane": map[string]any{},
	}
	if machineCP {
		spec["controlPlane"] = map[string]any{"machineInfrastructure": map[string]any{}}
	}
	c := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	c.SetName("gke")
	c.SetAnnotations(map[string]string{"herd.io/cluster-labels": "cni=flannel", clusters.AnnotationDefaultVersion: "v1.34.0"})
	return c
}

var now = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func TestFillFromTemplateInAnotherNamespace(t *testing.T) {
	c := clusters.New("web", "prod")
	_ = clusters.SetVariables(c, map[string]any{"machineType": "e2-medium"})
	Label(c, tmpl())
	if err := Fill(c, tmpl(), class(false), now); err != nil {
		t.Fatal(err)
	}
	get := func(f ...string) any { v, _, _ := unstructured.NestedFieldNoCopy(c.Object, f...); return v }
	if get("spec", "topology", "classRef", "name") != "gke" || get("spec", "topology", "classRef", "namespace") != "capi-clusters" {
		t.Errorf("classRef = %v", get("spec", "topology", "classRef"))
	}
	if get("spec", "topology", "version") != "v1.35.8" {
		t.Errorf("version = %v", get("spec", "topology", "version"))
	}
	if get("spec", "topology", "controlPlane") != nil {
		t.Error("a managed control plane must get no replicas")
	}
	pools, _, _ := unstructured.NestedSlice(c.Object, "spec", "topology", "workers", "machinePools")
	if len(pools) != 1 || pools[0].(map[string]any)["replicas"] != int64(3) || pools[0].(map[string]any)["class"] != "default-pool" {
		t.Errorf("workers = %v", pools)
	}
	v := clusters.Variables(c)
	if v["machineType"] != "e2-medium" || v["project"] != "p1" {
		t.Errorf("variables = %v", v)
	}
	if _, ok := v["noDefault"]; ok {
		t.Error("a nil variable should be left to the ClusterClass")
	}
	l := c.GetLabels()
	if l[LabelTemplate] != "gke-small" || l[LabelTemplateNamespace] != "capi-clusters" || l["hyve.io/team"] != "web" || l["cni"] != "flannel" || l["cluster-api.cattle.io/rancher-auto-import"] != "" {
		t.Errorf("labels = %v", l)
	}
	if got := c.GetAnnotations()[AnnotationExpiresAt]; got != "2026-10-10T17:00:00Z" {
		t.Errorf("expires-at = %s", got)
	}
}

func TestFillKeepsWhatTheClusterSets(t *testing.T) {
	c := clusters.New("web", "capi-clusters")
	_ = unstructured.SetNestedField(c.Object, "v1.33.1", "spec", "topology", "version")
	c.SetAnnotations(map[string]string{AnnotationExpiresAt: Never})
	if err := Fill(c, tmpl(), class(true), now); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version"); v != "v1.33.1" {
		t.Errorf("version = %s", v)
	}
	if r, _, _ := unstructured.NestedInt64(c.Object, "spec", "topology", "controlPlane", "replicas"); r != 1 {
		t.Errorf("a machine control plane should default to 1 replica, got %d", r)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(c.Object, "spec", "topology", "classRef", "namespace"); found {
		t.Error("same-namespace class needs no classRef.namespace")
	}
	if got := c.GetAnnotations()[AnnotationExpiresAt]; got != Never {
		t.Errorf("expires-at = %s, want never kept", got)
	}
	if _, ok, _ := ExpiresAt(c); ok {
		t.Error("never must mean no expiry")
	}
}

func TestFillRefusesLockedAndOtherClass(t *testing.T) {
	c := clusters.New("x", "prod")
	_ = clusters.SetVariables(c, map[string]any{"project": "other"})
	var le *LockedError
	if err := Fill(c, tmpl(), class(false), now); !errors.As(err, &le) || le.Names[0] != "project" {
		t.Errorf("err = %v", err)
	}
	c = clusters.New("x", "prod")
	_ = clusters.SetVariables(c, map[string]any{"project": "p1"})
	if err := Fill(c, tmpl(), class(false), now); err != nil {
		t.Errorf("the locked value itself is fine: %v", err)
	}
	c = clusters.New("x", "prod")
	_ = unstructured.SetNestedField(c.Object, "capd", "spec", "topology", "classRef", "name")
	if err := Fill(c, tmpl(), class(false), now); err == nil {
		t.Error("another class must be refused")
	}
}

func TestFillNeedsAVersion(t *testing.T) {
	tp := tmpl()
	tp.Spec.Version = ""
	cl := class(false)
	cl.SetAnnotations(nil)
	if err := Fill(clusters.New("x", "prod"), tp, cl, now); err == nil {
		t.Error("want an error with no version anywhere")
	}
	cl = class(false)
	c := clusters.New("x", "prod")
	if err := Fill(c, tp, cl, now); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version"); v != "v1.34.0" {
		t.Errorf("version = %s, want the class default", v)
	}
}

func TestCheckLockedAndChanged(t *testing.T) {
	c := clusters.New("x", "prod")
	_ = clusters.SetVariables(c, map[string]any{"project": "p2"})
	if bad := CheckLocked(tmpl(), c); len(bad) != 1 {
		t.Errorf("bad = %v", bad)
	}
	old := clusters.New("x", "prod")
	_ = clusters.SetVariables(old, map[string]any{"project": "p2"})
	if ch := ChangedVariables(old, c, []string{"project"}); len(ch) != 0 {
		t.Errorf("unchanged = %v", ch)
	}
}

func TestValidate(t *testing.T) {
	tp := tmpl()
	if err := Validate(tp); err != nil {
		t.Fatal(err)
	}
	tp.Spec.Resources = []resources.Entry{{Name: "a", HelmOp: map[string]any{"helm": map[string]any{"chart": "x"}}}, {Name: "a", Ref: "y"}}
	if err := Validate(tp); err == nil {
		t.Error("duplicate resource names must fail")
	}
	tp.Spec.Resources = []resources.Entry{{Name: "a", HelmOp: map[string]any{"helm": map[string]any{"chart": "x"}, "targets": []any{}}}}
	if err := Validate(tp); err == nil {
		t.Error("targets must be refused")
	}
	tp = tmpl()
	tp.Spec.LockedVariables = []string{"missing"}
	if err := Validate(tp); err == nil {
		t.Error("locking a variable with no value must fail")
	}
}

func TestLockedVersion(t *testing.T) {
	tp := tmpl()
	tp.Spec.LockedVariables = []string{LockVersion}
	if err := Validate(tp); err != nil {
		t.Fatal(err)
	}
	c := clusters.New("x", "prod")
	_ = unstructured.SetNestedField(c.Object, "v1.35.8", "spec", "topology", "version")
	if bad := CheckLocked(tp, c); len(bad) != 0 {
		t.Errorf("at the template's version: bad = %v", bad)
	}
	up := c.DeepCopy()
	_ = unstructured.SetNestedField(up.Object, "v1.36.0", "spec", "topology", "version")
	if bad := CheckLocked(tp, up); len(bad) != 1 || bad[0] != LockVersion {
		t.Errorf("upgraded: bad = %v", bad)
	}
	if ch := ChangedVariables(c, up, []string{LockVersion}); len(ch) != 1 {
		t.Errorf("changed = %v", ch)
	}
	var locked *LockedError
	if err := Fill(up, tp, class(false), now); !errors.As(err, &locked) {
		t.Errorf("Fill at another version: %v", err)
	}
	tp.Spec.Version = ""
	if err := Validate(tp); err == nil {
		t.Error("a locked version needs kubernetesVersion")
	}
}
