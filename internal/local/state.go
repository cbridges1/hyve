package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clusterctl "sigs.k8s.io/cluster-api/cmd/clusterctl/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
)

// Saved state (HYVE-PROPOSAL.md, "Local mode"): local mode's Cluster API
// objects, with hyve's templates and resources (their CRDs are labelled
// for clusterctl move), written as files by clusterctl's move
// --to-directory. A fresh local cluster restores them before anything
// else, so losing it, or starting every CI job from nothing, loses no
// clusters; their agents reconnect to the new host by themselves.

// StateDir is where state is kept: the directory 'hyve local up
// --state-dir' set, or ~/.hyve/local/state.
func StateDir(home string) string {
	if b, err := os.ReadFile(stateDirFile(home)); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	return filepath.Join(Dir(home), "state")
}

func stateDirFile(home string) string { return filepath.Join(Dir(home), "state-dir") }

// SetStateDir keeps state in dir from now on.
func SetStateDir(home, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		return err
	}
	return os.WriteFile(stateDirFile(home), []byte(abs+"\n"), 0o600)
}

// ErrDeleting is Save declining while a cluster is being deleted, which
// a restore would otherwise bring back. The next save keeps the result.
var ErrDeleting = errors.New("a cluster is being deleted; the state is saved once it's gone")

// namespacesFile keeps the saved namespaces' labels, which mark
// environments and template namespaces; clusterctl recreates bare ones.
const namespacesFile = "hyve-namespaces.yaml"

var (
	namespaceGVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	// The kinds whose namespaces are saved.
	stateKinds = []schema.GroupVersionResource{
		{Group: "cluster.x-k8s.io", Version: "v1beta2", Resource: "clusters"},
		{Group: "cluster.x-k8s.io", Version: "v1beta2", Resource: "clusterclasses"},
		{Group: "hyve.io", Version: "v1alpha1", Resource: "clustertemplates"},
		{Group: "hyve.io", Version: "v1alpha1", Resource: "resources"},
		{Group: "hyve.io", Version: "v1alpha1", Resource: "clusterresources"},
	}
)

// adminKubeconfig writes the local cluster's admin kubeconfig to a file
// clusterctl can read, and returns a client for it.
func adminKubeconfig(ctx context.Context, home string) (string, *kube.Client, error) {
	raw, err := run(ctx, "k3d", "kubeconfig", "get", Name)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(Dir(home), "kubeconfig")
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		return "", nil, err
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(raw))
	if err != nil {
		return "", nil, err
	}
	kc, err := kube.New(cfg)
	return path, kc, err
}

func newClusterctl(ctx context.Context) (clusterctl.Client, error) {
	// clusterctl logs through controller-runtime's logger; hyve reports
	// what it did itself.
	ctrllog.SetLogger(ctrllog.Log.WithSink(nil))
	return clusterctl.New(ctx, "")
}

// Save writes the local cluster's state to dir, replacing what's there.
// The local cluster must be running.
func Save(ctx context.Context, home, dir string) (int, error) {
	if _, running := Running(ctx); !running {
		return 0, errors.New("the local cluster isn't running")
	}
	if dir == "" {
		dir = StateDir(home)
	}
	kcPath, kc, err := adminKubeconfig(ctx, home)
	if err != nil {
		return 0, err
	}
	clusters, err := kc.Dynamic.Resource(stateKinds[0]).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, c := range clusters.Items {
			if c.GetDeletionTimestamp() != nil {
				return 0, ErrDeleting
			}
		}
	}
	namespaces, err := stateNamespaces(ctx, kc)
	if err != nil {
		return 0, err
	}
	tmp := dir + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	c, err := newClusterctl(ctx)
	if err != nil {
		return 0, err
	}
	var nsObjs []any
	for _, ns := range namespaces {
		if err := c.Move(ctx, clusterctl.MoveOptions{
			FromKubeconfig: clusterctl.Kubeconfig{Path: kcPath},
			Namespace:      ns,
			ToDirectory:    tmp,
		}); err != nil {
			return 0, fmt.Errorf("saving namespace %s: %w", ns, err)
		}
		obj, err := kc.Dynamic.Resource(namespaceGVR).Get(ctx, ns, metav1.GetOptions{})
		if err != nil {
			return 0, err
		}
		// hyve's labels mark environments and template namespaces; others
		// belong to whatever created the namespace here.
		labels := map[string]any{}
		for k, v := range obj.GetLabels() {
			if strings.HasPrefix(k, "hyve.io/") {
				labels[k] = v
			}
		}
		nsObjs = append(nsObjs, map[string]any{
			"apiVersion": "v1", "kind": "Namespace",
			"metadata": map[string]any{"name": ns, "labels": labels},
		})
	}
	raw, err := yaml.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": nsObjs})
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(tmp, namespacesFile), raw, 0o600); err != nil {
		return 0, err
	}
	entries, _ := os.ReadDir(tmp)
	// Swap the new state in whole, so an interrupted save leaves the old.
	old := dir + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, old); err != nil {
			return 0, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return 0, err
	}
	_ = os.RemoveAll(old)
	return len(entries) - 1, nil
}

// stateNamespaces are the namespaces holding anything Save keeps.
func stateNamespaces(ctx context.Context, kc *kube.Client) ([]string, error) {
	seen := map[string]bool{}
	for _, gvr := range stateKinds {
		list, err := kc.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "could not find the requested resource") {
				continue
			}
			return nil, err
		}
		for _, o := range list.Items {
			seen[o.GetNamespace()] = true
		}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out, nil
}

// HasState says whether dir holds saved state.
func HasState(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != namespacesFile && strings.HasSuffix(e.Name(), ".yaml") {
			return true
		}
	}
	return false
}

// Restore recreates saved state in the local cluster: the namespaces with
// their labels, the providers the saved objects need, hyve's templates and
// resources (so the webhook can fill in the Clusters made from them), then
// everything through clusterctl's move --from-directory.
func Restore(ctx context.Context, home, dir string, logf func(string, ...any)) (int, error) {
	if dir == "" {
		dir = StateDir(home)
	}
	if !HasState(dir) {
		return 0, nil
	}
	kcPath, kc, err := adminKubeconfig(ctx, home)
	if err != nil {
		return 0, err
	}
	objs, err := readState(dir)
	if err != nil {
		return 0, err
	}
	if raw, err := os.ReadFile(filepath.Join(dir, namespacesFile)); err == nil {
		list, err := kube.Decode(raw)
		if err != nil {
			return 0, err
		}
		for _, l := range list {
			items, _, _ := unstructured.NestedSlice(l.Object, "items")
			for _, it := range items {
				m, _ := it.(map[string]any)
				if err := kc.ApplyObject(ctx, &unstructured.Unstructured{Object: m}); err != nil {
					return 0, err
				}
			}
		}
	}
	var kinds []schema.GroupKind
	for _, o := range objs {
		kinds = append(kinds, o.GroupVersionKind().GroupKind())
	}
	added, _, err := install.EnsureProviders(ctx, kc, kinds, install.LeanOptions{Log: logf})
	if err != nil {
		return 0, err
	}
	if len(added) > 0 {
		logf("Waiting for provider %s", strings.Join(added, ", "))
		if err := install.WaitProviders(ctx, kc, logf); err != nil {
			return 0, err
		}
	}
	for _, o := range objs {
		if o.GroupVersionKind().Group != "hyve.io" {
			continue
		}
		fresh := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": o.GetAPIVersion(), "kind": o.GetKind(),
			"metadata": map[string]any{"name": o.GetName(), "namespace": o.GetNamespace(), "labels": toAny(o.GetLabels()), "annotations": toAny(o.GetAnnotations())},
			"spec":     o.Object["spec"],
		}}
		if err := kc.ApplyObject(ctx, fresh); err != nil {
			return 0, fmt.Errorf("restoring %s %s/%s: %w", o.GetKind(), o.GetNamespace(), o.GetName(), err)
		}
	}
	c, err := newClusterctl(ctx)
	if err != nil {
		return 0, err
	}
	// Webhooks of providers just added may need a moment; clusterctl
	// retries each object, and this retries the whole restore once more.
	err = c.Move(ctx, clusterctl.MoveOptions{FromDirectory: dir, ToKubeconfig: clusterctl.Kubeconfig{Path: kcPath}})
	if err != nil {
		logf("Retrying the restore: %v", err)
		time.Sleep(10 * time.Second)
		err = c.Move(ctx, clusterctl.MoveOptions{FromDirectory: dir, ToKubeconfig: clusterctl.Kubeconfig{Path: kcPath}})
	}
	if err != nil {
		return 0, err
	}
	return len(objs), nil
}

func readState(dir string) ([]*unstructured.Unstructured, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	for _, e := range entries {
		if e.Name() == namespacesFile || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		objs, err := kube.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, objs...)
	}
	return out, nil
}

func toAny(m map[string]string) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
