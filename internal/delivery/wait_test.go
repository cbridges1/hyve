package delivery

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/cbridges1/hyve/internal/resources"
)

func obj(kind, name string, gen int64, labels map[string]string, spec, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1", "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ws, "generation": gen},
		"spec":     spec, "status": status,
	}}
	if labels != nil {
		u.SetLabels(labels)
	}
	return u
}

// bd is a cluster's BundleDeployment of bundle: applied is the deployment
// it runs, staged the one its bundle wants.
func bd(bundle, cluster, staged, applied string, ready bool) *unstructured.Unstructured {
	id := applied
	if staged != applied && applied == "" {
		id = staged
	}
	u := obj("BundleDeployment", bundle+"-"+cluster, 1, map[string]string{
		"fleet.cattle.io/bundle-name": bundle, "fleet.cattle.io/bundle-namespace": ws,
		"fleet.cattle.io/cluster-namespace": "dev", "fleet.cattle.io/cluster": cluster,
	}, map[string]any{"deploymentID": id, "stagedDeploymentID": staged},
		map[string]any{"appliedDeploymentID": applied, "ready": ready})
	u.SetNamespace("cluster-dev-" + cluster)
	return u
}

func fakeClient(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	kinds := map[schema.GroupVersionResource]string{
		resources.BundleDeploymentGVR: "BundleDeploymentList", resources.BundleGVR: "BundleList",
		resources.HelmOpGVR: "HelmOpList", resources.GitRepoGVR: "GitRepoList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objs...)
}

func helmSpec(version string) map[string]any {
	return map[string]any{
		"defaultNamespace": "web",
		"helm":             map[string]any{"chart": "web", "repo": "https://charts.example", "version": version, "values": map[string]any{"replicas": int64(2)}},
		"targets":          []any{map[string]any{"clusterGroup": "template-web"}},
	}
}

// The Bundle Fleet makes from helmSpec: targets named, version resolved,
// its own fields added.
func bundleSpec(version string) map[string]any {
	s := helmSpec(version)
	s["targets"] = []any{map[string]any{"clusterGroup": "template-web", "name": "target000"}}
	s["helmOpOptions"] = map[string]any{}
	s["paused"] = false
	return s
}

func TestHelmSynced(t *testing.T) {
	op := obj("HelmOp", "web", 2, nil, helmSpec("1.2.0"), nil)
	if !helmSynced(op, obj("Bundle", "web", 1, nil, bundleSpec("1.2.0"), nil)) {
		t.Error("a Bundle Fleet made from the HelmOp isn't synced")
	}
	if helmSynced(op, obj("Bundle", "web", 1, nil, bundleSpec("1.1.0"), nil)) {
		t.Error("a Bundle of the old version is synced")
	}
	old := bundleSpec("1.2.0")
	old["helm"].(map[string]any)["values"] = map[string]any{"replicas": int64(1)}
	if helmSynced(op, obj("Bundle", "web", 1, nil, old, nil)) {
		t.Error("a Bundle with the old values is synced")
	}
	// A range resolves to whatever Fleet found.
	ranged := obj("HelmOp", "web", 2, nil, helmSpec("^1.2"), nil)
	if !helmSynced(ranged, obj("Bundle", "web", 1, nil, bundleSpec("1.2.7"), nil)) {
		t.Error("a range's resolved version isn't synced")
	}
}

func TestCheckHelmOp(t *testing.T) {
	ctx := context.Background()
	op := obj("HelmOp", "web", 2, nil, helmSpec("1.2.0"), nil)
	bundle := obj("Bundle", "web", 3, map[string]string{"fleet.cattle.io/fleet-helm-name": "web"}, bundleSpec("1.2.0"),
		map[string]any{"observedGeneration": int64(3), "summary": map[string]any{"desiredReady": int64(2)}})

	// One cluster runs the new deployment; the rollout holds the other back.
	dc := fakeClient(bundle, bd("web", "a", "new", "new", true), bd("web", "b", "new", "old", true))
	p, err := Check(ctx, dc, op)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Synced || p.Ready != 1 || p.Desired != 2 || p.Done() {
		t.Fatalf("half rolled out: %+v", p)
	}

	dc = fakeClient(bundle, bd("web", "a", "new", "new", true), bd("web", "b", "new", "new", true))
	if p, _ = Check(ctx, dc, op); !p.Done() {
		t.Fatalf("rolled out: %+v", p)
	}

	// Fleet hasn't taken the change in: the Bundle has the old version.
	stale := bundle.DeepCopy()
	stale.Object["spec"] = bundleSpec("1.1.0")
	dc = fakeClient(stale, bd("web", "a", "old", "old", true), bd("web", "b", "old", "old", true))
	if p, _ = Check(ctx, dc, op); p.Synced || p.Done() {
		t.Fatalf("an old Bundle counts as done: %+v", p)
	}

	// Not ready on a cluster that applied it isn't done either.
	dc = fakeClient(bundle, bd("web", "a", "new", "new", true), bd("web", "b", "new", "new", false))
	if p, _ = Check(ctx, dc, op); p.Done() || p.Ready != 1 {
		t.Fatalf("a cluster that isn't ready: %+v", p)
	}

	paused := op.DeepCopy()
	_ = unstructured.SetNestedField(paused.Object, true, "spec", "paused")
	if p, _ = Check(ctx, fakeClient(bundle), paused); !p.Paused || p.Done() {
		t.Fatalf("paused: %+v", p)
	}
}

func TestCheckErrApplied(t *testing.T) {
	b := obj("Bundle", "web", 1, nil, map[string]any{}, map[string]any{"observedGeneration": int64(1), "summary": map[string]any{"desiredReady": int64(1)}})
	failed := bd("web", "a", "new", "old", false)
	_ = unstructured.SetNestedField(failed.Object, map[string]any{"state": "ErrApplied", "message": "no matches for kind Widget"}, "status", "display")
	p, err := Check(context.Background(), fakeClient(b, failed), b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Problem == "" || p.Done() {
		t.Fatalf("an apply error isn't a problem: %+v", p)
	}
}

func TestCheckGitRepo(t *testing.T) {
	ctx := context.Background()
	repo := obj("GitRepo", "config", 4, nil, map[string]any{"repo": "https://git.example/config"},
		map[string]any{"observedGeneration": int64(4), "commit": "abc", "gitJobStatus": "Current"})
	bundle := func(commit string) *unstructured.Unstructured {
		return obj("Bundle", "config-apps", 1, map[string]string{"fleet.cattle.io/repo-name": "config", "fleet.cattle.io/commit": commit}, map[string]any{},
			map[string]any{"observedGeneration": int64(1), "summary": map[string]any{"desiredReady": int64(1)}})
	}
	ready := bd("config-apps", "a", "x", "x", true)

	if p, _ := Check(ctx, fakeClient(bundle("abc"), ready), repo); !p.Done() {
		t.Fatalf("deployed commit: %+v", p)
	}
	if p, _ := Check(ctx, fakeClient(bundle("old"), ready), repo); p.Synced {
		t.Fatalf("a bundle of an old commit is synced: %+v", p)
	}
	polled := repo.DeepCopy()
	_ = unstructured.SetNestedField(polled.Object, "def", "status", "pollingCommit")
	if p, _ := Check(ctx, fakeClient(bundle("abc"), ready), polled); p.Synced {
		t.Fatalf("a newer commit not yet deployed is synced: %+v", p)
	}
	unseen := repo.DeepCopy()
	unseen.SetGeneration(5)
	if p, _ := Check(ctx, fakeClient(bundle("abc"), ready), unseen); p.Synced {
		t.Fatalf("a spec change Fleet hasn't seen is synced: %+v", p)
	}
}
