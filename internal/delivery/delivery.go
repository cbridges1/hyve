// Package delivery is what hyve puts on clusters, as Fleet itself models it
// (HYVE-PROPOSAL.md, "Delivery"): a Deployment is one Fleet GitRepo, HelmOp
// or Bundle, and its targets name Fleet ClusterGroups. hyve keeps a group
// for each ClusterTemplate (template-NAME) and each environment (env-NAME);
// CAAPF keeps one for each ClusterClass (named after the class). hyve has
// no delivery objects of its own: a Deployment written by hand, in git or
// with kubectl, is the same as one made in the UI.
//
// A Deployment lives in a template namespace (the Fleet workspace whose
// groups it targets), in an environment's namespace when all it targets
// is that environment's clusters, or in fleet-local for the host cluster.
// CAAPF's BundleNamespaceMappings (and hyve's, for imported clusters) let
// a template namespace's Deployments reach clusters in every environment.
//
// Targets are written as TYPE/NAME: template/civo, class/civo, env/prod,
// cluster/prod/web, group/NAME (any other ClusterGroup), all (every hyve
// cluster) and host (the management cluster).
package delivery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
)

var ClusterGroupGVR = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "clustergroups"}

const (
	// LabelEnvironment and LabelCluster on a CAPI Cluster (and, through
	// CAAPF, its Fleet cluster) name its environment and itself, so groups
	// and Deployments in a template namespace can select them.
	LabelEnvironment = clusters.LabelEnvironment
	LabelCluster     = clusters.LabelCluster
	// LabelCanary marks the clusters a canary rollout updates first.
	LabelCanary = "hyve.io/canary"
	// LabelGroupKind is on the ClusterGroups hyve keeps: template or
	// environment.
	LabelGroupKind = "hyve.io/group-kind"

	GroupKindTemplate    = "template"
	GroupKindEnvironment = "environment"
	// GroupKindCopy is an environment's copy of a group of the template
	// namespace (a class's, or one of your own): Fleet looks groups up in
	// each cluster's own namespace.
	GroupKindCopy = "copy"

	// HostWorkspace is Fleet's workspace for the management cluster, which
	// Fleet registers as HostCluster.
	HostWorkspace = "fleet-local"
	HostCluster   = "local"
)

// TemplateGroup and EnvGroup name the ClusterGroups hyve keeps.
func TemplateGroup(template string) string { return "template-" + template }
func EnvGroup(env string) string           { return "env-" + env }

// Target kinds.
const (
	Template = "template"
	Class    = "class"
	Env      = "env"
	Cluster  = "cluster"
	Group    = "group"
	All      = "all"
	Host     = "host"
	// Custom is a Fleet target hyve doesn't model; Raw holds it.
	Custom = "custom"
)

// Target is one thing a Deployment is delivered to.
type Target struct {
	Kind string `json:"kind"`
	// Name of the template, class, environment, cluster or group.
	Name string `json:"name,omitempty"`
	// Env is a cluster's environment.
	Env string `json:"env,omitempty"`
	// Raw is a Custom target as Fleet has it.
	Raw map[string]any `json:"raw,omitempty"`
}

func (t Target) String() string {
	switch t.Kind {
	case All, Host:
		return t.Kind
	case Cluster:
		return Cluster + "/" + t.Env + "/" + t.Name
	case Custom:
		return Custom
	}
	return t.Kind + "/" + t.Name
}

// ParseTarget reads TYPE/NAME (cluster/ENV/NAME, all, host).
func ParseTarget(s string) (Target, error) {
	parts := strings.Split(s, "/")
	switch {
	case len(parts) == 1 && (s == All || s == Host):
		return Target{Kind: s}, nil
	case len(parts) == 3 && parts[0] == Cluster && parts[1] != "" && parts[2] != "":
		return Target{Kind: Cluster, Env: parts[1], Name: parts[2]}, nil
	case len(parts) == 2 && parts[1] != "":
		switch parts[0] {
		case Template, Class, Env, Group:
			return Target{Kind: parts[0], Name: parts[1]}, nil
		case Cluster:
			return Target{}, fmt.Errorf("target %q: a cluster is cluster/ENVIRONMENT/NAME", s)
		}
	}
	return Target{}, fmt.Errorf("target %q: want template/NAME, class/NAME, env/NAME, cluster/ENV/NAME, group/NAME, all or host", s)
}

// Home is the namespace a new Deployment with these targets lives in: the
// host's workspace for host, an environment's namespace when every target
// is in that one environment, else the template namespace (workspace).
func Home(ts []Target, workspace string) (string, error) {
	if len(ts) == 0 {
		return "", errors.New("give at least one target")
	}
	hosts, env := 0, ""
	for _, t := range ts {
		switch t.Kind {
		case Host:
			hosts++
		case Cluster:
			env = sameEnv(env, t.Env)
		case Env:
			env = sameEnv(env, t.Name)
		default:
			env = "\x00"
		}
	}
	switch {
	case hosts == len(ts):
		return HostWorkspace, nil
	case hosts > 0:
		return "", errors.New("the host cluster can't be targeted together with other clusters")
	case env != "" && env != "\x00":
		return env, nil
	}
	return workspace, nil
}

func sameEnv(have, env string) string {
	if have == "" || have == env {
		return env
	}
	return "\x00"
}

// FleetTargets turns targets into Fleet's, for a Deployment living in
// namespace home (see Home). Environment namespaces and fleet-local can
// only reach their own clusters.
func FleetTargets(home, workspace string, ts []Target) ([]any, error) {
	var out []any
	for _, t := range ts {
		ft, err := fleetTarget(home, workspace, t)
		if err != nil {
			return nil, err
		}
		out = append(out, ft)
	}
	return out, nil
}

func fleetTarget(home, workspace string, t Target) (map[string]any, error) {
	sel := func(labels map[string]any) map[string]any {
		return map[string]any{"clusterSelector": map[string]any{"matchLabels": labels}}
	}
	if t.Kind == Custom {
		return t.Raw, nil
	}
	if home == HostWorkspace {
		if t.Kind != Host {
			return nil, fmt.Errorf("a Deployment in %s only reaches the host cluster", HostWorkspace)
		}
		return map[string]any{"clusterName": HostCluster}, nil
	}
	if home != workspace {
		// An environment's namespace: its own clusters only.
		switch {
		case t.Kind == Cluster && t.Env == home:
			return map[string]any{"clusterName": t.Name}, nil
		case t.Kind == Env && t.Name == home:
			return map[string]any{"clusterSelector": map[string]any{}}, nil
		}
		return nil, fmt.Errorf("this Deployment lives in environment %s, so it can only target that environment's clusters (not %s): make a new one for %s", home, t, t)
	}
	switch t.Kind {
	case Template:
		return map[string]any{"clusterGroup": TemplateGroup(t.Name)}, nil
	case Class:
		return map[string]any{"clusterGroup": t.Name}, nil
	case Env:
		return map[string]any{"clusterGroup": EnvGroup(t.Name)}, nil
	case Group:
		return map[string]any{"clusterGroup": t.Name}, nil
	case Cluster:
		return sel(map[string]any{LabelEnvironment: t.Env, LabelCluster: t.Name}), nil
	case All:
		return sel(map[string]any{clusters.LabelManagedBy: clusters.ManagedBy}), nil
	case Host:
		return nil, fmt.Errorf("the host cluster is reached from %s; make a separate Deployment for it", HostWorkspace)
	}
	return nil, fmt.Errorf("unknown target kind %q", t.Kind)
}

// Decode reads a Fleet object's targets back. classGroups names the
// ClusterGroups in its namespace that CAAPF keeps for ClusterClasses;
// anything hyve doesn't model comes back as Custom.
func Decode(o *unstructured.Unstructured, workspace string, classGroups map[string]bool) []Target {
	ns := o.GetNamespace()
	raw, _, _ := unstructured.NestedSlice(o.Object, "spec", "targets")
	if len(raw) == 0 && ns == HostWorkspace {
		// No targets in fleet-local: Fleet's default group, the host.
		return []Target{{Kind: Host}}
	}
	var out []Target
	for _, r := range raw {
		m, _ := r.(map[string]any)
		out = append(out, decodeOne(m, ns, workspace, classGroups))
	}
	return out
}

func decodeOne(m map[string]any, ns, workspace string, classGroups map[string]bool) Target {
	custom := Target{Kind: Custom, Raw: m}
	keys := 0
	for k := range m {
		if k != "name" {
			keys++
		}
	}
	if keys != 1 {
		return custom
	}
	if n, ok := m["clusterName"].(string); ok {
		switch {
		case ns == HostWorkspace && n == HostCluster:
			return Target{Kind: Host}
		case ns != workspace && ns != HostWorkspace:
			return Target{Kind: Cluster, Env: ns, Name: n}
		}
		return custom
	}
	if g, ok := m["clusterGroup"].(string); ok && ns == workspace {
		switch {
		case strings.HasPrefix(g, "template-"):
			return Target{Kind: Template, Name: strings.TrimPrefix(g, "template-")}
		case strings.HasPrefix(g, "env-"):
			return Target{Kind: Env, Name: strings.TrimPrefix(g, "env-")}
		case classGroups[g]:
			return Target{Kind: Class, Name: g}
		}
		return Target{Kind: Group, Name: g}
	}
	if s, ok := m["clusterSelector"].(map[string]any); ok {
		if len(s) == 0 && ns != workspace && ns != HostWorkspace {
			return Target{Kind: Env, Name: ns}
		}
		ml, _ := s["matchLabels"].(map[string]any)
		if len(s) != 1 || ml == nil || ns != workspace {
			return custom
		}
		if len(ml) == 1 && ml[clusters.LabelManagedBy] == clusters.ManagedBy {
			return Target{Kind: All}
		}
		env, _ := ml[LabelEnvironment].(string)
		name, _ := ml[LabelCluster].(string)
		if len(ml) == 2 && env != "" && name != "" {
			return Target{Kind: Cluster, Env: env, Name: name}
		}
	}
	return custom
}

// Rollout choices, as Fleet rolloutStrategies.
const (
	RolloutAll    = "all"
	RolloutSerial = "one-at-a-time"
	RolloutCanary = "canary"
	// RolloutCustom is a rolloutStrategy hyve doesn't model.
	RolloutCustom = "custom"
)

// RolloutStrategy returns Fleet's rolloutStrategy for a choice; nil for
// all at once (Fleet's default).
func RolloutStrategy(choice string) (map[string]any, error) {
	switch choice {
	case "", RolloutAll:
		return nil, nil
	case RolloutSerial:
		// Fleet updates a cluster only while fewer than maxUnavailable are
		// unavailable (updating counts): one at a time, and a cluster that
		// doesn't come back ready stops the rest.
		return map[string]any{"maxUnavailable": int64(1)}, nil
	case RolloutCanary:
		// The same, with the clusters in order: the canary partition's
		// first (Fleet works through partitions in order; a cluster in none
		// is never updated, hence "rest").
		return map[string]any{
			"maxUnavailable": int64(1),
			"partitions": []any{
				map[string]any{"name": "canary",
					"clusterSelector": map[string]any{"matchLabels": map[string]any{LabelCanary: "true"}}},
				map[string]any{"name": "rest",
					"clusterSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": LabelCanary, "operator": "DoesNotExist"}}}},
			},
		}, nil
	}
	return nil, fmt.Errorf("rollout %q: want all, one-at-a-time or canary", choice)
}

// RolloutOf names an object's rolloutStrategy.
func RolloutOf(o *unstructured.Unstructured) string {
	rs, ok, _ := unstructured.NestedMap(o.Object, "spec", "rolloutStrategy")
	if !ok || len(rs) == 0 {
		return RolloutAll
	}
	for _, c := range []string{RolloutSerial, RolloutCanary} {
		want, _ := RolloutStrategy(c)
		if reflect.DeepEqual(normalize(rs), normalize(want)) {
			return c
		}
	}
	return RolloutCustom
}

// normalize makes numbers comparable whether they came from JSON or Go.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return v
}

// Deployment is what the CLI's and UI's forms give: a source (a resources
// Entry: Fleet's HelmOp, Bundle or GitRepo spec, plus a namespace and the
// Secrets it needs), targets and a rollout.
type Deployment struct {
	Name string
	// Namespace is where it lives; empty picks Home.
	Namespace string
	Source    resources.Entry
	Targets   []Target
	Rollout   string
	Paused    bool
}

// Object builds a Deployment's Fleet object. existing, when the
// Deployment is being edited, keeps the fields hyve doesn't model.
func Object(d Deployment, workspace string, existing *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if err := resources.ValidName(d.Name); err != nil {
		return nil, err
	}
	src := d.Source
	src.Name = d.Name
	if src.Ref != "" {
		return nil, errors.New("a Deployment's source can't be a ref")
	}
	ns := d.Namespace
	if ns == "" {
		var err error
		if ns, err = Home(d.Targets, workspace); err != nil {
			return nil, err
		}
	}
	targets, err := FleetTargets(ns, workspace, d.Targets)
	if err != nil {
		return nil, err
	}
	u, err := resources.FleetObject(src, d.Name, ns, targets)
	if err != nil {
		return nil, err
	}
	if src.GitRepo == nil {
		rs, err := RolloutStrategy(d.Rollout)
		if err != nil {
			return nil, err
		}
		if rs != nil {
			u.Object["spec"].(map[string]any)["rolloutStrategy"] = rs
		}
	} else if d.Rollout != "" && d.Rollout != RolloutAll {
		return nil, errors.New("a git source's rollout is set in its fleet.yaml (rolloutStrategy)")
	}
	if d.Paused {
		u.Object["spec"].(map[string]any)["paused"] = true
	}
	if existing != nil {
		if existing.GetKind() != u.GetKind() {
			return nil, fmt.Errorf("%s is a %s; its source can't become a %s", d.Name, existing.GetKind(), u.GetKind())
		}
		keep(existing, u, d.Rollout)
	}
	return u, nil
}

// keep carries over what the form doesn't edit: labels, annotations and
// spec fields hyve doesn't model (targetCustomizations, diff, ...).
func keep(existing, u *unstructured.Unstructured, rollout string) {
	spec := u.Object["spec"].(map[string]any)
	old, _, _ := unstructured.NestedMap(existing.Object, "spec")
	modelled := map[string]bool{"helm": true, "resources": true, "repo": true, "branch": true, "revision": true, "paths": true,
		"targets": true, "rolloutStrategy": true, "paused": true, "defaultNamespace": true, "targetNamespace": true,
		"downstreamResources": true, "clientSecretName": true, "correctDrift": true}
	for k, v := range old {
		if _, set := spec[k]; !set && !modelled[k] {
			spec[k] = v
		}
	}
	if rollout == RolloutCustom {
		if rs, ok := old["rolloutStrategy"]; ok {
			spec["rolloutStrategy"] = rs
		}
	}
	// Custom targets come back as Raw, so they're already in targets.
	l := existing.GetLabels()
	for k, v := range u.GetLabels() {
		if l == nil {
			l = map[string]string{}
		}
		l[k] = v
	}
	u.SetLabels(l)
	u.SetAnnotations(existing.GetAnnotations())
}

// Item is a Deployment read back from Fleet.
type Item struct {
	Name, Namespace, Kind string
	Source                string
	Targets               []Target
	Rollout               string
	Paused                bool
	Ready, State          string
	Object                *unstructured.Unstructured
}

// Namespaces are where Deployments live: the template namespaces, the
// environments, and fleet-local.
func Namespaces(ctx context.Context, dc dynamic.Interface, workspace string) ([]string, error) {
	nsGVR := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	envs, err := dc.Resource(nsGVR).List(ctx, metav1.ListOptions{LabelSelector: LabelEnvironment})
	if err != nil {
		return nil, err
	}
	out := []string{workspace, HostWorkspace}
	for _, n := range envs.Items {
		out = append(out, n.GetName())
	}
	return out, nil
}

// List returns the Deployments in namespaces: every HelmOp, GitRepo and
// Bundle, except the Bundles Fleet makes for HelmOps and GitRepos.
func List(ctx context.Context, dc dynamic.Interface, workspace string, namespaces []string) ([]Item, error) {
	var items []Item
	for _, ns := range namespaces {
		classGroups := map[string]bool{}
		if ns == workspace {
			gs, err := dc.Resource(ClusterGroupGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
				return nil, err
			}
			if gs != nil {
				for _, g := range gs.Items {
					if IsClassGroup(&g) {
						classGroups[g.GetName()] = true
					}
				}
			}
		}
		for _, kind := range []string{"HelmOp", "GitRepo", "Bundle"} {
			list, err := dc.Resource(resources.FleetGVRs[kind]).Namespace(ns).List(ctx, metav1.ListOptions{})
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for i := range list.Items {
				o := &list.Items[i]
				if kind == "Bundle" && Generated(o) {
					continue
				}
				items = append(items, ItemOf(o, workspace, classGroups))
			}
		}
	}
	sort.Slice(items, func(a, b int) bool {
		return items[a].Namespace+"/"+items[a].Name < items[b].Namespace+"/"+items[b].Name
	})
	return items, nil
}

// Generated reports whether a Bundle is one Fleet made for a GitRepo or a
// HelmOp (or anything else that owns it), rather than a Deployment.
func Generated(b *unstructured.Unstructured) bool {
	l := b.GetLabels()
	if l["fleet.cattle.io/repo-name"] != "" || l["fleet.cattle.io/fleet-helm-name"] != "" {
		return true
	}
	return len(b.GetOwnerReferences()) > 0
}

// IsClassGroup reports whether a ClusterGroup is one CAAPF keeps for a
// ClusterClass.
func IsClassGroup(g *unstructured.Unstructured) bool {
	ml, _, _ := unstructured.NestedStringMap(g.Object, "spec", "selector", "matchLabels")
	return ml["clusterclass-name.fleet.addons.cluster.x-k8s.io"] != ""
}

// ItemOf reads one Fleet object as a Deployment.
func ItemOf(o *unstructured.Unstructured, workspace string, classGroups map[string]bool) Item {
	it := resources.Item{Object: o, Kind: o.GetKind()}
	ready, state := it.Ready()
	paused, _, _ := unstructured.NestedBool(o.Object, "spec", "paused")
	return Item{
		Name: o.GetName(), Namespace: o.GetNamespace(), Kind: o.GetKind(),
		Source:  Describe(o),
		Targets: Decode(o, workspace, classGroups),
		Rollout: RolloutOf(o), Paused: paused,
		Ready: ready, State: state, Object: o,
	}
}

// Describe summarises a Fleet object's source.
func Describe(o *unstructured.Unstructured) string {
	spec, _, _ := unstructured.NestedMap(o.Object, "spec")
	e := resources.Entry{}
	switch o.GetKind() {
	case "HelmOp":
		e.HelmOp = spec
	case "GitRepo":
		e.GitRepo = spec
	case "Bundle":
		e.Bundle = spec
		if h, _, _ := unstructured.NestedMap(spec, "helm"); h["chart"] != nil {
			e = resources.Entry{HelmOp: spec}
		}
	}
	return e.Describe()
}

// SourceOf reads a Fleet object back as a form's source.
func SourceOf(o *unstructured.Unstructured) resources.Entry {
	spec, _, _ := unstructured.NestedMap(o.Object, "spec")
	for _, k := range []string{"targets", "rolloutStrategy", "paused"} {
		delete(spec, k)
	}
	e := resources.Entry{Name: o.GetName()}
	switch o.GetKind() {
	case "HelmOp":
		e.HelmOp = spec
	case "GitRepo":
		e.GitRepo = spec
	case "Bundle":
		e.Bundle = spec
	}
	return e
}

// DescribeTargets is a one-line list of targets.
func DescribeTargets(ts []Target) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = t.String()
	}
	return strings.Join(s, ", ")
}

// Secrets returns the Secrets a Deployment has Fleet copy to its clusters
// (downstreamResources), which must exist in its namespace.
func Secrets(o *unstructured.Unstructured) []string {
	ds, _, _ := unstructured.NestedSlice(o.Object, "spec", "downstreamResources")
	var out []string
	for _, d := range ds {
		if m, ok := d.(map[string]any); ok && m["kind"] == "Secret" {
			if n, _ := m["name"].(string); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// ForTemplate returns the Deployments in items that reach every cluster
// of a template whose ClusterClass is class: those aimed at the template,
// its class, or all clusters.
func ForTemplate(items []Item, template, class string) []Item {
	var out []Item
	for _, it := range items {
		for _, t := range it.Targets {
			if (t.Kind == Template && t.Name == template) || (t.Kind == Class && t.Name == class) || t.Kind == All {
				out = append(out, it)
				break
			}
		}
	}
	return out
}
