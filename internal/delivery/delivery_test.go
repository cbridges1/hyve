package delivery

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/resources"
)

const ws = "capi-clusters"

func targets(t *testing.T, ss ...string) []Target {
	t.Helper()
	var out []Target
	for _, s := range ss {
		tg, err := ParseTarget(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tg)
	}
	return out
}

func TestParseTarget(t *testing.T) {
	for _, s := range []string{"template/civo", "class/civo", "env/prod", "cluster/prod/web", "group/gpu", "all", "host"} {
		tg, err := ParseTarget(s)
		if err != nil || tg.String() != s {
			t.Errorf("%s: %v %v", s, tg, err)
		}
	}
	for _, s := range []string{"cluster/web", "template/", "nope/x", "everything"} {
		if _, err := ParseTarget(s); err == nil {
			t.Errorf("%s parsed", s)
		}
	}
}

func TestHome(t *testing.T) {
	cases := map[string][]string{
		ws:            {"template/civo", "env/prod"},
		"prod":        {"cluster/prod/web", "env/prod"},
		HostWorkspace: {"host"},
	}
	for want, ts := range cases {
		if got, err := Home(targets(t, ts...), ws); err != nil || got != want {
			t.Errorf("%v: %s %v, want %s", ts, got, err, want)
		}
	}
	if got, _ := Home(targets(t, "cluster/prod/web", "cluster/dev/web"), ws); got != ws {
		t.Errorf("two environments' clusters live in %s", got)
	}
	if _, err := Home(targets(t, "host", "all"), ws); err == nil {
		t.Error("host with other targets passed")
	}
}

// Every target kind comes back as itself from the Fleet targets it makes.
func TestTargetsRoundTrip(t *testing.T) {
	cases := map[string][]string{
		ws:            {"template/civo", "class/civo", "env/prod", "cluster/prod/web", "group/gpu", "all"},
		"prod":        {"cluster/prod/web", "env/prod"},
		HostWorkspace: {"host"},
	}
	for home, ss := range cases {
		ts := targets(t, ss...)
		ft, err := FleetTargets(home, ws, ts)
		if err != nil {
			t.Fatal(err)
		}
		o := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": home}, "spec": map[string]any{"targets": ft}}}
		got := Decode(o, ws, map[string]bool{"civo": true})
		if !reflect.DeepEqual(got, ts) {
			t.Errorf("%s: %v came back as %v (via %v)", home, ts, got, ft)
		}
	}
	if _, err := FleetTargets("prod", ws, targets(t, "template/civo")); err == nil {
		t.Error("an environment's Deployment targeted a template")
	}
	custom := map[string]any{"clusterSelector": map[string]any{"matchLabels": map[string]any{"gpu": "true"}}}
	o := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": ws}, "spec": map[string]any{"targets": []any{custom}}}}
	if got := Decode(o, ws, nil); len(got) != 1 || got[0].Kind != Custom {
		t.Errorf("custom target = %v", got)
	}
	none := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": HostWorkspace}, "spec": map[string]any{}}}
	if got := Decode(none, ws, nil); len(got) != 1 || got[0].Kind != Host {
		t.Errorf("a fleet-local GitRepo with no targets = %v", got)
	}
}

func TestRollout(t *testing.T) {
	for _, c := range []string{RolloutSerial, RolloutCanary} {
		rs, err := RolloutStrategy(c)
		if err != nil {
			t.Fatal(err)
		}
		o := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"rolloutStrategy": rs}}}
		if got := RolloutOf(o); got != c {
			t.Errorf("%s came back as %s", c, got)
		}
	}
	o := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"rolloutStrategy": map[string]any{"maxUnavailable": "10%"}}}}
	if got := RolloutOf(o); got != RolloutCustom {
		t.Errorf("custom = %s", got)
	}
}

func TestObjectKeepsWhatItDoesntModel(t *testing.T) {
	d := Deployment{
		Name:    "podinfo",
		Source:  resources.Entry{Namespace: "podinfo", Secrets: []string{"s"}, HelmOp: map[string]any{"helm": map[string]any{"repo": "https://x", "chart": "podinfo", "version": "6.7.1"}}},
		Targets: targets(t, "template/civo", "env/prod"),
		Rollout: RolloutSerial,
	}
	u, err := Object(d, ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.GetNamespace() != ws || u.GetKind() != "HelmOp" {
		t.Fatalf("%s %s/%s", u.GetKind(), u.GetNamespace(), u.GetName())
	}
	_ = unstructured.SetNestedField(u.Object, map[string]any{"x": "y"}, "spec", "diff")
	u.SetAnnotations(map[string]string{"note": "kept"})
	d.Rollout = RolloutAll
	again, err := Object(d, ws, u)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := unstructured.NestedMap(again.Object, "spec", "diff"); !ok || again.GetAnnotations()["note"] != "kept" {
		t.Errorf("lost what the form doesn't edit: %v", again.Object)
	}
	if _, ok, _ := unstructured.NestedMap(again.Object, "spec", "rolloutStrategy"); ok {
		t.Error("rollout all kept the old strategy")
	}
	git := Deployment{Name: "wg", Source: resources.Entry{GitRepo: map[string]any{"repo": "https://g"}}, Targets: targets(t, "template/civo"), Rollout: RolloutCanary}
	if _, err := Object(git, ws, nil); err == nil {
		t.Error("a git source took a rollout")
	}
	if _, err := Object(d, ws, &unstructured.Unstructured{Object: map[string]any{"kind": "GitRepo"}}); err == nil {
		t.Error("a GitRepo became a HelmOp")
	}
}
