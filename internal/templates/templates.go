// Package templates is hyve's ClusterTemplate: a ClusterClass plus
// defaults, locked variables, expiry and the resources every cluster made
// from it gets (HYVE-PROPOSAL.md, "Objects").
//
// A cluster is made from a template by creating a Cluster labelled
// hyve.io/template (and hyve.io/template-namespace when the template is in
// another namespace). Fill completes it from the template and its
// ClusterClass. hyve-controller's mutating webhook runs Fill on every such
// Cluster when it's created, so `kubectl`, GitOps, the UI and the CLI all
// take the same path (open question 1, option B). The CLI runs Fill too, so
// it can show what it's about to create.
package templates

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
)

// GVR is the ClusterTemplate resource.
var GVR = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "clustertemplates"}

const (
	// LabelTemplate on a Cluster names the template it's made from.
	LabelTemplate = resources.LabelTemplate
	// LabelTemplateNamespace on a Cluster is the template's namespace,
	// when it isn't the Cluster's own.
	LabelTemplateNamespace = resources.LabelTemplateNamespace
	// AnnotationExpiresAt on a Cluster: the RFC 3339 time hyve-controller
	// deletes it at, or "never".
	AnnotationExpiresAt = "hyve.io/expires-at"
	// Never is AnnotationExpiresAt's value for a cluster that doesn't
	// expire, whatever its template's schedule.
	Never = "never"
)

// Template is a ClusterTemplate.
type Template struct {
	Name, Namespace string
	Spec            Spec
}

// Spec is a ClusterTemplate's spec.
type Spec struct {
	Description     string            `json:"description,omitempty"`
	ClusterClassRef ClassRef          `json:"clusterClassRef"`
	Version         string            `json:"kubernetesVersion,omitempty"`
	ControlPlane    *Replicas         `json:"controlPlane,omitempty"`
	Workers         *Workers          `json:"workers,omitempty"`
	Variables       map[string]any    `json:"variables,omitempty"`
	LockedVariables []string          `json:"lockedVariables,omitempty"`
	Schedule        string            `json:"schedule,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Resources       []resources.Entry `json:"resources,omitempty"`
}

type ClassRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type Replicas struct {
	Replicas int64 `json:"replicas,omitempty"`
}

type Workers struct {
	Type  string `json:"type,omitempty"`
	Class string `json:"class,omitempty"`
	Count *int64 `json:"count,omitempty"`
}

// ClassNamespace is where the template's ClusterClass lives: the
// template's own namespace unless the ref says otherwise.
func (t *Template) ClassNamespace() string {
	if t.Spec.ClusterClassRef.Namespace != "" {
		return t.Spec.ClusterClassRef.Namespace
	}
	return t.Namespace
}

// FromUnstructured converts a ClusterTemplate object.
func FromUnstructured(u *unstructured.Unstructured) (*Template, error) {
	t := &Template{Name: u.GetName(), Namespace: u.GetNamespace()}
	raw, _, _ := unstructured.NestedMap(u.Object, "spec")
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &t.Spec); err != nil {
		return nil, fmt.Errorf("ClusterTemplate %s: %w", u.GetName(), err)
	}
	return t, nil
}

// ToUnstructured renders a ClusterTemplate object.
func (t *Template) ToUnstructured() (*unstructured.Unstructured, error) {
	spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&t.Spec)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hyve.io/v1alpha1",
		"kind":       "ClusterTemplate",
		"metadata":   map[string]any{"name": t.Name, "namespace": t.Namespace},
		"spec":       spec,
	}}, nil
}

// Ref returns the template a Cluster is labelled with: its name and
// namespace, or "" when it has none.
func Ref(c *unstructured.Unstructured) (name, namespace string) {
	l := c.GetLabels()
	name = l[LabelTemplate]
	namespace = l[LabelTemplateNamespace]
	if namespace == "" {
		namespace = c.GetNamespace()
	}
	return name, namespace
}

// Label labels a Cluster with a template.
func Label(c *unstructured.Unstructured, t *Template) {
	l := c.GetLabels()
	if l == nil {
		l = map[string]string{}
	}
	l[LabelTemplate] = t.Name
	l[LabelTemplateNamespace] = t.Namespace
	c.SetLabels(l)
}

// LockedError is returned when a cluster sets a locked variable to
// something other than the template's value.
type LockedError struct {
	Template string
	Names    []string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("ClusterTemplate %s locks %s: they must keep the template's values", e.Template, strings.Join(e.Names, ", "))
}

// Fill completes a Cluster labelled with t, as it's created: the class
// (and its namespace when it's another), version, control plane, workers,
// variables, labels and expiry. Whatever the Cluster already sets is kept,
// except that it can't use another class or change a locked variable.
// now is the creation time, for the template's schedule.
func Fill(c *unstructured.Unstructured, t *Template, class *unstructured.Unstructured, now time.Time) error {
	ref := t.Spec.ClusterClassRef.Name
	if have, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "classRef", "name"); have != "" && have != ref {
		return fmt.Errorf("ClusterTemplate %s builds from ClusterClass %s, not %s", t.Name, ref, have)
	}
	if err := unstructured.SetNestedField(c.Object, ref, "spec", "topology", "classRef", "name"); err != nil {
		return err
	}
	if ns := t.ClassNamespace(); ns != c.GetNamespace() {
		_ = unstructured.SetNestedField(c.Object, ns, "spec", "topology", "classRef", "namespace")
	}

	if v, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version"); v == "" {
		v = t.Spec.Version
		if v == "" {
			v = clusters.DefaultVersion(class)
		}
		if v == "" {
			return fmt.Errorf("neither ClusterTemplate %s nor ClusterClass %s sets a Kubernetes version (%s); give one", t.Name, ref, clusters.AnnotationDefaultVersion)
		}
		_ = unstructured.SetNestedField(c.Object, v, "spec", "topology", "version")
	}

	// Replicas only mean something for a machine-based control plane
	// (kubeadm); a managed one (GKE, EKS, AKS) has none.
	if _, found, _ := unstructured.NestedFieldNoCopy(c.Object, "spec", "topology", "controlPlane", "replicas"); !found && clusters.MachineControlPlane(class) {
		n := int64(1)
		if t.Spec.ControlPlane != nil && t.Spec.ControlPlane.Replicas > 0 {
			n = t.Spec.ControlPlane.Replicas
		}
		_ = unstructured.SetNestedField(c.Object, n, "spec", "topology", "controlPlane", "replicas")
	}

	if _, found, _ := unstructured.NestedFieldNoCopy(c.Object, "spec", "topology", "workers"); !found {
		typ, wc := clusters.DefaultWorkers(class)
		count := int64(1)
		if w := t.Spec.Workers; w != nil {
			if w.Type != "" {
				typ, wc = w.Type, clusters.FirstWorkerClass(class, w.Type)
			}
			if w.Class != "" {
				wc = w.Class
			}
			if w.Count != nil {
				count = *w.Count
			}
		}
		if wc != "" && count > 0 {
			if err := clusters.SetWorkers(c, typ, wc, count); err != nil {
				return err
			}
		}
	}

	vars := clusters.Variables(c)
	var locked []string
	for _, l := range t.Spec.LockedVariables {
		if l == LockVersion {
			if v, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version"); v != t.Spec.Version {
				locked = append(locked, l)
			}
			continue
		}
		want, ok := t.Spec.Variables[l]
		if !ok || want == nil {
			continue
		}
		if have, set := vars[l]; set && !JSONEqual(have, want) {
			locked = append(locked, l)
		}
	}
	if len(locked) > 0 {
		sort.Strings(locked)
		return &LockedError{Template: t.Name, Names: locked}
	}
	for k, v := range t.Spec.Variables {
		if _, set := vars[k]; !set && v != nil { // nil: no default, the class's own applies
			vars[k] = v
		}
	}
	if err := clusters.SetVariables(c, vars); err != nil {
		return err
	}

	if _, found, _ := unstructured.NestedFieldNoCopy(c.Object, "spec", "clusterNetwork"); !found {
		_ = unstructured.SetNestedField(c.Object, map[string]any{
			"pods":     map[string]any{"cidrBlocks": []any{"10.244.0.0/16"}},
			"services": map[string]any{"cidrBlocks": []any{"10.128.0.0/12"}},
		}, "spec", "clusterNetwork")
	}

	labels := c.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range clusters.ClassLabels(class) {
		if _, ok := labels[k]; !ok {
			labels[k] = v
		}
	}
	for k, v := range t.Spec.Labels {
		if _, ok := labels[k]; !ok {
			labels[k] = v
		}
	}
	labels[LabelTemplate] = t.Name
	labels[LabelTemplateNamespace] = t.Namespace
	if _, ok := labels[clusters.LabelManagedBy]; !ok {
		labels[clusters.LabelManagedBy] = clusters.ManagedBy
	}
	c.SetLabels(labels)

	ann := c.GetAnnotations()
	if ann[AnnotationExpiresAt] == "" && t.Spec.Schedule != "" {
		next, err := NextRun(t.Spec.Schedule, now)
		if err != nil {
			return err
		}
		if ann == nil {
			ann = map[string]string{}
		}
		ann[AnnotationExpiresAt] = next.UTC().Format(time.RFC3339)
		c.SetAnnotations(ann)
	}
	return nil
}

// CheckLocked returns the locked variables whose value on the Cluster
// differs from the template's.
func CheckLocked(t *Template, c *unstructured.Unstructured) []string {
	have := clusters.Variables(c)
	var bad []string
	for _, l := range t.Spec.LockedVariables {
		if l == LockVersion {
			if v, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version"); t.Spec.Version != "" && v != t.Spec.Version {
				bad = append(bad, l)
			}
			continue
		}
		want, ok := t.Spec.Variables[l]
		if !ok || want == nil {
			continue
		}
		if !JSONEqual(want, have[l]) {
			bad = append(bad, l)
		}
	}
	sort.Strings(bad)
	return bad
}

// ChangedVariables returns the names whose value differs between two
// versions of a Cluster.
func ChangedVariables(old, new *unstructured.Unstructured, names []string) []string {
	ov, nv := clusters.Variables(old), clusters.Variables(new)
	var out []string
	for _, n := range names {
		if n == LockVersion {
			o, _, _ := unstructured.NestedString(old.Object, "spec", "topology", "version")
			v, _, _ := unstructured.NestedString(new.Object, "spec", "topology", "version")
			if o != v {
				out = append(out, n)
			}
			continue
		}
		if !JSONEqual(ov[n], nv[n]) {
			out = append(out, n)
		}
	}
	return out
}

// JSONEqual compares two values as JSON (so 3 == 3.0, int64 == float64).
func JSONEqual(a, b any) bool {
	ra, err1 := json.Marshal(a)
	rb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	var na, nb any
	_ = json.Unmarshal(ra, &na)
	_ = json.Unmarshal(rb, &nb)
	return reflect.DeepEqual(na, nb)
}

// ExpiresAt reads a Cluster's expiry: the time, and whether it has one.
func ExpiresAt(c *unstructured.Unstructured) (time.Time, bool, error) {
	v := c.GetAnnotations()[AnnotationExpiresAt]
	if v == "" || v == Never {
		return time.Time{}, false, nil
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s %q: %w", AnnotationExpiresAt, v, err)
	}
	return at, true, nil
}

// NextRun returns the schedule's next occurrence after now. Schedules are
// standard 5-field cron in UTC, or in the zone a CRON_TZ=<zone> prefix
// names. UTC whatever now's zone, so the CLI (in the user's zone) and
// hyve-controller (in UTC) agree.
func NextRun(schedule string, now time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("schedule %q: %w", schedule, err)
	}
	return sched.Next(now.UTC()), nil
}

// Validate checks a template for mistakes the CRD schema can't catch.
func Validate(t *Template) error {
	if t.Spec.ClusterClassRef.Name == "" {
		return fmt.Errorf("clusterClassRef.name is required")
	}
	if t.Spec.Schedule != "" {
		if _, err := NextRun(t.Spec.Schedule, time.Now()); err != nil {
			return err
		}
	}
	if w := t.Spec.Workers; w != nil && w.Type != "" && w.Type != clusters.MachineDeployment && w.Type != clusters.MachinePool {
		return fmt.Errorf("workers.type %q: want %s or %s", w.Type, clusters.MachineDeployment, clusters.MachinePool)
	}
	seen := map[string]bool{}
	for i := range t.Spec.Resources {
		r := &t.Spec.Resources[i]
		if err := resources.ValidName(r.Name); err != nil {
			return err
		}
		if err := r.Validate(true); err != nil {
			return err
		}
		if seen[r.Name] {
			return fmt.Errorf("resource %q listed twice", r.Name)
		}
		seen[r.Name] = true
	}
	for _, l := range t.Spec.LockedVariables {
		if l == LockVersion {
			if t.Spec.Version == "" {
				return fmt.Errorf("%s is locked, but kubernetesVersion isn't set", LockVersion)
			}
			continue
		}
		if _, ok := t.Spec.Variables[l]; !ok {
			return fmt.Errorf("locked variable %q has no value in variables", l)
		}
	}
	return nil
}

// LockVersion in lockedVariables locks the Kubernetes version to the
// template's kubernetesVersion, as a variable is locked: clusters made from
// the template can't be upgraded (or created) at another version.
const LockVersion = "kubernetesVersion"

// IsLocked reports whether a template locks a variable.
func (t *Template) IsLocked(name string) bool {
	return slices.Contains(t.Spec.LockedVariables, name)
}

// ScaffoldVariables returns a ClusterClass's variables with their schema
// defaults (nil when a variable has none), for `template create
// --from-clusterclass`.
func ScaffoldVariables(class *unstructured.Unstructured) map[string]any {
	out := map[string]any{}
	vars, _, _ := unstructured.NestedSlice(class.Object, "spec", "variables")
	for _, v := range vars {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		def, _, _ := unstructured.NestedFieldCopy(m, "schema", "openAPIV3Schema", "default")
		out[name] = def
	}
	return out
}
