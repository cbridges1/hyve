package install

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/kube"
)

// OperatorKinds are the Cluster API Operator's provider kinds, by
// provider type.
var OperatorKinds = map[string]string{
	"core": "CoreProvider", "bootstrap": "BootstrapProvider", "controlPlane": "ControlPlaneProvider",
	"infrastructure": "InfrastructureProvider", "addon": "AddonProvider", "ipam": "IPAMProvider",
	"runtimeExtension": "RuntimeExtensionProvider",
}

// ProviderRequest is a provider to add: what 'hyve provider add' takes,
// and the UI's form.
type ProviderRequest struct {
	Name string `json:"name"`
	// Type is a provider type (OperatorKinds' keys); empty is
	// infrastructure, and lets a provider hyve knows (docker, kubeadm) go
	// in with every type it needs.
	Type         string `json:"type,omitempty"`
	Version      string `json:"version,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	FetchURL     string `json:"fetchURL,omitempty"`
	// Variables are what the provider's clusterctl configuration reads:
	// credentials and feature flags.
	Variables map[string]string `json:"variables,omitempty"`
}

// AddProvider installs a Cluster API provider the way the management
// cluster installs them: as a Turtles CAPIProvider with Rancher, a CAPI
// Operator provider object without, or from manifests on a lean install
// (local mode). It returns what it did, for the user.
func AddProvider(ctx context.Context, kc *kube.Client, r ProviderRequest) (string, error) {
	if r.Name == "" {
		return "", fmt.Errorf("a provider needs a name")
	}
	typ := r.Type
	if typ == "" {
		typ = "infrastructure"
	}
	providerName := r.ProviderName
	if providerName == "" {
		providerName = r.Name
	}
	namespace := r.Namespace
	if namespace == "" {
		namespace = r.Name + "-system"
	}
	// ClusterClasses need ClusterTopology; Variables can override.
	data := map[string]any{}
	for k, v := range FeatureVariables {
		data[k] = v
	}
	for k, v := range r.Variables {
		data[k] = v
	}
	if DetectCAPI(kc) == CAPILean {
		return leanProviderAdd(ctx, kc, r.Name, r.FetchURL, data)
	}
	turtles := DetectCAPI(kc) == CAPITurtles
	// The providers hyve knows (docker, kubeadm) go in as local mode adds
	// them: every type they need (kubeadm is a bootstrap and a
	// control-plane provider), at core Cluster API's version.
	if known, err := ProviderObjects(r.Name); err == nil && !turtles && r.Type == "" && r.Version == "" && r.FetchURL == "" && len(r.Variables) == 0 {
		for _, o := range known {
			if err := kc.ApplyObject(ctx, o); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("Applied Cluster API provider %s (%s)", r.Name, CAPIVersion), nil
	}
	objs := []*unstructured.Unstructured{{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace},
	}}}
	secret := r.Name + "-variables"
	objs = append(objs, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]any{"name": secret, "namespace": namespace},
		"stringData": data,
	}})
	spec := map[string]any{}
	if r.Version != "" {
		spec["version"] = r.Version
	}
	if r.FetchURL != "" {
		spec["fetchConfig"] = map[string]any{"url": r.FetchURL}
	}
	var obj *unstructured.Unstructured
	if turtles {
		spec["name"], spec["type"] = providerName, typ
		spec["configSecret"] = map[string]any{"name": secret}
		obj = &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "turtles-capi.cattle.io/v1alpha1", "kind": "CAPIProvider",
			"metadata": map[string]any{"name": r.Name, "namespace": namespace}, "spec": spec,
		}}
	} else {
		kind, ok := OperatorKinds[typ]
		if !ok {
			return "", fmt.Errorf("provider type %q: want one of core, bootstrap, controlPlane, infrastructure, addon, ipam, runtimeExtension", typ)
		}
		if !kc.HasKind("operator.cluster.x-k8s.io", "v1alpha2", kind) {
			return "", fmt.Errorf("neither Rancher Turtles nor the Cluster API Operator is installed; the hyve chart installs the operator")
		}
		spec["configSecret"] = map[string]any{"name": secret, "namespace": namespace}
		// The operator names a provider after its object.
		obj = &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operator.cluster.x-k8s.io/v1alpha2", "kind": kind,
			"metadata": map[string]any{"name": providerName, "namespace": namespace}, "spec": spec,
		}}
	}
	objs = append(objs, obj)
	for _, o := range objs {
		if err := kc.ApplyObject(ctx, o); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("Applied %s %s/%s", obj.GetKind(), namespace, obj.GetName()), nil
}

// leanProviderAdd adds a provider to a lean install (local mode), which
// has no CAPI Operator: from the manifests hyve embeds, or the release
// manifest at fetchURL, with the variables substituted the way clusterctl
// does.
func leanProviderAdd(ctx context.Context, kc *kube.Client, name, fetchURL string, data map[string]any) (string, error) {
	vars := map[string]string{}
	for k, v := range data {
		vars[k] = fmt.Sprint(v)
	}
	o := LeanOptions{Variables: vars}
	if fetchURL == "" {
		if _, ok := LeanProviders[name]; !ok {
			return "", fmt.Errorf("hyve doesn't carry provider %q: give its components manifest's URL", name)
		}
		if err := LeanInstall(ctx, kc, []string{name}, o); err != nil {
			return "", err
		}
	} else {
		raw, err := fetchManifest(ctx, fetchURL)
		if err != nil {
			return "", err
		}
		if err := LeanInstallManifest(ctx, kc, name, raw, o); err != nil {
			return "", err
		}
	}
	return "Added Cluster API provider " + name, nil
}

// releaseURL turns the URL form clusterctl and the CAPI Operator take for
// a GitHub release, https://github.com/O/R/releases/TAG/FILE (TAG may be
// latest), into the file's download URL, which a plain GET can fetch.
// Other URLs are returned as they are.
func releaseURL(url string) string {
	const gh = "https://github.com/"
	if !strings.HasPrefix(url, gh) {
		return url
	}
	p := strings.Split(strings.TrimPrefix(url, gh), "/")
	if len(p) != 5 || p[2] != "releases" || p[3] == "download" {
		return url
	}
	if p[3] == "latest" {
		return gh + strings.Join([]string{p[0], p[1], "releases", "latest", "download", p[4]}, "/")
	}
	return gh + strings.Join([]string{p[0], p[1], "releases", "download", p[3], p[4]}, "/")
}

func fetchManifest(ctx context.Context, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, fmt.Errorf("%q: want an http(s) URL", url)
	}
	url = releaseURL(url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}
