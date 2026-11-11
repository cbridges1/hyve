package delivery

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/cbridges1/hyve/internal/resources"
)

// Progress is how far a Deployment's latest change has got, for `hyve
// deploy wait`: Fleet has taken the change in (its Bundles match the
// Deployment), then each cluster it targets has applied that change and
// is ready.
type Progress struct {
	// Synced: Fleet's Bundles are the Deployment's latest spec (for a
	// GitRepo, its latest commit).
	Synced bool `json:"synced"`
	// Desired is how many clusters it targets; Ready how many run the
	// latest change, ready.
	Desired int  `json:"desired"`
	Ready   int  `json:"ready"`
	Paused  bool `json:"paused,omitempty"`
	// Problem is why it can't go on ("" while it can): an error on the
	// Deployment (a chart that can't be found, a repo it can't clone) or
	// a cluster that couldn't apply it.
	Problem  string            `json:"problem,omitempty"`
	Clusters []ClusterProgress `json:"clusters,omitempty"`
}

// ClusterProgress is one cluster's part of a Deployment.
type ClusterProgress struct {
	Cluster string `json:"cluster"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Done    bool   `json:"done"`
}

// Done reports whether every targeted cluster runs the latest change.
func (p Progress) Done() bool {
	return p.Synced && !p.Paused && p.Problem == "" && p.Ready >= p.Desired
}

// Summary is a one-line account, such as "2/3 clusters ready".
func (p Progress) Summary() string {
	switch {
	case p.Paused:
		return "paused"
	case !p.Synced:
		return "waiting for Fleet to take in the change"
	}
	return fmt.Sprintf("%d/%d clusters ready", p.Ready, p.Desired)
}

// BDState is a BundleDeployment's state and its message, if any.
func BDState(bd *unstructured.Unstructured) (string, string) {
	state, _, _ := unstructured.NestedString(bd.Object, "status", "display", "state")
	if state == "" {
		if ready, _, _ := unstructured.NestedBool(bd.Object, "status", "ready"); ready {
			state = "Ready"
		} else {
			state = "Pending"
		}
	}
	msg, _, _ := unstructured.NestedString(bd.Object, "status", "display", "message")
	if msg == "" {
		conds, _, _ := unstructured.NestedSlice(bd.Object, "status", "conditions")
		for _, c := range conds {
			if m, ok := c.(map[string]any); ok && m["status"] == "False" && m["message"] != nil && m["message"] != "" {
				msg = fmt.Sprint(m["message"])
				break
			}
		}
	}
	return state, msg
}

// Check reads a Deployment's progress. o is the Deployment as just read.
func Check(ctx context.Context, dc dynamic.Interface, o *unstructured.Unstructured) (Progress, error) {
	var p Progress
	p.Paused, _, _ = unstructured.NestedBool(o.Object, "spec", "paused")
	bundles, synced, err := bundlesOf(ctx, dc, o)
	if err != nil {
		return p, err
	}
	p.Synced = synced
	if o.GetKind() != "GitRepo" || observed(o) {
		p.Problem = resources.Problem(o)
	}
	// Clusters are listed even before Fleet has taken the change in (for
	// 'hyve deploy show'); they count only once it has (Done).
	by := map[string]*ClusterProgress{}
	for _, b := range bundles {
		d, _, _ := unstructured.NestedInt64(b.Object, "status", "summary", "desiredReady")
		p.Desired += int(d)
		bds, err := dc.Resource(resources.BundleDeploymentGVR).List(ctx, metav1.ListOptions{
			LabelSelector: "fleet.cattle.io/bundle-namespace=" + b.GetNamespace() + ",fleet.cattle.io/bundle-name=" + b.GetName(),
		})
		if err != nil {
			return p, err
		}
		for i := range bds.Items {
			bd := &bds.Items[i]
			l := bd.GetLabels()
			key := l["fleet.cattle.io/cluster-namespace"] + "/" + l["fleet.cattle.io/cluster"]
			state, msg := BDState(bd)
			done := current(bd)
			if done {
				p.Ready++
			}
			// A GitRepo's bundles count once per cluster; the worst shows.
			if cp := by[key]; cp == nil {
				by[key] = &ClusterProgress{Cluster: key, State: state, Message: msg, Done: done}
			} else if cp.Done && !done {
				*cp = ClusterProgress{Cluster: key, State: state, Message: msg}
			}
			if state == "ErrApplied" && p.Problem == "" {
				p.Problem = fmt.Sprintf("%s: %s", key, strings.Join(strings.Fields(msg), " "))
			}
		}
	}
	for _, cp := range by {
		p.Clusters = append(p.Clusters, *cp)
	}
	sort.Slice(p.Clusters, func(a, b int) bool { return p.Clusters[a].Cluster < p.Clusters[b].Cluster })
	return p, nil
}

// current reports whether a cluster has applied a bundle's latest
// deployment (not one a rollout is still holding back) and it's ready.
func current(bd *unstructured.Unstructured) bool {
	id, _, _ := unstructured.NestedString(bd.Object, "spec", "deploymentID")
	staged, _, _ := unstructured.NestedString(bd.Object, "spec", "stagedDeploymentID")
	applied, _, _ := unstructured.NestedString(bd.Object, "status", "appliedDeploymentID")
	ready, _, _ := unstructured.NestedBool(bd.Object, "status", "ready")
	return id != "" && id == applied && (staged == "" || staged == id) && ready
}

// observed reports whether Fleet has seen an object's latest spec.
func observed(o *unstructured.Unstructured) bool {
	g, _, _ := unstructured.NestedInt64(o.Object, "status", "observedGeneration")
	return g >= o.GetGeneration()
}

// bundlesOf returns the Bundles a Deployment is delivered as, and whether
// they're its latest spec.
func bundlesOf(ctx context.Context, dc dynamic.Interface, o *unstructured.Unstructured) ([]*unstructured.Unstructured, bool, error) {
	bundles := dc.Resource(resources.BundleGVR).Namespace(o.GetNamespace())
	switch o.GetKind() {
	case "Bundle":
		return []*unstructured.Unstructured{o}, observed(o), nil
	case "HelmOp":
		b, err := bundles.Get(ctx, o.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return []*unstructured.Unstructured{b}, observed(b) && helmSynced(o, b), nil
	case "GitRepo":
		commit, _, _ := unstructured.NestedString(o.Object, "status", "commit")
		synced := observed(o) && commit != "" && gitCurrent(o, commit)
		list, err := bundles.List(ctx, metav1.ListOptions{LabelSelector: "fleet.cattle.io/repo-name=" + o.GetName()})
		if err != nil {
			return nil, false, err
		}
		var out []*unstructured.Unstructured
		for i := range list.Items {
			b := &list.Items[i]
			if b.GetLabels()["fleet.cattle.io/commit"] != commit || !observed(b) {
				synced = false
			}
			out = append(out, b)
		}
		return out, synced, nil
	}
	return nil, false, fmt.Errorf("%s isn't a Deployment", o.GetKind())
}

// gitCurrent reports whether a GitRepo has nothing newer than commit to
// deploy: no git job running, and no newer commit seen by polling or a
// webhook.
func gitCurrent(o *unstructured.Unstructured, commit string) bool {
	status, _, _ := unstructured.NestedMap(o.Object, "status")
	if j, _ := status["gitJobStatus"].(string); j != "" && j != "Current" {
		return false
	}
	for _, k := range []string{"pollingCommit", "webhookCommit"} {
		if c, _ := status[k].(string); c != "" && c != commit {
			return false
		}
	}
	conds, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	for _, c := range conds {
		if m, ok := c.(map[string]any); ok && m["type"] == "Reconciling" && m["status"] == "True" {
			return false
		}
	}
	return true
}

// helmSynced reports whether Fleet has copied a HelmOp's spec to its
// Bundle. Fleet names unnamed targets and resolves the chart version
// (a range, or none, becomes the version it found), so those don't count;
// an exact version must match.
func helmSynced(op, b *unstructured.Unstructured) bool {
	want, _, _ := unstructured.NestedMap(op.Object, "spec")
	have, _, _ := unstructured.NestedMap(b.Object, "spec")
	for _, k := range []string{"helm", "defaultNamespace", "targetNamespace", "targets", "rolloutStrategy", "paused",
		"forceSyncGeneration", "downstreamResources", "correctDrift", "diff", "keepResources", "deleteNamespace"} {
		w, h := comparable(want[k], k), comparable(have[k], k)
		if !reflect.DeepEqual(w, h) {
			return false
		}
	}
	v, _, _ := unstructured.NestedString(want, "helm", "version")
	hv, _, _ := unstructured.NestedString(have, "helm", "version")
	return !exactVersion(v) || v == hv
}

// comparable drops what Fleet fills in on the Bundle (target names, chart
// versions) and zero values, which may be left out on either side.
func comparable(v any, key string) any {
	v = prune(normalize(v))
	switch key {
	case "helm":
		if m, ok := v.(map[string]any); ok {
			delete(m, "version")
			if len(m) == 0 {
				return nil
			}
		}
	case "targets":
		if ts, ok := v.([]any); ok {
			for _, t := range ts {
				if m, ok := t.(map[string]any); ok {
					delete(m, "name")
					if h, ok := m["helm"].(map[string]any); ok {
						delete(h, "version")
						for _, f := range []string{"repo", "chart"} {
							delete(h, f)
						}
						if len(h) == 0 {
							delete(m, "helm")
						}
					}
				}
			}
		}
	}
	return v
}

// prune drops zero values: false, 0, "", empty maps and lists.
func prune(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e = prune(e); e == nil {
				delete(x, k)
			} else {
				x[k] = e
			}
		}
		if len(x) == 0 {
			return nil
		}
	case []any:
		if len(x) == 0 {
			return nil
		}
		for i, e := range x {
			x[i] = prune(e)
		}
	case bool:
		if !x {
			return nil
		}
	case float64:
		if x == 0 {
			return nil
		}
	case string:
		if x == "" {
			return nil
		}
	}
	return v
}

// exactVersion reports whether a chart version names one version, not a
// range (or "latest", when empty).
func exactVersion(v string) bool {
	return v != "" && !strings.ContainsAny(v, "*^~<>=|, xX")
}
