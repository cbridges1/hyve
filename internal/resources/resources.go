// Package resources turns what hyve puts on clusters into Fleet objects
// (HYVE-PROPOSAL.md, "Resources: owned by templates").
//
// An Entry is one item of a ClusterTemplate's resources, the spec of a
// per-cluster ClusterResource, or the spec of a shared Resource. It embeds
// Fleet's own HelmOp, Bundle or GitRepo spec, minus targets, so hyve has no
// resource format of its own. hyve-controller makes one Fleet object per
// entry:
//
//   - a template's entries in the template's namespace, targeted at the
//     hyve.io/template labels CAAPF copies onto each Fleet cluster; CAAPF's
//     BundleNamespaceMapping carries them to clusters in other namespaces;
//   - a ClusterResource in the cluster's namespace, targeted at that one
//     cluster by name (CAAPF names the Fleet cluster after the CAPI one).
//
// Each Fleet object is owned by the template or ClusterResource it came
// from, so deleting either removes it, and Fleet then removes what it
// deployed.
package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

var (
	HelmOpGVR           = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "helmops"}
	GitRepoGVR          = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"}
	BundleGVR           = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"}
	BundleDeploymentGVR = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundledeployments"}
	fleetClusterGVR     = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "clusters"}

	// ResourceGVR and ClusterResourceGVR are hyve's own kinds.
	ResourceGVR        = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "resources"}
	ClusterResourceGVR = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "clusterresources"}
)

// GVRs of the Fleet kinds an entry can become, by kind.
var FleetGVRs = map[string]schema.GroupVersionResource{"HelmOp": HelmOpGVR, "GitRepo": GitRepoGVR, "Bundle": BundleGVR}

// Labels hyve puts on the objects it makes, and on Clusters.
const (
	labelManagedBy = "app.kubernetes.io/managed-by"
	managedBy      = "hyve"
	// LabelResource is the entry's name.
	LabelResource = "hyve.io/resource"
	// LabelOwnerKind and LabelOwner name the ClusterTemplate or
	// ClusterResource an object came from.
	LabelOwnerKind = "hyve.io/owner-kind"
	LabelOwner     = "hyve.io/owner"
	// LabelCluster on a ClusterResource names its cluster, so they can be
	// listed per cluster.
	LabelCluster = "hyve.io/cluster"
	// LabelTemplate and LabelTemplateNamespace on a Cluster (and, through
	// CAAPF, its Fleet cluster) name the template it was made from.
	LabelTemplate          = "hyve.io/template"
	LabelTemplateNamespace = "hyve.io/template-namespace"

	annotSource = "hyve.io/source"
)

// Owner kinds.
const (
	OwnerTemplate        = "ClusterTemplate"
	OwnerClusterResource = "ClusterResource"
)

// ClusterIndexedLabel is Rancher's marker for objects that belong to one
// cluster; Rancher's (and hyve's) cluster role bindings cover objects
// carrying it.
const ClusterIndexedLabel = "auth.cattle.io/cluster-indexed"

// Entry is one resource. Exactly one of HelmOp, Bundle, GitRepo or Ref is
// set.
type Entry struct {
	// Name is the entry's name within a template. A ClusterResource's and a
	// Resource's is their metadata.name.
	Name string `json:"name,omitempty"`
	// Namespace on the cluster that the resource's objects go in, unless its
	// Fleet spec says otherwise (defaultNamespace, targetNamespace).
	Namespace string `json:"namespace,omitempty"`
	// Secrets in the owner's namespace on the management cluster that
	// Fleet copies to each target cluster (downstreamResources). hyve only
	// names them; how they're filled is up to the installer.
	Secrets []string `json:"secrets,omitempty"`

	// Ref names a shared Resource; Values are merged over its Helm values.
	Ref          string         `json:"ref,omitempty"`
	RefNamespace string         `json:"refNamespace,omitempty"`
	Values       map[string]any `json:"values,omitempty"`

	// Fleet's HelmOp, Bundle or GitRepo spec, without targets.
	HelmOp  map[string]any `json:"helmOp,omitempty"`
	Bundle  map[string]any `json:"bundle,omitempty"`
	GitRepo map[string]any `json:"gitRepo,omitempty"`
}

// Kind returns the Fleet kind the entry becomes, or "Ref".
func (e *Entry) Kind() string {
	switch {
	case e.HelmOp != nil:
		return "HelmOp"
	case e.Bundle != nil:
		return "Bundle"
	case e.GitRepo != nil:
		return "GitRepo"
	case e.Ref != "":
		return "Ref"
	}
	return ""
}

// Describe is a one-line summary of where the entry comes from.
func (e *Entry) Describe() string {
	switch {
	case e.Ref != "":
		return "ref " + e.Ref
	case e.HelmOp != nil:
		h, _, _ := unstructured.NestedMap(e.HelmOp, "helm")
		d := fmt.Sprint(h["chart"])
		if r, _ := h["repo"].(string); r != "" {
			if h["chart"] == nil || h["chart"] == "" {
				d = r
			} else {
				d = r + " " + d
			}
		}
		if v, _ := h["version"].(string); v != "" {
			d += "@" + v
		}
		return d
	case e.GitRepo != nil:
		d := fmt.Sprint(e.GitRepo["repo"])
		if b, _ := e.GitRepo["branch"].(string); b != "" {
			d += "@" + b
		}
		if ps, _ := e.GitRepo["paths"].([]any); len(ps) > 0 {
			var s []string
			for _, p := range ps {
				s = append(s, fmt.Sprint(p))
			}
			d += " " + strings.Join(s, ",")
		}
		return d
	case e.Bundle != nil:
		rs, _ := e.Bundle["resources"].([]any)
		return fmt.Sprintf("%d manifests", len(rs))
	}
	return ""
}

var nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ValidName checks a resource name.
func ValidName(name string) error {
	if !nameRE.MatchString(name) || len(name) > 40 {
		return fmt.Errorf("resource name %q: want a DNS label of at most 40 characters", name)
	}
	return nil
}

// Validate checks an entry. Shared Resources can't themselves be refs.
func (e *Entry) Validate(allowRef bool) error {
	n := 0
	for _, set := range []bool{e.HelmOp != nil, e.Bundle != nil, e.GitRepo != nil, e.Ref != ""} {
		if set {
			n++
		}
	}
	name := e.Name
	if name == "" {
		name = "(unnamed)"
	}
	if n != 1 {
		return fmt.Errorf("resource %s: want exactly one of helmOp, bundle, gitRepo or ref", name)
	}
	if e.Ref != "" && !allowRef {
		return fmt.Errorf("resource %s: a shared Resource can't be a ref", name)
	}
	if e.Values != nil && e.Ref == "" {
		return fmt.Errorf("resource %s: values only go with ref (put Helm values in helmOp.helm.values)", name)
	}
	if len(e.Secrets) > 0 && e.GitRepo != nil {
		return fmt.Errorf("resource %s: a gitRepo can't copy secrets; list them in its fleet.yaml's downstreamResources", name)
	}
	for _, spec := range []map[string]any{e.HelmOp, e.Bundle, e.GitRepo} {
		if _, ok := spec["targets"]; ok {
			return fmt.Errorf("resource %s: leave out targets; hyve targets the template's or cluster's clusters", name)
		}
	}
	if e.HelmOp != nil {
		h, _, _ := unstructured.NestedMap(e.HelmOp, "helm")
		chart, _ := h["chart"].(string)
		repo, _ := h["repo"].(string)
		if chart == "" && !strings.HasPrefix(repo, "oci://") {
			return fmt.Errorf("resource %s: helmOp.helm.chart is required (or an oci:// repo)", name)
		}
	}
	if e.GitRepo != nil {
		if r, _ := e.GitRepo["repo"].(string); r == "" {
			return fmt.Errorf("resource %s: gitRepo.repo is required", name)
		}
	}
	if e.Bundle != nil {
		if rs, _ := e.Bundle["resources"].([]any); len(rs) == 0 {
			return fmt.Errorf("resource %s: bundle.resources is empty", name)
		}
	}
	return nil
}

// Lookup returns the shared Resource ns/name's entry.
type Lookup func(ctx context.Context, namespace, name string) (*Entry, error)

// Resolve replaces a ref with the shared Resource it names, merging Values
// over its Helm values and its secrets with the entry's. defaultRefNS is
// where a ref without a refNamespace is looked up.
func Resolve(ctx context.Context, e Entry, defaultRefNS string, lookup Lookup) (Entry, error) {
	if e.Ref == "" {
		return deepCopy(e), nil
	}
	ns := e.RefNamespace
	if ns == "" {
		ns = defaultRefNS
	}
	shared, err := lookup(ctx, ns, e.Ref)
	if err != nil {
		return Entry{}, fmt.Errorf("resource %s: ref %s/%s: %w", e.Name, ns, e.Ref, err)
	}
	out := deepCopy(*shared)
	out.Name = e.Name
	if e.Namespace != "" {
		out.Namespace = e.Namespace
	}
	out.Secrets = union(out.Secrets, e.Secrets)
	if len(e.Values) > 0 {
		if out.HelmOp == nil {
			return Entry{}, fmt.Errorf("resource %s: values given, but Resource %s isn't a helmOp", e.Name, e.Ref)
		}
		vals, _, _ := unstructured.NestedMap(out.HelmOp, "helm", "values")
		if vals == nil {
			vals = map[string]any{}
		}
		MergeValues(vals, deepCopyMap(e.Values))
		if err := unstructured.SetNestedMap(out.HelmOp, vals, "helm", "values"); err != nil {
			return Entry{}, err
		}
	}
	return out, nil
}

// MergeValues merges src into dst, recursing into maps; src wins.
func MergeValues(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				MergeValues(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func deepCopy(e Entry) Entry {
	raw, _ := json.Marshal(e)
	var out Entry
	_ = json.Unmarshal(raw, &out)
	return out
}

func deepCopyMap(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// Owner is the object a Fleet object comes from.
type Owner struct {
	Kind, Name, Namespace string
	UID                   types.UID
	// ClusterName is the target of a ClusterResource.
	ClusterName string
}

// ObjectName is the Fleet object's name for an entry: "tpl-<template>-<name>"
// for a template's, or the ClusterResource's own name; shortened with a
// hash when longer than Fleet allows.
func ObjectName(o Owner, entry string) string {
	n := o.Name
	if o.Kind == OwnerTemplate {
		n = "tpl-" + o.Name + "-" + entry
	}
	if len(n) <= 50 {
		return n
	}
	sum := sha256.Sum256([]byte(n))
	return strings.TrimRight(n[:41], "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// Targets returns the Fleet targets for an owner: the template's clusters
// by label, or one cluster by name.
func Targets(o Owner) []any {
	if o.Kind == OwnerTemplate {
		return []any{map[string]any{"clusterSelector": map[string]any{"matchLabels": map[string]any{
			LabelTemplate: o.Name, LabelTemplateNamespace: o.Namespace,
		}}}}
	}
	return []any{map[string]any{"clusterName": o.ClusterName}}
}

// Object builds the Fleet object for a resolved entry (no ref).
func Object(e Entry, o Owner) (*unstructured.Unstructured, error) {
	if err := e.Validate(false); err != nil {
		return nil, err
	}
	kind := e.Kind()
	var spec map[string]any
	switch kind {
	case "HelmOp":
		spec = deepCopyMap(e.HelmOp)
		if rn, _, _ := unstructured.NestedString(spec, "helm", "releaseName"); rn == "" {
			_ = unstructured.SetNestedField(spec, e.Name, "helm", "releaseName")
		}
	case "Bundle":
		spec = deepCopyMap(e.Bundle)
	case "GitRepo":
		spec = deepCopyMap(e.GitRepo)
	}
	if e.Namespace != "" {
		key := "defaultNamespace"
		if kind == "GitRepo" {
			key = "targetNamespace"
		}
		if _, ok := spec[key]; !ok {
			spec[key] = e.Namespace
		}
	}
	if len(e.Secrets) > 0 {
		ds, _ := spec["downstreamResources"].([]any)
		have := map[string]bool{}
		for _, d := range ds {
			if m, ok := d.(map[string]any); ok && m["kind"] == "Secret" {
				have[fmt.Sprint(m["name"])] = true
			}
		}
		for _, s := range e.Secrets {
			if !have[s] {
				ds = append(ds, map[string]any{"kind": "Secret", "name": s})
			}
		}
		spec["downstreamResources"] = ds
	}
	spec["targets"] = Targets(o)
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1",
		"kind":       kind,
		"metadata":   map[string]any{"name": ObjectName(o, e.Name), "namespace": o.Namespace},
		"spec":       spec,
	}}
	u.SetLabels(map[string]string{
		labelManagedBy: managedBy,
		LabelResource:  e.Name,
		LabelOwnerKind: o.Kind,
		LabelOwner:     o.Name,
	})
	u.SetAnnotations(map[string]string{annotSource: e.Describe()})
	if o.UID != "" {
		t := true
		u.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: "hyve.io/v1alpha1", Kind: o.Kind, Name: o.Name, UID: o.UID,
			Controller: &t, BlockOwnerDeletion: &t,
		}})
	}
	return u, nil
}

// ReadBundle reads the manifests under path (a file or a directory) as
// Fleet bundle resources, for `--path` on the CLI.
func ReadBundle(path string) ([]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var out []any
	add := func(p, rel string) error {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, map[string]any{"name": filepath.ToSlash(rel), "content": string(raw)})
		return nil
	}
	if !info.IsDir() {
		return out, add(path, filepath.Base(path))
	}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != path {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml", ".json":
			rel, _ := filepath.Rel(path, p)
			return add(p, rel)
		}
		return nil
	})
	if err == nil && len(out) == 0 {
		err = fmt.Errorf("%s: no .yaml, .yml or .json files", path)
	}
	return out, err
}

// EntryFromSpec reads a Resource's or ClusterResource's spec.
func EntryFromSpec(u *unstructured.Unstructured) (Entry, error) {
	var e Entry
	raw, _, _ := unstructured.NestedMap(u.Object, "spec")
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &e); err != nil {
		return e, fmt.Errorf("%s %s: %w", u.GetKind(), u.GetName(), err)
	}
	e.Name = u.GetName()
	return e, nil
}

// SharedLookup looks shared Resources up with a dynamic client.
func SharedLookup(dc dynamic.Interface) Lookup {
	return func(ctx context.Context, ns, name string) (*Entry, error) {
		u, err := dc.Resource(ResourceGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		e, err := EntryFromSpec(u)
		return &e, err
	}
}

// Item is a hyve-made Fleet object, read back.
type Item struct {
	Name, Kind, Source string
	OwnerKind, Owner   string
	Object             *unstructured.Unstructured
}

// Ready returns "ready/total" deployments and Fleet's state.
func (i *Item) Ready() (string, string) {
	d, _, _ := unstructured.NestedStringMap(i.Object.Object, "status", "display")
	ready := d["readyBundleDeployments"]
	if ready == "" {
		ready = d["readyClusters"]
	}
	if ready == "" {
		if s, ok, _ := unstructured.NestedFieldNoCopy(i.Object.Object, "status", "summary"); ok {
			if m, ok := s.(map[string]any); ok {
				ready = fmt.Sprintf("%v/%v", m["ready"], m["desiredReady"])
			}
		}
	}
	state := d["state"]
	if msg := Problem(i.Object); msg != "" {
		state = msg
	}
	if state == "" && strings.HasPrefix(ready, "0/0") {
		state = "NoTargets"
	}
	return ready, state
}

// Problem returns a Fleet object's error, a False condition's message, or
// "" when it has none.
func Problem(u *unstructured.Unstructured) string {
	if e, _, _ := unstructured.NestedString(u.Object, "status", "display", "message"); e != "" {
		if isErr, _, _ := unstructured.NestedBool(u.Object, "status", "display", "error"); isErr {
			return "Error: " + e
		}
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		// Ready=False is progress (a rollout), shown by the state; the
		// others (Accepted, Stalled, ...) are real errors.
		if ok && m["status"] == "False" && m["type"] != "Ready" && m["message"] != nil && m["message"] != "" {
			return fmt.Sprintf("%v: %v", m["type"], m["message"])
		}
	}
	return ""
}

// Selector picks hyve's Fleet objects.
type Selector struct {
	// Namespace to list in; "" is every namespace.
	Namespace string
	// OwnerKind and Owner narrow it to one template's or ClusterResource's.
	OwnerKind, Owner string
}

// List returns hyve's Fleet objects.
func List(ctx context.Context, dc dynamic.Interface, s Selector) ([]Item, error) {
	sel := labelManagedBy + "=" + managedBy + "," + LabelResource
	if s.OwnerKind != "" {
		sel += "," + LabelOwnerKind + "=" + s.OwnerKind
	}
	if s.Owner != "" {
		sel += "," + LabelOwner + "=" + s.Owner
	}
	var items []Item
	for _, kind := range []string{"HelmOp", "GitRepo", "Bundle"} {
		gvr := FleetGVRs[kind]
		list, err := dc.Resource(gvr).Namespace(s.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if apierrors.IsNotFound(err) {
			continue // that Fleet kind isn't installed
		}
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			o := &list.Items[i]
			l := o.GetLabels()
			// HelmOps and GitRepos make Bundles of their own; skip those.
			if kind == "Bundle" && (l["fleet.cattle.io/repo-name"] != "" || hasNonHyveOwner(o)) {
				continue
			}
			items = append(items, Item{
				Name: l[LabelResource], Kind: o.GetKind(), Source: o.GetAnnotations()[annotSource],
				OwnerKind: l[LabelOwnerKind], Owner: l[LabelOwner], Object: o,
			})
		}
	}
	sort.Slice(items, func(a, b int) bool {
		ka := items[a].Object.GetNamespace() + "/" + items[a].OwnerKind + "/" + items[a].Owner + "/" + items[a].Name
		kb := items[b].Object.GetNamespace() + "/" + items[b].OwnerKind + "/" + items[b].Owner + "/" + items[b].Name
		return ka < kb
	})
	return items, nil
}

func hasNonHyveOwner(o *unstructured.Unstructured) bool {
	for _, r := range o.GetOwnerReferences() {
		if !strings.HasPrefix(r.APIVersion, "hyve.io/") {
			return true
		}
	}
	return false
}

// ClusterStates maps bundle name to BundleDeployment state on the Fleet
// cluster ns/name, plus "repo:<name>" for a GitRepo's bundles (the worst of
// them). nil when the cluster isn't registered with Fleet yet.
func ClusterStates(ctx context.Context, dc dynamic.Interface, ns, name string) (map[string]string, error) {
	fc, err := dc.Resource(fleetClusterGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	bdNS, _, _ := unstructured.NestedString(fc.Object, "status", "namespace")
	if bdNS == "" {
		return nil, errors.New("the Fleet cluster has no namespace yet")
	}
	list, err := dc.Resource(BundleDeploymentGVR).Namespace(bdNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, bd := range list.Items {
		l := bd.GetLabels()
		state, _, _ := unstructured.NestedString(bd.Object, "status", "display", "state")
		if state == "" {
			if ready, _, _ := unstructured.NestedBool(bd.Object, "status", "ready"); ready {
				state = "Ready"
			} else {
				state = "Pending"
			}
		}
		key := l["fleet.cattle.io/bundle-namespace"] + "/" + l["fleet.cattle.io/bundle-name"]
		out[key] = state
		if repo := l["fleet.cattle.io/repo-name"]; repo != "" {
			k := "repo:" + l["fleet.cattle.io/bundle-namespace"] + "/" + repo
			out[k] = worst(out[k], state)
		}
	}
	return out, nil
}

// StateOn returns an item's deployment state on one cluster, from
// ClusterStates.
func (i *Item) StateOn(states map[string]string) string {
	if msg := Problem(i.Object); msg != "" {
		return msg
	}
	if states == nil {
		return ""
	}
	key := i.Object.GetNamespace() + "/" + i.Object.GetName()
	if i.Kind == "GitRepo" {
		return states["repo:"+key]
	}
	return states[key]
}

func worst(a, b string) string {
	if a == "" || a == "Ready" {
		return b
	}
	return a
}
