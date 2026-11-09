package install

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/kube"
)

// IsClassObject reports whether o is part of a ClusterClass as providers
// publish one: the ClusterClass itself, or one of the provider templates
// it refers to (CivoClusterTemplate, KubeadmControlPlaneTemplate,
// DockerMachineTemplate...).
func IsClassObject(o *unstructured.Unstructured) bool {
	g := o.GroupVersionKind().Group
	if g == "cluster.x-k8s.io" {
		return o.GetKind() == "ClusterClass"
	}
	return strings.HasSuffix(g, ".cluster.x-k8s.io") && strings.HasSuffix(o.GetKind(), "Template")
}

// ClassURL is where a provider publishes ClusterClass name, by clusterctl's
// convention: clusterclass-NAME.yaml beside the components manifest at
// fetchURL. A name that's already a URL is returned as it is.
func ClassURL(fetchURL, name string) (string, error) {
	if strings.Contains(name, "://") {
		return name, nil
	}
	i := strings.LastIndex(fetchURL, "/")
	if fetchURL == "" || i < 0 {
		return "", fmt.Errorf("ClusterClass %s: %w", name, ErrNoClassSource)
	}
	return fetchURL[:i+1] + "clusterclass-" + name + ".yaml", nil
}

// AddClusterClass installs a ClusterClass file a provider publishes into
// namespace, with clusterctl's variable substitution. The file may hold
// only the ClusterClass and its provider templates. It waits (up to
// timeout) for the provider's kinds, which an operator install adds a
// little after the provider object is applied.
func AddClusterClass(ctx context.Context, kc *kube.Client, url, namespace string, vars map[string]string, timeout time.Duration) ([]string, error) {
	raw, err := fetchManifest(ctx, url)
	if err != nil {
		return nil, err
	}
	raw, err = substitute(raw, vars)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	objs, err := kube.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("%s has nothing in it", url)
	}
	for _, o := range objs {
		if !IsClassObject(o) {
			return nil, fmt.Errorf("%s: %s %s isn't part of a ClusterClass; hyve only installs a ClusterClass and its provider templates from a provider's release", url, o.GetKind(), o.GetName())
		}
		o.SetNamespace(namespace)
	}
	missing := func() []string {
		var out []string
		for _, o := range objs {
			gvk := o.GroupVersionKind()
			if !kc.HasKind(gvk.Group, gvk.Version, gvk.Kind) {
				out = append(out, gvk.Kind)
			}
		}
		return out
	}
	err = kube.Poll(ctx, 5*time.Second, timeout, nil, func(context.Context) (bool, string, error) {
		return len(missing()) == 0, "", nil
	})
	if err != nil {
		if m := missing(); len(m) > 0 {
			return nil, fmt.Errorf("the cluster doesn't serve %s yet: is the provider installed? 'hyve provider list' shows when it's ready; then add the class again", strings.Join(m, ", "))
		}
		return nil, err
	}
	var applied []string
	for _, o := range objs {
		if err := kc.ApplyObject(ctx, o); err != nil {
			return applied, err
		}
		applied = append(applied, o.GetKind()+" "+namespace+"/"+o.GetName())
	}
	return applied, nil
}

// addClasses installs r's ClusterClasses, after its provider, and says
// which.
func addClasses(ctx context.Context, kc *kube.Client, r ProviderRequest, vars map[string]string) (string, error) {
	ns := r.ClusterClassNamespace
	if ns == "" {
		ns = DefaultTemplateNamespace
	}
	var classes []string
	for _, name := range r.ClusterClasses {
		url, err := ClassURL(r.FetchURL, name)
		if err != nil {
			return "", err
		}
		applied, err := AddClusterClass(ctx, kc, url, ns, vars, 3*time.Minute)
		if err != nil {
			return "", fmt.Errorf("ClusterClass %s: %w", name, err)
		}
		for _, a := range applied {
			if c, ok := strings.CutPrefix(a, "ClusterClass "); ok {
				classes = append(classes, c)
			}
		}
	}
	return "; installed ClusterClass " + strings.Join(classes, ", "), nil
}

// ErrNoClassSource is a ClusterClass named without the components URL it's
// published beside.
var ErrNoClassSource = errors.New("a provider's ClusterClasses are published beside its components manifest: give its URL (--fetch-url), or the class file's own URL")
