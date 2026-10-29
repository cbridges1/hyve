// Package helmrel reads Helm's release Secrets and uninstalls a release the
// way `helm uninstall --no-hooks` does, so hyve needs neither helm nor its
// library.
package helmrel

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/cbridges1/hyve/internal/kube"
)

// Release is one revision of a Helm release.
type Release struct {
	Name, Namespace string
	Version         int
	Manifest        string
	// Annotations are the chart's.
	Annotations map[string]string
}

// Decode reads a release Secret's payload: base64 of gzipped JSON (on top
// of the Secret's own encoding).
func Decode(data []byte) (Release, error) {
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
	return Release{Name: rel.Name, Namespace: rel.Namespace, Version: rel.Version, Manifest: rel.Manifest, Annotations: rel.Chart.Metadata.Annotations}, nil
}

// Latest lists the latest revision of each release in namespace (all
// namespaces when empty).
func Latest(ctx context.Context, cs kubernetes.Interface, namespace string) ([]Release, error) {
	list, err := cs.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm"})
	if err != nil {
		return nil, err
	}
	latest := map[string]Release{}
	var order []string
	for i := range list.Items {
		s := &list.Items[i]
		if s.Type != "helm.sh/release.v1" {
			continue
		}
		r, err := Decode(s.Data["release"])
		if err != nil {
			continue
		}
		key := r.Namespace + "/" + r.Name
		cur, ok := latest[key]
		if !ok {
			order = append(order, key)
		}
		if !ok || r.Version > cur.Version {
			latest[key] = r
		}
	}
	out := make([]Release, 0, len(latest))
	for _, k := range order {
		out = append(out, latest[k])
	}
	return out, nil
}

// Uninstall deletes a release's objects (unless keepResources), last first,
// then its history, as `helm uninstall --no-hooks` does. Objects of the
// keepKinds stay.
func Uninstall(ctx context.Context, kc *kube.Client, cs kubernetes.Interface, r Release, keepResources bool, keepKinds ...string) error {
	if !keepResources {
		objs, err := kube.Decode([]byte(r.Manifest))
		if err != nil {
			return err
		}
		for i := len(objs) - 1; i >= 0; i-- {
			o := objs[i]
			if slices.Contains(keepKinds, o.GetKind()) {
				continue
			}
			if o.GetNamespace() == "" {
				o.SetNamespace(r.Namespace)
			}
			if err := kc.DeleteObject(ctx, o); err != nil {
				return err
			}
		}
	}
	err := cs.CoreV1().Secrets(r.Namespace).DeleteCollection(ctx, metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "owner=helm,name=" + r.Name})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
