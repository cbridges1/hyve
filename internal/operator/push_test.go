package operator

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

var pushNow = time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)

func fleetCluster(lastSeen string) *unstructured.Unstructured {
	fc := object(fleetClusterGVK)
	fc.SetNamespace("default")
	fc.SetName("demo")
	_ = unstructured.SetNestedField(fc.Object, "demo-kubeconfig", "spec", "kubeConfigSecret")
	_ = unstructured.SetNestedField(fc.Object, "cluster-default-demo-1", "status", "namespace")
	if lastSeen != "" {
		_ = unstructured.SetNestedField(fc.Object, lastSeen, "status", "agent", "lastSeen")
	}
	return fc
}

func capiDemo(delivery string) *unstructured.Unstructured {
	c := object(clusterGVK)
	c.SetNamespace("default")
	c.SetName("demo")
	if delivery != "" {
		c.SetLabels(map[string]string{LabelDelivery: delivery})
	}
	return c
}

func TestIsPush(t *testing.T) {
	for _, tc := range []struct {
		name     string
		capi     *unstructured.Unstructured
		lastSeen string
		want     bool
	}{
		{"agent never checked in", capiDemo(""), "", true},
		{"agent checked in recently", capiDemo(""), pushNow.Add(-time.Minute).Format(time.RFC3339), false},
		{"agent gone quiet", capiDemo(""), pushNow.Add(-time.Hour).Format(time.RFC3339), true},
		{"label forces push", capiDemo("push"), pushNow.Format(time.RFC3339), true},
		{"label forces agent", capiDemo("agent"), "", false},
		{"not a CAPI cluster's", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fake.NewClientBuilder()
			if tc.capi != nil {
				b = b.WithObjects(tc.capi)
			}
			p := &Push{Client: b.Build(), Now: func() time.Time { return pushNow }}
			if got, why := p.isPush(context.Background(), fleetCluster(tc.lastSeen)); got != tc.want {
				t.Errorf("isPush = %v (%s), want %v", got, why, tc.want)
			}
		})
	}
}

// helmRelease encodes a release as Helm stores it in a Secret's data.
func helmRelease(t *testing.T, name, ns string, version int, annotations map[string]string, manifest string) []byte {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"name": name, "namespace": ns, "version": version, "manifest": manifest,
		"chart": map[string]any{"metadata": map[string]any{"annotations": annotations}},
	})
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(raw)
	_ = w.Close()
	return []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))
}

func TestDecodeRelease(t *testing.T) {
	data := helmRelease(t, "podinfo", "podinfo", 3, map[string]string{chartBundleID: "tpl-x", chartAgentNamespace: fleetAgentNamespace, chartKeepResources: "true"}, "kind: ConfigMap\n")
	r, err := decodeRelease(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "podinfo" || r.Namespace != "podinfo" || r.Version != 3 || r.BundleID != "tpl-x" || r.AgentNamespace != fleetAgentNamespace || !r.KeepResources || r.Manifest != "kind: ConfigMap\n" {
		t.Errorf("decoded %+v", r)
	}
}

func TestChartSecretMatchesItsChecksum(t *testing.T) {
	out := []byte(`apiVersion: fleet.cattle.io/v1alpha1
kind: Bundle
spec:
  resources:
  - name: .chart/abc/podinfo/Chart.yaml
    content: "name: podinfo\n"
`)
	s, err := chartSecret(out, "hyve-chart-x", "default", "demo")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(s.Data["content"]))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(zr)
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != string(s.Data["sha256sum"]) {
		t.Error("sha256sum isn't the manifest's: fleet deploy would refuse it")
	}
	if !strings.Contains(string(data), "podinfo/Chart.yaml") {
		t.Errorf("manifest = %s", data)
	}
	if _, err := chartSecret([]byte("kind: Bundle\nspec: {}\n"), "x", "default", "demo"); err == nil {
		t.Error("a Bundle without the chart should be refused")
	}
}

type fakeChild struct {
	releases    []Release
	applied     []string
	uninstalled []string
}

func (f *fakeChild) ApplyObject(_ context.Context, o *unstructured.Unstructured) error {
	f.applied = append(f.applied, o.GetKind()+" "+o.GetNamespace()+"/"+o.GetName())
	return nil
}
func (f *fakeChild) ListReleases(context.Context) ([]Release, error) { return f.releases, nil }
func (f *fakeChild) Uninstall(_ context.Context, r Release) error {
	f.uninstalled = append(f.uninstalled, r.Namespace+"/"+r.Name)
	return nil
}

func TestPushDeploysAndReports(t *testing.T) {
	ctx := context.Background()
	bd := object(bundleDeploymentGVK)
	bd.SetNamespace("cluster-default-demo-1")
	bd.SetName("tpl-web-nginx")
	bd.SetLabels(map[string]string{"fleet.cattle.io/cluster": "demo", "fleet.cattle.io/cluster-namespace": "default"})
	_ = unstructured.SetNestedField(bd.Object, "s-abc:opts", "spec", "deploymentID")
	_ = unstructured.SetNestedMap(bd.Object, map[string]any{
		"defaultNamespace":    "web",
		"helm":                map[string]any{"releaseName": "nginx"},
		"downstreamResources": []any{map[string]any{"kind": "Secret", "name": "dns-token"}},
	}, "spec", "options")
	content := object(contentGVK)
	content.SetName("s-abc")
	content.Object["content"] = "H4sI"
	content.Object["sha256sum"] = "abc"
	kc := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "demo-kubeconfig"}, Data: map[string][]byte{"value": []byte("kubeconfig")}}
	vals := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: bd.GetNamespace(), Name: bd.GetName()}, Data: map[string][]byte{"values": []byte(`{"replicaCount":2}`)}}
	dns := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: bd.GetNamespace(), Name: "dns-token"}, Data: map[string][]byte{"token": []byte("t")}}
	fleetCtl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"namespace": "cattle-fleet-system", "name": "fleet-controller"},
		"spec":     map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "c", "image": "rancher/fleet:v9"}}}}},
	}}
	cl := fake.NewClientBuilder().WithObjects(capiDemo(""), fleetCluster(""), bd, content, kc, vals, dns, fleetCtl).WithStatusSubresource(bd).Build()
	stale := Release{Name: "old", Namespace: "x", BundleID: "tpl-gone", AgentNamespace: fleetAgentNamespace}
	other := Release{Name: "mine", Namespace: "y", BundleID: ""}
	child := &fakeChild{releases: []Release{stale, other}}
	p := &Push{Client: cl, Reader: cl, NewChild: func([]byte) (Child, error) { return child, nil }, Now: func() time.Time { return pushNow }}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "demo"}}

	if _, err := p.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if len(child.uninstalled) != 1 || child.uninstalled[0] != "x/old" {
		t.Errorf("uninstalled %v, want only Fleet's release for a gone BundleDeployment", child.uninstalled)
	}
	if strings.Join(child.applied, ",") != "Namespace /web,Secret web/dns-token" {
		t.Errorf("applied to child %v, want the downstream Secret in the target namespace", child.applied)
	}
	var jobs batchv1.JobList
	if err := cl.List(ctx, &jobs, client.InNamespace("default")); err != nil || len(jobs.Items) != 1 {
		t.Fatalf("jobs %v %v", len(jobs.Items), err)
	}
	j := jobs.Items[0]
	if got := strings.Join(j.Spec.Template.Spec.Containers[0].Command, " "); got != "fleet deploy -i /in/input.yaml -a cattle-fleet-system -n web --kubeconfig /kubeconfig/value" || j.Spec.Template.Spec.Containers[0].Image != "rancher/fleet:v9" {
		t.Errorf("job runs %s %q", j.Spec.Template.Spec.Containers[0].Image, got)
	}
	var in corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "default", Name: j.Name}, &in); err != nil {
		t.Fatal(err)
	}
	docs := strings.Split(string(in.Data["input.yaml"]), "\n---\n")
	var input map[string]any
	if err := yaml.Unmarshal([]byte(docs[1]), &input); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := unstructured.NestedFieldNoCopy(input, "spec", "options", "helm", "values", "replicaCount"); v != float64(2) {
		t.Errorf("input options lack the cluster's values: %s", docs[1])
	}

	// Again while it runs: nothing new.
	if _, err := p.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	_ = cl.List(ctx, &jobs, client.InNamespace("default"))
	if len(jobs.Items) != 1 {
		t.Fatalf("a second Job started: %d", len(jobs.Items))
	}

	// Done: the status is written as fleet-agent writes it.
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if err := cl.Status().Update(ctx, &j); err != nil {
		t.Fatal(err)
	}
	child.releases = append(child.releases, Release{Name: "nginx", Namespace: "web", Version: 1, BundleID: "tpl-web-nginx", AgentNamespace: fleetAgentNamespace})
	if _, err := p.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	got := object(bundleDeploymentGVK)
	if err := cl.Get(ctx, client.ObjectKeyFromObject(bd), got); err != nil {
		t.Fatal(err)
	}
	applied, _, _ := unstructured.NestedString(got.Object, "status", "appliedDeploymentID")
	ready, _, _ := unstructured.NestedBool(got.Object, "status", "ready")
	release, _, _ := unstructured.NestedString(got.Object, "status", "release")
	if applied != "s-abc:opts" || !ready || release != "web/nginx:1" {
		t.Errorf("status = %v", got.Object["status"])
	}
}

func TestPushJobNameChangesWithTheBundleDeployment(t *testing.T) {
	a := pushJobName("hyve-push", "default", "demo", "bd", "uid-1", "s-abc:x", "")
	if a != pushJobName("hyve-push", "default", "demo", "bd", "uid-1", "s-abc:x", "") {
		t.Error("the same deploy should map to the same Job")
	}
	if a == pushJobName("hyve-push", "default", "demo", "bd", "uid-2", "s-abc:x", "") {
		t.Error("a BundleDeployment made again would reuse the old one's finished Job")
	}
}

func TestPushLeavesAgentClustersAlone(t *testing.T) {
	cl := fake.NewClientBuilder().WithObjects(capiDemo(""), fleetCluster(pushNow.Format(time.RFC3339))).Build()
	called := false
	p := &Push{Client: cl, Reader: cl, NewChild: func([]byte) (Child, error) { called = true; return &fakeChild{}, nil }, Now: func() time.Time { return pushNow }}
	res, err := p.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "demo"}})
	if err != nil || called || res.RequeueAfter != agentStale {
		t.Errorf("err=%v child=%v requeue=%v", err, called, res.RequeueAfter)
	}
}

func TestChartAuthFlags(t *testing.T) {
	assert.Equal(t, "", chartAuthFlags(nil, false))
	assert.Equal(t, " --helm-insecure-skip-tls", chartAuthFlags(nil, true))
	sec := &corev1.Secret{Data: map[string][]byte{"username": []byte("u"), "password": []byte("p"), "cacerts": []byte("pem"), "basicHTTP": []byte("true")}}
	got := chartAuthFlags(sec, false)
	assert.Contains(t, got, `--username "$(cat /auth/username)"`)
	assert.Contains(t, got, "--password-file /auth/password")
	assert.Contains(t, got, "--cacerts-file /auth/cacerts")
	assert.Contains(t, got, "--helm-repo-url-regex '.*'")
	assert.Contains(t, got, "--helm-basic-http")
	assert.NotContains(t, got, "--helm-insecure-skip-tls")
	// A username without a password isn't credentials.
	assert.NotContains(t, chartAuthFlags(&corev1.Secret{Data: map[string][]byte{"username": []byte("u")}}, false), "--username")
}

func TestDriftDue(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	r := &Push{Now: func() time.Time { return now }}
	bd := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "b", "uid": "u1"}, "spec": map[string]any{}}}
	assert.False(t, r.driftDue(bd), "without correctDrift")
	_ = unstructured.SetNestedField(bd.Object, true, "spec", "correctDrift", "enabled")
	assert.False(t, r.driftDue(bd), "first seen: wait an interval")
	now = now.Add(pushResync / 2)
	assert.False(t, r.driftDue(bd))
	now = now.Add(pushResync / 2)
	assert.True(t, r.driftDue(bd))
	assert.False(t, r.driftDue(bd), "just redeployed")
}
