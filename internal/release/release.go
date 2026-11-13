// Package release takes a cluster out of hyve's management and leaves it
// running ('hyve cluster release'): the reverse of 'hyve cluster adopt'
// and 'hyve cluster import'.
//
// An imported cluster's Cluster is just deleted: hyve-controller detaches
// it, as 'hyve cluster delete' does. A cluster hyve made or adopted is
// released as clusterctl move leaves its source: its Cluster API objects
// are paused, so no provider acts on them, then deleted with their
// finalizers removed, so no provider deletes what they stand for. Before
// that, hyve's and Fleet's agents come off the cluster (importer.Detach),
// so Fleet forgetting the cluster doesn't remove what it deployed there.
package release

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/importer"
	"github.com/cbridges1/hyve/internal/kube"
)

// PausedAnnotation pauses Cluster API's and providers' reconciling of an
// object.
const PausedAnnotation = "cluster.x-k8s.io/paused"

// Options change how a cluster is released.
type Options struct {
	// Force releases the cluster even when its agents can't be removed
	// (it can't be reached): they stay, disconnected.
	Force bool
	// DryRun checks the user may release the cluster and lists what
	// would be removed, and changes nothing.
	DryRun bool
	// Detach removes hyve's and Fleet's agents from the cluster; nil is
	// importer.Detach (tests replace it).
	Detach func(ctx context.Context, kubeconfig []byte) error
	// Logf, if set, reports progress.
	Logf func(format string, args ...any)
}

// Result is what Release did.
type Result struct {
	// Imported is true for an imported cluster, which was detached.
	Imported bool `json:"imported,omitempty"`
	// Kubeconfig is the cluster's admin kubeconfig as hyve held it, to
	// keep reaching it (nil for an imported cluster, whose was hyve's own).
	Kubeconfig []byte `json:"kubeconfig,omitempty"`
	// HasKubeconfig says hyve holds a kubeconfig for it: a dry run says
	// so without sending it.
	HasKubeconfig bool `json:"hasKubeconfig,omitempty"`
	// Removed are the management cluster's objects for it, as Kind/name.
	Removed []string `json:"removed"`
	// DetachError is why its agents couldn't be removed (with Force).
	DetachError string `json:"detachError,omitempty"`
}

// object is one of the cluster's objects and where it's served.
type object struct {
	gvr schema.GroupVersionResource
	u   *unstructured.Unstructured
}

func (o object) String() string { return o.u.GetKind() + "/" + o.u.GetName() }

// Release takes cluster name in namespace ns out of hyve's management. kc
// acts as the user, who must be able to delete it and change its objects:
// every change is first tried as a dry run, so a user who can't release
// it changes nothing.
func Release(ctx context.Context, kc *kube.Client, ns, name string, opts Options) (*Result, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	clusterRI := kc.Dynamic.Resource(clusters.ClusterGVR).Namespace(ns)
	c, err := clusterRI.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if c.GetDeletionTimestamp() != nil {
		return nil, fmt.Errorf("cluster %s is being deleted already", name)
	}
	if clusters.Origin(c) == clusters.OriginImported {
		// hyve-controller detaches an imported cluster when its Cluster
		// goes, and never changes the cluster itself.
		del := metav1.DeleteOptions{}
		if opts.DryRun {
			del.DryRun = []string{metav1.DryRunAll}
		}
		if err := clusterRI.Delete(ctx, name, del); err != nil {
			return nil, err
		}
		return &Result{Imported: true, Removed: []string{"Cluster/" + name}}, nil
	}

	objs, err := objectsOf(ctx, kc, c)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	for _, o := range objs {
		res.Removed = append(res.Removed, o.String())
	}
	kubeconfig, err := kc.Core.CoreV1().Secrets(ns).Get(ctx, name+"-kubeconfig", metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// Never provisioned: nothing on it to detach.
	case err != nil:
		return nil, fmt.Errorf("reading its kubeconfig: %w", err)
	default:
		res.Kubeconfig = kubeconfig.Data["value"]
		res.HasKubeconfig = len(res.Kubeconfig) > 0
	}

	// Everything as a dry run first: RBAC and admission say no here, or
	// not at all.
	dry := []string{metav1.DryRunAll}
	for _, o := range objs {
		if err := pause(ctx, kc.Dynamic, o, dry); err != nil {
			return nil, fmt.Errorf("can't release %s: %s: %w", name, o, err)
		}
		if err := unfinalize(ctx, kc.Dynamic, o, dry); err != nil {
			return nil, fmt.Errorf("can't release %s: %s: %w", name, o, err)
		}
		if err := remove(ctx, kc.Dynamic, o, dry); err != nil {
			return nil, fmt.Errorf("can't release %s: %s: %w", name, o, err)
		}
	}
	if opts.DryRun {
		res.Kubeconfig = nil
		return res, nil
	}

	logf("Pausing %d objects of cluster %s, so its provider leaves the cluster alone", len(objs), name)
	for _, o := range objs {
		if err := pause(ctx, kc.Dynamic, o, nil); err != nil {
			return nil, unpauseAfter(ctx, kc.Dynamic, objs, fmt.Errorf("pausing %s: %w", o, err))
		}
	}

	if res.Kubeconfig != nil {
		logf("Removing hyve's and Fleet's agents from the cluster")
		detach := opts.Detach
		if detach == nil {
			detach = importer.Detach
		}
		if err := detach(ctx, res.Kubeconfig); err != nil {
			msg := firstLine(err)
			if !opts.Force {
				return nil, unpauseAfter(ctx, kc.Dynamic, objs, fmt.Errorf("couldn't remove hyve's agents from the cluster, so nothing was released: %s (force releases it anyway, leaving them disconnected)", msg))
			}
			res.DetachError = msg
			logf("Couldn't remove the agents (%s); releasing anyway", msg)
		}
	}

	logf("Removing cluster %s from hyve, leaving the cluster running", name)
	for _, o := range objs {
		if err := unfinalize(ctx, kc.Dynamic, o, nil); err != nil {
			return nil, fmt.Errorf("%s: %w", o, err)
		}
	}
	// The Cluster last, so nothing is deleted through it first.
	for _, o := range objs {
		if err := remove(ctx, kc.Dynamic, o, nil); err != nil {
			return nil, fmt.Errorf("%s: %w", o, err)
		}
	}
	// A finalizer added back between the two steps keeps its object:
	// remove it again until all are gone.
	err = kube.Poll(ctx, time.Second, 30*time.Second, nil, func(ctx context.Context) (bool, string, error) {
		var left []string
		for _, o := range objs {
			u, err := kc.Dynamic.Resource(o.gvr).Namespace(ns).Get(ctx, o.u.GetName(), metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return false, "", err
			}
			if len(u.GetFinalizers()) > 0 && u.GetUID() == o.u.GetUID() {
				if err := unfinalize(ctx, kc.Dynamic, object{o.gvr, u}, nil); err != nil {
					return false, "", err
				}
			}
			left = append(left, o.String())
		}
		return len(left) == 0, "waiting for " + strings.Join(left, ", "), nil
	})
	return res, err
}

// objectsOf are a cluster's objects on the management cluster: the
// Cluster API objects (any *.cluster.x-k8s.io kind: control plane,
// infrastructure, machines, pools, bootstrap configs) and Secrets labelled
// as its, and the objects its refs name, with the Cluster last.
func objectsOf(ctx context.Context, kc *kube.Client, c *unstructured.Unstructured) ([]object, error) {
	ns, name := c.GetNamespace(), c.GetName()
	res, err := kc.NamespacedResources()
	if err != nil {
		return nil, err
	}
	seen := map[types.UID]bool{c.GetUID(): true}
	var out []object
	add := func(gvr schema.GroupVersionResource, u *unstructured.Unstructured) {
		if !seen[u.GetUID()] {
			seen[u.GetUID()] = true
			out = append(out, object{gvr, u})
		}
	}
	sel := metav1.ListOptions{LabelSelector: importer.ClusterNameLabel + "=" + name}
	for _, gvr := range res {
		if !(gvr.Group == "" && gvr.Resource == "secrets") && !strings.HasSuffix(gvr.Group, "cluster.x-k8s.io") {
			continue
		}
		if gvr == clusters.ClusterGVR || gvr.Resource == "clusterclasses" {
			continue
		}
		l, err := kc.Dynamic.Resource(gvr).Namespace(ns).List(ctx, sel)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", gvr.GroupResource(), err)
		}
		for i := range l.Items {
			add(gvr, &l.Items[i])
		}
	}
	// A Cluster made without a topology needn't label what it refers to.
	for _, ref := range []string{"infrastructureRef", "controlPlaneRef"} {
		r, ok, _ := unstructured.NestedMap(c.Object, "spec", ref)
		if !ok {
			continue
		}
		gk, version := refKind(r)
		gvr, err := kc.Mapping(gk, version)
		if err != nil {
			continue
		}
		n, _ := r["name"].(string)
		u, err := kc.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, n, metav1.GetOptions{})
		if err == nil {
			add(gvr, u)
		}
	}
	slices.SortStableFunc(out, func(a, b object) int { return strings.Compare(a.String(), b.String()) })
	return append(out, object{clusters.ClusterGVR, c}), nil
}

// refKind reads a ref's kind: v1beta2's apiGroup, or v1beta1's apiVersion.
func refKind(r map[string]any) (schema.GroupKind, string) {
	kind, _ := r["kind"].(string)
	if g, ok := r["apiGroup"].(string); ok {
		return schema.GroupKind{Group: g, Kind: kind}, ""
	}
	av, _ := r["apiVersion"].(string)
	gv, _ := schema.ParseGroupVersion(av)
	return schema.GroupKind{Group: gv.Group, Kind: kind}, gv.Version
}

func patch(ctx context.Context, dyn dynamic.Interface, o object, body string, dryRun []string) error {
	_, err := dyn.Resource(o.gvr).Namespace(o.u.GetNamespace()).Patch(ctx, o.u.GetName(), types.MergePatchType, []byte(body), metav1.PatchOptions{DryRun: dryRun})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// pause stops Cluster API and its providers acting on o: the paused
// annotation, and on the Cluster spec.paused too.
func pause(ctx context.Context, dyn dynamic.Interface, o object, dryRun []string) error {
	body := `{"metadata":{"annotations":{"` + PausedAnnotation + `":""}}}`
	if o.gvr == clusters.ClusterGVR {
		body = `{"metadata":{"annotations":{"` + PausedAnnotation + `":""}},"spec":{"paused":true}}`
	}
	return patch(ctx, dyn, o, body, dryRun)
}

func unfinalize(ctx context.Context, dyn dynamic.Interface, o object, dryRun []string) error {
	return patch(ctx, dyn, o, `{"metadata":{"finalizers":null}}`, dryRun)
}

func remove(ctx context.Context, dyn dynamic.Interface, o object, dryRun []string) error {
	err := dyn.Resource(o.gvr).Namespace(o.u.GetNamespace()).Delete(ctx, o.u.GetName(), metav1.DeleteOptions{DryRun: dryRun})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// unpauseAfter undoes pause when a release stops part way, and returns
// cause. What was paused before stays paused.
func unpauseAfter(ctx context.Context, dyn dynamic.Interface, objs []object, cause error) error {
	errs := []error{cause}
	for _, o := range objs {
		if _, was := o.u.GetAnnotations()[PausedAnnotation]; was {
			continue
		}
		if p, _, _ := unstructured.NestedBool(o.u.Object, "spec", "paused"); p && o.gvr == clusters.ClusterGVR {
			continue
		}
		body := `{"metadata":{"annotations":{"` + PausedAnnotation + `":null}}}`
		if o.gvr == clusters.ClusterGVR {
			body = `{"metadata":{"annotations":{"` + PausedAnnotation + `":null}},"spec":{"paused":null}}`
		}
		if err := patch(ctx, dyn, o, body, nil); err != nil {
			errs = append(errs, fmt.Errorf("unpausing %s: %w", o, err))
		}
	}
	return errors.Join(errs...)
}

// firstLine is a joined error's first error, and how many more there were.
func firstLine(err error) string {
	lines := strings.Split(err.Error(), "\n")
	if len(lines) == 1 {
		return lines[0]
	}
	return fmt.Sprintf("%s (and %d more)", lines[0], len(lines)-1)
}
