package operator

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/helmrel"
	"github.com/cbridges1/hyve/internal/kube"
)

// Fleet's marks on the charts it deploys (fleet's helmdeployer): which
// BundleDeployment a release is, and which agent manages it.
const (
	chartBundleID       = "fleet.cattle.io/bundle-id"
	chartAgentNamespace = "fleet.cattle.io/agent-namespace"
	chartKeepResources  = "fleet.cattle.io/keep-resources"
)

// Release is a Helm release on a child, as far as push needs it.
type Release struct {
	Name, Namespace string
	Version         int
	BundleID        string
	AgentNamespace  string
	KeepResources   bool
	Manifest        string
}

// Child is what push does on a child cluster.
type Child interface {
	ApplyObject(ctx context.Context, o *unstructured.Unstructured) error
	// ListReleases lists the latest revision of each Helm release.
	ListReleases(ctx context.Context) ([]Release, error)
	// Uninstall removes a release's objects (unless it keeps them) and its
	// history, as `helm uninstall` does, without running hooks.
	Uninstall(ctx context.Context, r Release) error
}

type pushChild struct {
	*kube.Client
	cs kubernetes.Interface
}

func defaultPushChild(kubeconfig []byte) (Child, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 30 * time.Second
	kc, err := kube.New(cfg)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &pushChild{Client: kc, cs: cs}, nil
}

func (c *pushChild) ListReleases(ctx context.Context) ([]Release, error) {
	rels, err := helmrel.Latest(ctx, c.cs, "")
	if err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(rels))
	for _, r := range rels {
		out = append(out, fromHelm(r))
	}
	return out, nil
}

func (c *pushChild) Uninstall(ctx context.Context, r Release) error {
	return helmrel.Uninstall(ctx, c.Client, c.cs, helmrel.Release{Name: r.Name, Namespace: r.Namespace, Manifest: r.Manifest}, r.KeepResources)
}

// decodeRelease reads a Helm release Secret's payload.
func decodeRelease(data []byte) (Release, error) {
	r, err := helmrel.Decode(data)
	if err != nil {
		return Release{}, err
	}
	return fromHelm(r), nil
}

func fromHelm(r helmrel.Release) Release {
	a := r.Annotations
	return Release{
		Name: r.Name, Namespace: r.Namespace, Version: r.Version, Manifest: r.Manifest,
		BundleID: a[chartBundleID], AgentNamespace: a[chartAgentNamespace], KeepResources: a[chartKeepResources] == "true",
	}
}
