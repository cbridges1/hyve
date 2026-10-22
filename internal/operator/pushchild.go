package operator

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

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
	list, err := c.cs.CoreV1().Secrets("").List(ctx, metav1.ListOptions{LabelSelector: "owner=helm"})
	if err != nil {
		return nil, err
	}
	latest := map[string]Release{}
	for i := range list.Items {
		s := &list.Items[i]
		if s.Type != "helm.sh/release.v1" {
			continue
		}
		r, err := decodeRelease(s.Data["release"])
		if err != nil {
			continue
		}
		key := r.Namespace + "/" + r.Name
		if cur, ok := latest[key]; !ok || r.Version > cur.Version {
			latest[key] = r
		}
	}
	out := make([]Release, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	return out, nil
}

func (c *pushChild) Uninstall(ctx context.Context, r Release) error {
	if !r.KeepResources {
		objs, err := kube.Decode([]byte(r.Manifest))
		if err != nil {
			return err
		}
		for i := len(objs) - 1; i >= 0; i-- {
			o := objs[i]
			if o.GetNamespace() == "" {
				o.SetNamespace(r.Namespace)
			}
			if err := c.DeleteObject(ctx, o); err != nil {
				return err
			}
		}
	}
	err := c.cs.CoreV1().Secrets(r.Namespace).DeleteCollection(ctx, metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "owner=helm,name=" + r.Name})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// decodeRelease reads a Helm release Secret's payload: base64 of gzipped
// JSON (on top of the Secret's own encoding).
func decodeRelease(data []byte) (Release, error) {
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return Release{}, err
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return Release{}, err
		}
		if raw, err = io.ReadAll(zr); err != nil {
			return Release{}, err
		}
	}
	var rel struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Version   int    `json:"version"`
		Manifest  string `json:"manifest"`
		Chart     struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		return Release{}, err
	}
	if rel.Name == "" {
		return Release{}, fmt.Errorf("not a release")
	}
	a := rel.Chart.Metadata.Annotations
	return Release{
		Name: rel.Name, Namespace: rel.Namespace, Version: rel.Version, Manifest: rel.Manifest,
		BundleID: a[chartBundleID], AgentNamespace: a[chartAgentNamespace], KeepResources: a[chartKeepResources] == "true",
	}, nil
}
