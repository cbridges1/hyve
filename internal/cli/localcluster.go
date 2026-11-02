package cli

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/login"
)

// The host cluster, hyve's management cluster, is "local" in the cluster
// commands, as it is in the proxy's paths (/k8s/clusters/local) and as
// Rancher names its own: listed first, and reachable with show,
// kubeconfig and auth. It isn't a Cluster API Cluster, so hyve doesn't
// create, scale, upgrade, expire or delete it; 'hyve install' changes
// what runs on it. No child cluster can take the name.

// isLocal says name is the host cluster's.
func isLocal(name string) bool { return name == login.LocalCluster }

// The Fleet cluster the host registers itself as.
const localFleetNS, localFleetName = "fleet-local", "local"

// errLocal is for what only an environment's clusters have: a lifecycle,
// extras, access grants.
func errLocal() error {
	return fmt.Errorf("%q is the host cluster, not one of an environment's clusters: hyve doesn't create, change or delete it ('hyve install' changes what runs on it)", login.LocalCluster)
}

// localSummary is what list and show print for the host: its Kubernetes
// version, when it was made (kube-system's age) and its Fleet agent's
// state. What the user can't read is left as "-".
type localSummary struct {
	version, age, fleet string
	nodes               int
}

func (s *session) localSummary(ctx context.Context) localSummary {
	out := localSummary{version: "-", age: "-", fleet: "-", nodes: -1}
	if v, err := s.kube.Core.Discovery().ServerVersion(); err == nil {
		out.version = v.GitVersion
	}
	if ns, err := s.kube.Core.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{}); err == nil {
		out.age = clusters.Age(ns.CreationTimestamp.Time)
	}
	if nodes, err := s.kube.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		out.nodes = len(nodes.Items)
	}
	fc, err := clusters.FleetCluster(ctx, s.kube.Dynamic, localFleetNS, localFleetName)
	switch {
	case err != nil:
	case fc == nil:
		out.fleet = "not registered"
	default:
		// Most of what the host runs comes from git repos, so how many of
		// its bundles are ready says more than ready or not.
		ready, _, _ := unstructured.NestedString(fc.Object, "status", "display", "readyBundles")
		out.fleet = dash(ready) + " bundles ready"
	}
	return out
}

func (s *session) showLocal(ctx context.Context) {
	l := s.localSummary(ctx)
	fmt.Printf("Name:          %s\n", login.LocalCluster)
	fmt.Println("Role:          host (hyve, Cluster API and Fleet run here)")
	fmt.Printf("Version:       %s\n", l.version)
	if l.nodes >= 0 {
		fmt.Printf("Nodes:         %d\n", l.nodes)
	}
	fmt.Printf("Age:           %s\n", l.age)
	fmt.Printf("Fleet:         %s\n", l.fleet)
}
