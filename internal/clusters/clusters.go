// Package clusters builds and reads Cluster API topology Clusters, and the
// Fleet clusters CAAPF registers them as (HYVE-PROPOSAL.md, "Objects").
package clusters

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	ClusterGVR        = schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta2", Resource: "clusters"}
	ClusterClassGVR   = schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta2", Resource: "clusterclasses"}
	FleetClusterGVR   = schema.GroupVersionResource{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "clusters"}
	RancherClusterGVR = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}
)

const (
	// LabelManagedBy marks objects hyve created.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedBy is LabelManagedBy's value.
	ManagedBy = "hyve"
	// LabelAutoImport asks Rancher Turtles to import the cluster into
	// Rancher. Harmless without Rancher.
	LabelAutoImport = "cluster-api.cattle.io/rancher-auto-import"

	// AnnotationClusterLabels on a ClusterClass: "k=v,k2=v2" labels every
	// Cluster of the class gets (e.g. the one a CNI ClusterResourceSet
	// selects on).
	AnnotationClusterLabels = "hyve.io/cluster-labels"
	// AnnotationDefaultVersion on a ClusterClass: the Kubernetes version
	// used when none is given.
	AnnotationDefaultVersion = "hyve.io/default-version"

	// herd's names for the two annotations above, still read so
	// ClusterClasses written for herd keep working.
	legacyClusterLabels  = "herd.io/cluster-labels"
	legacyDefaultVersion = "herd.io/default-version"

	// Turtles' labels on the Rancher cluster naming its CAPI cluster.
	turtlesOwner   = "cluster-api.cattle.io/capi-cluster-owner"
	turtlesOwnerNS = "cluster-api.cattle.io/capi-cluster-owner-ns"
)

// Worker types.
const (
	MachineDeployment = "machineDeployment"
	MachinePool       = "machinePool"
)

// DefaultVersion is the Kubernetes version a ClusterClass's annotation
// suggests, or "".
func DefaultVersion(class *unstructured.Unstructured) string {
	ann := class.GetAnnotations()
	if v := ann[AnnotationDefaultVersion]; v != "" {
		return v
	}
	return ann[legacyDefaultVersion]
}

// ClassLabels are the labels a ClusterClass's annotation gives each of its
// Clusters.
func ClassLabels(class *unstructured.Unstructured) map[string]string {
	ann := class.GetAnnotations()
	raw := ann[AnnotationClusterLabels]
	if raw == "" {
		raw = ann[legacyClusterLabels]
	}
	out := map[string]string{}
	for _, kv := range strings.Split(raw, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k != "" {
			out[k] = v
		}
	}
	return out
}

// DefaultWorkers returns the worker type and class a ClusterClass offers
// first: machineDeployment unless it only has machine pools.
func DefaultWorkers(class *unstructured.Unstructured) (typ, workerClass string) {
	typ = MachineDeployment
	if pools, _, _ := unstructured.NestedSlice(class.Object, "spec", "workers", "machinePools"); len(pools) > 0 {
		if mds, _, _ := unstructured.NestedSlice(class.Object, "spec", "workers", "machineDeployments"); len(mds) == 0 {
			typ = MachinePool
		}
	}
	return typ, FirstWorkerClass(class, typ)
}

// FirstWorkerClass is the class of a ClusterClass's first worker of typ.
func FirstWorkerClass(class *unstructured.Unstructured, typ string) string {
	ws, _, _ := unstructured.NestedSlice(class.Object, "spec", "workers", typ+"s")
	if len(ws) > 0 {
		if m, ok := ws[0].(map[string]any); ok {
			c, _ := m["class"].(string)
			return c
		}
	}
	return ""
}

// MachineControlPlane reports whether a ClusterClass's control plane runs
// on machines (has a machineInfrastructure template), i.e. takes replicas.
// A managed one (GKE, EKS, AKS) has none.
func MachineControlPlane(class *unstructured.Unstructured) bool {
	_, found, _ := unstructured.NestedMap(class.Object, "spec", "controlPlane", "machineInfrastructure")
	return found
}

// WorkersName is the topology name hyve gives its one worker group.
func WorkersName(typ string) string {
	if typ == MachinePool {
		return "mp-0"
	}
	return "md-0"
}

// SetWorkers sets the topology's single worker group.
func SetWorkers(c *unstructured.Unstructured, typ, class string, replicas int64) error {
	if typ != MachineDeployment && typ != MachinePool {
		return fmt.Errorf("worker type %q: want %s or %s", typ, MachineDeployment, MachinePool)
	}
	return unstructured.SetNestedField(c.Object, map[string]any{typ + "s": []any{
		map[string]any{"class": class, "name": WorkersName(typ), "replicas": replicas},
	}}, "spec", "topology", "workers")
}

// Variables returns a Cluster's topology variables by name.
func Variables(c *unstructured.Unstructured) map[string]any {
	out := map[string]any{}
	vars, _, _ := unstructured.NestedSlice(c.Object, "spec", "topology", "variables")
	for _, v := range vars {
		if m, ok := v.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				out[n] = m["value"]
			}
		}
	}
	return out
}

// SetVariables replaces a Cluster's topology variables, sorted by name.
func SetVariables(c *unstructured.Unstructured, vars map[string]any) error {
	if len(vars) == 0 {
		unstructured.RemoveNestedField(c.Object, "spec", "topology", "variables")
		return nil
	}
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "value": vars[n]})
	}
	return unstructured.SetNestedSlice(c.Object, out, "spec", "topology", "variables")
}

// New returns a bare topology Cluster: a name, a namespace and a class.
// Everything else is filled in from the class and the template (see
// templates.Fill).
func New(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta2",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       map[string]any{"topology": map[string]any{}},
	}}
}

// Phase maps a Cluster's conditions to CREATING / UPDATING / ACTIVE /
// DELETING / FAILED.
func Phase(c *unstructured.Unstructured) string {
	cond := map[string]string{}
	conds, _, _ := unstructured.NestedSlice(c.Object, "status", "conditions")
	for _, x := range conds {
		if m, ok := x.(map[string]any); ok {
			t, _ := m["type"].(string)
			s, _ := m["status"].(string)
			cond[t] = s
		}
	}
	phase, _, _ := unstructured.NestedString(c.Object, "status", "phase")
	observed, found, _ := unstructured.NestedInt64(c.Object, "status", "observedGeneration")
	inFlight := (found && observed != c.GetGeneration()) ||
		cond["TopologyReconciled"] == "False" || cond["RollingOut"] == "True" ||
		cond["ScalingUp"] == "True" || cond["ScalingDown"] == "True"
	switch {
	case c.GetDeletionTimestamp() != nil:
		return "DELETING"
	case phase == "Failed":
		return "FAILED"
	case inFlight && cond["ControlPlaneAvailable"] == "True":
		return "UPDATING"
	case cond["Available"] == "True" && !inFlight:
		return "ACTIVE"
	default:
		return "CREATING"
	}
}

// Conditions returns "Type=Status reason: message" lines for display.
func Conditions(u *unstructured.Unstructured) []string {
	var out []string
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, x := range conds {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		line := fmt.Sprintf("%v=%v", m["type"], m["status"])
		if r, _ := m["reason"].(string); r != "" {
			line += " " + r
		}
		if msg, _ := m["message"].(string); msg != "" {
			line += ": " + strings.ReplaceAll(msg, "\n", " ")
		}
		out = append(out, line)
	}
	return out
}

// ConditionStatus returns a condition's status ("" when absent).
func ConditionStatus(u *unstructured.Unstructured, typ string) string {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, x := range conds {
		if m, ok := x.(map[string]any); ok && m["type"] == typ {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

// FleetCluster returns the Fleet cluster CAAPF registered for the CAPI
// cluster ns/name (same name and namespace), or nil when there isn't one
// (yet).
func FleetCluster(ctx context.Context, dc dynamic.Interface, ns, name string) (*unstructured.Unstructured, error) {
	fc, err := dc.Resource(FleetClusterGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return fc, err
}

// FleetReady reports whether a Fleet cluster's agent has every bundle
// aimed at it ready.
func FleetReady(fc *unstructured.Unstructured) bool {
	if fc == nil {
		return false
	}
	ready, _, _ := unstructured.NestedString(fc.Object, "status", "display", "readyBundles")
	r, t, ok := strings.Cut(ready, "/")
	return ok && r == t && t != "0"
}

// RancherCluster finds the Rancher cluster Turtles imported the CAPI
// cluster ns/name as; nil when it hasn't been imported, or Rancher isn't
// installed.
func RancherCluster(ctx context.Context, dc dynamic.Interface, ns, name string) (*unstructured.Unstructured, error) {
	list, err := dc.Resource(RancherClusterGVR).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", turtlesOwner, name, turtlesOwnerNS, ns),
	})
	if apierrors.IsNotFound(err) {
		return nil, nil // no Rancher
	}
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}
	return nil, nil
}

// RancherState summarises a Rancher cluster: "", "pending", "connected".
func RancherState(rc *unstructured.Unstructured) string {
	switch {
	case rc == nil:
		return ""
	case ConditionStatus(rc, "Ready") == "True" && ConditionStatus(rc, "Connected") == "True":
		return "connected"
	default:
		return "pending"
	}
}

// Age formats the time since t like kubectl does.
func Age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FillDefaults completes a Cluster from its ClusterClass alone, for
// clusters made without a template: version, control plane, workers,
// network and the class's labels. Whatever the Cluster sets is kept.
func FillDefaults(c, class *unstructured.Unstructured, autoImport bool) error {
	if v, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version"); v == "" {
		v = DefaultVersion(class)
		if v == "" {
			return fmt.Errorf("ClusterClass %s sets no %s annotation; give a version", class.GetName(), AnnotationDefaultVersion)
		}
		_ = unstructured.SetNestedField(c.Object, v, "spec", "topology", "version")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(c.Object, "spec", "topology", "controlPlane", "replicas"); !found && MachineControlPlane(class) {
		_ = unstructured.SetNestedField(c.Object, int64(1), "spec", "topology", "controlPlane", "replicas")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(c.Object, "spec", "topology", "workers"); !found {
		if typ, wc := DefaultWorkers(class); wc != "" {
			if err := SetWorkers(c, typ, wc, 1); err != nil {
				return err
			}
		}
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
	for k, v := range ClassLabels(class) {
		if _, ok := labels[k]; !ok {
			labels[k] = v
		}
	}
	if _, ok := labels[LabelManagedBy]; !ok {
		labels[LabelManagedBy] = ManagedBy
	}
	if _, ok := labels[LabelAutoImport]; !ok && autoImport {
		labels[LabelAutoImport] = "true"
	}
	c.SetLabels(labels)
	return nil
}
