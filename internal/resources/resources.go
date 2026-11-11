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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var (
	HelmOpGVR           = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "helmops"}
	GitRepoGVR          = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"}
	BundleGVR           = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"}
	BundleDeploymentGVR = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundledeployments"}

	// ResourceGVR and ClusterResourceGVR are hyve's own kinds.
	ResourceGVR        = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "resources"}
	ClusterResourceGVR = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "clusterresources"}
)

// GVRs of the Fleet kinds an entry can become, by kind.
var FleetGVRs = map[string]schema.GroupVersionResource{"HelmOp": HelmOpGVR, "GitRepo": GitRepoGVR, "Bundle": BundleGVR}

// Labels hyve puts on the objects it makes, and on Clusters.
const (
	// LabelResource is the entry's name.
	LabelResource = "hyve.io/resource"
	// LabelOwner names the ClusterTemplate or ClusterResource an object
	// came from.
	LabelOwner = "hyve.io/owner"
	// LabelTemplate and LabelTemplateNamespace on a Cluster (and, through
	// CAAPF, its Fleet cluster) name the template it was made from.
	LabelTemplate          = "hyve.io/template"
	LabelTemplateNamespace = "hyve.io/template-namespace"
)

// Owner kinds.
const (
	OwnerTemplate = "ClusterTemplate"
)

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

// FleetObject builds the Fleet HelmOp, Bundle or GitRepo name in namespace
// for a resolved entry (no ref), with the given Fleet targets. The entry's
// name is the Helm release's when the chart doesn't name one.
func FleetObject(e Entry, name, namespace string, targets []any) (*unstructured.Unstructured, error) {
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
	spec["targets"] = targets
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       spec,
	}}, nil
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
