package install

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/kube"
)

// Lazy providers (HYVE-PROPOSAL.md, "Local mode"): local mode starts with
// core Cluster API and CAAPF only, and adds a provider the first time
// something needs one of its kinds: a ClusterClass that refers to them, a
// cluster created from such a class, or a Fleet bundle that failed for
// want of them.

// providerKinds says which known provider serves a group's kinds, by the
// kinds' prefix.
var providerKinds = []struct{ group, prefix, provider string }{
	{"infrastructure.cluster.x-k8s.io", "Docker", "docker"},
	{"bootstrap.cluster.x-k8s.io", "Kubeadm", "kubeadm"},
	{"controlplane.cluster.x-k8s.io", "Kubeadm", "kubeadm"},
}

// ProviderFor names the known provider that serves a kind, or "".
func ProviderFor(gk schema.GroupKind) string {
	for _, p := range providerKinds {
		if gk.Group == p.group && strings.HasPrefix(gk.Kind, p.prefix) {
			return p.provider
		}
	}
	return ""
}

// RefKinds are the kinds an object refers to: every apiVersion and kind
// pair under its spec, such as a ClusterClass's templates.
func RefKinds(obj *unstructured.Unstructured) []schema.GroupKind {
	seen := map[schema.GroupKind]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			av, _ := v["apiVersion"].(string)
			kind, _ := v["kind"].(string)
			if av != "" && kind != "" {
				gv, err := schema.ParseGroupVersion(av)
				if err == nil {
					seen[schema.GroupKind{Group: gv.Group, Kind: kind}] = true
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(obj.Object["spec"])
	return sortedKinds(seen)
}

// missingKindRE matches the API server's (and Helm's) error for a kind
// with no CRD, quotes escaped or not.
var missingKindRE = regexp.MustCompile(`no matches for kind \\?"([A-Za-z0-9]+)\\?" in version \\?"([a-z0-9.-]+)/`)

// MissingKinds are the kinds an error message says have no CRD.
func MissingKinds(text string) []schema.GroupKind {
	seen := map[schema.GroupKind]bool{}
	for _, m := range missingKindRE.FindAllStringSubmatch(text, -1) {
		seen[schema.GroupKind{Group: m[2], Kind: m[1]}] = true
	}
	return sortedKinds(seen)
}

func sortedKinds(seen map[schema.GroupKind]bool) []schema.GroupKind {
	out := make([]schema.GroupKind, 0, len(seen))
	for gk := range seen {
		out = append(out, gk)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

var ensureMu sync.Mutex

// CAPIInstall is how a management cluster's Cluster API was installed,
// which decides how providers are added to it.
type CAPIInstall string

const (
	CAPINone     CAPIInstall = ""
	CAPILean     CAPIInstall = "lean"     // release manifests (local mode)
	CAPIOperator CAPIInstall = "operator" // the CAPI Operator
)

// DetectCAPI says how the cluster's Cluster API was installed.
func DetectCAPI(kc *kube.Client) CAPIInstall {
	switch {
	case kc.HasKind("operator.cluster.x-k8s.io", "v1alpha2", "CoreProvider"):
		return CAPIOperator
	case kc.ServesKind(schema.GroupKind{Group: "cluster.x-k8s.io", Kind: "Cluster"}):
		return CAPILean
	}
	return CAPINone
}

// EnsureProviders adds the known providers serving any of kinds that the
// cluster doesn't serve yet, the way its Cluster API was installed. It
// returns what
// it added, and the missing kinds no known provider serves.
func EnsureProviders(ctx context.Context, kc *kube.Client, kinds []schema.GroupKind, o LeanOptions) (added []string, unknown []schema.GroupKind, err error) {
	// One at a time, so a second caller needing the same provider finds
	// it served rather than installing it again.
	ensureMu.Lock()
	defer ensureMu.Unlock()
	var capi []schema.GroupKind
	for _, gk := range kinds {
		if strings.HasSuffix(gk.Group, ".cluster.x-k8s.io") {
			capi = append(capi, gk)
		}
	}
	if len(capi) == 0 {
		return nil, nil, nil
	}
	want := map[string]bool{}
	for _, gk := range kc.Unserved(capi) {
		if p := ProviderFor(gk); p != "" {
			want[p] = true
		} else {
			unknown = append(unknown, gk)
		}
	}
	if len(want) == 0 {
		return nil, unknown, nil
	}
	for p := range want {
		added = append(added, p)
	}
	sort.Strings(added)
	switch DetectCAPI(kc) {
	case CAPILean:
		if o.GOGC == "" {
			o.GOGC = coreGOGC(ctx, kc)
		}
		err = LeanInstall(ctx, kc, added, o)
	case CAPIOperator:
		for _, p := range added {
			objs, perr := ProviderObjects(p)
			if perr != nil {
				return nil, unknown, perr
			}
			for _, obj := range objs {
				if err = kc.ApplyObject(ctx, obj); err != nil {
					return nil, unknown, err
				}
			}
		}
	default:
		return nil, unknown, fmt.Errorf("provider %s is missing; add it with 'hyve provider add'", strings.Join(added, ", "))
	}
	if err != nil {
		return nil, unknown, err
	}
	return added, unknown, nil
}

// coreGOGC is the GOGC core Cluster API runs with, so providers added
// later match the install's tuning.
func coreGOGC(ctx context.Context, kc *kube.Client) string {
	d, err := kc.Dynamic.Resource(deploymentGVR).Namespace("capi-system").Get(ctx, "capi-controller-manager", metav1.GetOptions{})
	if err != nil {
		return ""
	}
	containers, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")
	for _, c := range containers {
		c, _ := c.(map[string]any)
		env, _ := c["env"].([]any)
		for _, e := range env {
			e, _ := e.(map[string]any)
			if e["name"] == "GOGC" {
				v, _ := e["value"].(string)
				return v
			}
		}
	}
	return ""
}

// WaitProviders waits until every provider's Deployment is available, so
// their webhooks answer.
func WaitProviders(ctx context.Context, kc *kube.Client, logf func(string, ...any)) error {
	return kube.Poll(ctx, 3*time.Second, 5*time.Minute, func(m string) { logf("  %s", m) }, func(ctx context.Context) (bool, string, error) {
		list, err := kc.Dynamic.Resource(deploymentGVR).List(ctx, metav1.ListOptions{LabelSelector: "cluster.x-k8s.io/provider"})
		if err != nil {
			return false, "waiting for the providers: " + firstLine(err.Error()), nil
		}
		for _, d := range list.Items {
			want, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas")
			ready, _, _ := unstructured.NestedInt64(d.Object, "status", "availableReplicas")
			if ready < want {
				return false, fmt.Sprintf("waiting for %s/%s", d.GetNamespace(), d.GetName()), nil
			}
		}
		return true, "", nil
	})
}
