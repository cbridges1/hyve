package install

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/cbridges1/hyve/internal/helmrel"
	"github.com/cbridges1/hyve/internal/kube"
)

// Moving a management cluster off Rancher (HYVE-PROPOSAL.md, open question
// 3), rehearsed on Rancher 2.14 with Turtles: Rancher, Turtles, their
// charts, CRDs, webhooks, namespaces and RBAC go; Fleet (its release, CRDs,
// GitRepos and bundles) and Cluster API (its CRDs, objects and running
// providers) stay. herd, which installed Rancher on some hosts, goes too.
// Rancher's data (its PVC, or a host path) is left for the admin to delete.

var (
	capiProviderGVR     = schema.GroupVersionResource{Group: "turtles-capi.cattle.io", Version: "v1alpha1", Resource: "capiproviders"}
	clusterctlConfigGVR = schema.GroupVersionResource{Group: "turtles-capi.cattle.io", Version: "v1alpha1", Resource: "clusterctlconfigs"}
	namespaceGVR        = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	serviceGVR          = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	serviceAccountGVR   = schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
	clusterRoleGVR      = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	clusterBindingGVR   = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	apiServiceGVR       = schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}
	validatingHookGVR   = schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}
)

// rancherGroups are the API groups Rancher, Turtles and herd serve. Not
// fleet.cattle.io (Fleet stays), nor helm.cattle.io and k3s.cattle.io
// (k3s's own).
var rancherGroups = regexp.MustCompile(`^((auditlog|catalog|management|provisioning|rke-machine-config|rke-machine|rke|telemetry|turtles-capi|ui|upgrade|ext|cluster|project|cis|logging|monitoring|elemental)\.cattle\.io|herd\.io)$`)

// rancherNamespaces are namespaces only Rancher uses. cattle-capi-system
// isn't one: Cluster API's core keeps running there.
var rancherNamespaces = []string{
	"cattle-global-data", "cattle-global-nt", "cattle-impersonation-system", "cattle-local-user-passwords",
	"cattle-ui-plugin-system", "cattle-turtles-system", "cattle-provisioning-capi-system", "herd-system",
}

// rancherNamespaceRE matches the namespaces Rancher makes for its local
// cluster, projects and users; they also carry its namespace-auth
// annotation.
var rancherNamespaceRE = regexp.MustCompile(`^(local|p-[a-z0-9]{5}|c-[a-z0-9]{5}|user-[a-z0-9]{5}|u-[a-z0-9]{10})$`)

// rancherRBAC matches Rancher's cluster-wide roles and bindings by name.
// Not by label: k3s labels its own with objectset.rio.cattle.io too. Fleet's
// own (request-<id>-content, an agent's access to its bundles) stay.
var rancherRBAC = regexp.MustCompile(`^(cattle-|rancher-|rancher$|globaladmin-|grb-|crb-|fleetworkspace-|pod-impersonation-helm-op-|rke2-machineconfig-cleanup|default-admin-|cluster-owner$|project-owner$|(p|c)-[a-z0-9]{5}-|(u|user)-[a-z0-9]+-view$|herd-)`)

// rancherReleaseNamespaces are where Rancher's charts are released;
// releases there that Fleet deployed (a bundle's) stay.
var rancherReleaseNamespaces = []string{"cattle-system", "cattle-turtles-system", "cattle-ui-plugin-system", "cattle-provisioning-capi-system"}

// HasRancher reports whether Rancher, or Turtles, is on the cluster.
func HasRancher(ctx context.Context, kc *kube.Client) bool {
	return hasCRD(ctx, kc, "clusters.management.cattle.io") || hasCRD(ctx, kc, "capiproviders.turtles-capi.cattle.io")
}

// RemoveRancher removes Rancher, Turtles and herd from the cluster, keeping
// Fleet and Cluster API. Safe to re-run.
func RemoveRancher(ctx context.Context, kc *kube.Client, logf func(string, ...any)) error {
	logf("Stopping Rancher, Turtles and herd")
	for _, d := range [][2]string{{"cattle-system", "rancher"}, {"cattle-turtles-system", "rancher-turtles-controller-manager"}, {"herd-system", "herd-controller"}} {
		if err := scaleToZero(ctx, kc, d[0], d[1], logf); err != nil {
			return err
		}
	}

	// Turtles' CAPIProviders own each provider's objects: deleted with the
	// default propagation they'd take the running controllers with them,
	// leaving their webhooks pointing at nothing.
	logf("Detaching Cluster API from Turtles; its providers keep running")
	for _, gvr := range []schema.GroupVersionResource{capiProviderGVR, clusterctlConfigGVR} {
		if err := dropAll(ctx, kc, gvr, true); err != nil {
			return err
		}
	}

	logf("Uninstalling Rancher's charts")
	for _, ns := range rancherReleaseNamespaces {
		rels, err := helmrel.Latest(ctx, kc.Core, ns)
		if err != nil {
			return err
		}
		for _, r := range rels {
			if r.Annotations["fleet.cattle.io/bundle-id"] != "" {
				continue
			}
			logf("  %s/%s", r.Namespace, r.Name)
			// Without hooks: Turtles' delete the Cluster API core. And
			// without its namespaces: Turtles' chart owns
			// cattle-capi-system, where the core keeps running.
			if err := helmrel.Uninstall(ctx, kc, kc.Core, r, false, "Namespace"); err != nil {
				return fmt.Errorf("uninstalling %s/%s: %w", r.Namespace, r.Name, err)
			}
		}
	}

	logf("Deleting Rancher's webhooks, API extension and Deployment")
	// Installed without Helm (herd's manifests): the Deployment, its
	// Service and account. Its data (a PVC or host path) stays.
	for _, o := range []struct {
		gvr       schema.GroupVersionResource
		namespace string
		name      string
	}{
		{deploymentGVR, "cattle-system", "rancher"},
		{serviceGVR, "cattle-system", "rancher"},
		{serviceAccountGVR, "cattle-system", "rancher"},
		{validatingHookGVR, "", "rancher.cattle.io"},
		{mutatingWebhookGVR, "", "rancher.cattle.io"},
		{validatingHookGVR, "", "herd-controller"},
		{mutatingWebhookGVR, "", "herd-controller"},
	} {
		if err := deleteOne(ctx, resourceFor(kc, o.gvr, o.namespace), o.name, false); err != nil {
			return err
		}
	}
	// An APIService left behind breaks discovery for every client.
	svcs, err := kc.Dynamic.Resource(apiServiceGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, s := range svcs.Items {
		group, _, _ := unstructured.NestedString(s.Object, "spec", "group")
		if rancherGroups.MatchString(group) {
			if err := deleteOne(ctx, kc.Dynamic.Resource(apiServiceGVR), s.GetName(), false); err != nil {
				return err
			}
		}
	}

	logf("Deleting Rancher's CRDs (Fleet's stay)")
	crds, err := kc.Dynamic.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, c := range crds.Items {
		group, _, _ := unstructured.NestedString(c.Object, "spec", "group")
		if !rancherGroups.MatchString(group) {
			continue
		}
		// Rancher's finalizers on its objects would hold the CRD forever.
		if gvr, ok := servedGVR(&c); ok {
			if err := dropAll(ctx, kc, gvr, false); err != nil {
				return err
			}
		}
		if err := deleteOne(ctx, kc.Dynamic.Resource(crdGVR), c.GetName(), false); err != nil {
			return err
		}
	}

	logf("Deleting Rancher's namespaces")
	nss, err := kc.Dynamic.Resource(namespaceGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, ns := range nss.Items {
		name := ns.GetName()
		_, rancherAuth := ns.GetAnnotations()["lifecycle.cattle.io/create.namespace-auth"]
		if contains(rancherNamespaces, name) || (rancherAuth && rancherNamespaceRE.MatchString(name)) {
			if err := deleteOne(ctx, kc.Dynamic.Resource(namespaceGVR), name, false); err != nil {
				return err
			}
		}
	}

	logf("Deleting Rancher's cluster roles and bindings")
	for _, gvr := range []schema.GroupVersionResource{clusterBindingGVR, clusterRoleGVR} {
		l, err := kc.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, o := range l.Items {
			n := o.GetName()
			if !rancherRBAC.MatchString(n) || strings.HasPrefix(n, "system:") || strings.Contains(n, "fleet") || fleetManaged(&o) {
				continue
			}
			if err := dropFinalizers(ctx, kc.Dynamic.Resource(gvr), &o); err != nil {
				return err
			}
			if err := deleteOne(ctx, kc.Dynamic.Resource(gvr), n, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func resourceFor(kc *kube.Client, gvr schema.GroupVersionResource, ns string) dynamic.ResourceInterface {
	if ns == "" {
		return kc.Dynamic.Resource(gvr)
	}
	return kc.Dynamic.Resource(gvr).Namespace(ns)
}

// scaleToZero scales a Deployment to nothing and waits for its pods to
// go, so it does nothing behind the removal's back.
func scaleToZero(ctx context.Context, kc *kube.Client, ns, name string, logf func(string, ...any)) error {
	ri := kc.Dynamic.Resource(deploymentGVR).Namespace(ns)
	d, err := ri.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := ri.Patch(ctx, name, types.MergePatchType, []byte(`{"spec":{"replicas":0}}`), metav1.PatchOptions{}); err != nil {
		return err
	}
	sel, _, _ := unstructured.NestedStringMap(d.Object, "spec", "selector", "matchLabels")
	var parts []string
	for k, v := range sel {
		parts = append(parts, k+"="+v)
	}
	return kube.Poll(ctx, 2*time.Second, 5*time.Minute, func(s string) { logf("  %s", s) }, func(ctx context.Context) (bool, string, error) {
		pods, err := kc.Core.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: strings.Join(parts, ",")})
		if err != nil {
			return false, "", err
		}
		return len(pods.Items) == 0, fmt.Sprintf("waiting for %s/%s to stop (%d pods)", ns, name, len(pods.Items)), nil
	})
}

// dropAll deletes every object of gvr after dropping its finalizers;
// orphan leaves what it owns in place.
func dropAll(ctx context.Context, kc *kube.Client, gvr schema.GroupVersionResource, orphan bool) error {
	l, err := kc.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, o := range l.Items {
		ri := resourceFor(kc, gvr, o.GetNamespace())
		if err := dropFinalizers(ctx, ri, &o); err != nil {
			return err
		}
		if err := deleteOne(ctx, ri, o.GetName(), orphan); err != nil {
			return err
		}
	}
	return nil
}

func dropFinalizers(ctx context.Context, ri dynamic.ResourceInterface, o *unstructured.Unstructured) error {
	if len(o.GetFinalizers()) == 0 {
		return nil
	}
	_, err := ri.Patch(ctx, o.GetName(), types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`), metav1.PatchOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func deleteOne(ctx context.Context, ri dynamic.ResourceInterface, name string, orphan bool) error {
	opts := metav1.DeleteOptions{}
	if orphan {
		p := metav1.DeletePropagationOrphan
		opts.PropagationPolicy = &p
	}
	if err := ri.Delete(ctx, name, opts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}

// servedGVR is the resource a CRD serves, at its storage version.
func servedGVR(crd *unstructured.Unstructured) (schema.GroupVersionResource, bool) {
	group, _, _ := unstructured.NestedString(crd.Object, "spec", "group")
	plural, _, _ := unstructured.NestedString(crd.Object, "spec", "names", "plural")
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	for _, v := range versions {
		m, _ := v.(map[string]any)
		if served, _ := m["served"].(bool); served {
			name, _ := m["name"].(string)
			return schema.GroupVersionResource{Group: group, Version: name, Resource: plural}, true
		}
	}
	return schema.GroupVersionResource{}, false
}

// fleetManaged reports whether Fleet made o (an agent's access to its
// bundles, for one), whatever it's called.
func fleetManaged(o *unstructured.Unstructured) bool {
	for k := range o.GetLabels() {
		if strings.HasPrefix(k, "fleet.cattle.io/") {
			return true
		}
	}
	return false
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

var coreProviderGVR = schema.GroupVersionResource{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: "coreproviders"}

// operatorOwnsCore reports whether the CAPI Operator has a CoreProvider.
func operatorOwnsCore(ctx context.Context, kc *kube.Client) bool {
	l, err := kc.Dynamic.Resource(coreProviderGVR).List(ctx, metav1.ListOptions{})
	return err == nil && len(l.Items) > 0
}

// runningProviderNamespace is where a provider's controller runs (by its
// cluster.x-k8s.io/provider label), or def.
func runningProviderNamespace(ctx context.Context, kc *kube.Client, provider, def string) string {
	l, err := kc.Dynamic.Resource(deploymentGVR).List(ctx, metav1.ListOptions{LabelSelector: "cluster.x-k8s.io/provider=" + provider})
	if err != nil || len(l.Items) == 0 {
		return def
	}
	return l.Items[0].GetNamespace()
}

// providerLabels maps a running controller's cluster.x-k8s.io/provider
// label to the known provider hyve adopts it as.
var providerLabels = map[string]string{
	"infrastructure-docker": "docker",
	"bootstrap-kubeadm":     "kubeadm",
	"control-plane-kubeadm": "kubeadm",
}

var operatorProviderGVRs = []schema.GroupVersionResource{
	coreProviderGVR,
	{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: "bootstrapproviders"},
	{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: "controlplaneproviders"},
	{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: "infrastructureproviders"},
	{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: "addonproviders"},
}

// adoptProviders gives the CAPI Operator the providers that run without
// one of its provider objects (Turtles', after Rancher's removal), so
// they're upgraded with the rest. Known ones are adopted where they run;
// others are named, for 'hyve provider add'.
func adoptProviders(ctx context.Context, kc *kube.Client, logf func(string, ...any)) ([]string, error) {
	owned := map[string]bool{}
	for _, gvr := range operatorProviderGVRs {
		l, err := kc.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, p := range l.Items {
			owned[p.GetNamespace()] = true
		}
	}
	running, err := kc.Dynamic.Resource(deploymentGVR).List(ctx, metav1.ListOptions{LabelSelector: "cluster.x-k8s.io/provider"})
	if err != nil {
		return nil, err
	}
	var adopted []string
	seen := map[string]bool{}
	for _, d := range running.Items {
		if owned[d.GetNamespace()] {
			continue
		}
		label := d.GetLabels()["cluster.x-k8s.io/provider"]
		name, known := providerLabels[label]
		if !known {
			logf("Provider %s runs in %s without the CAPI Operator: hyve doesn't know it, so it isn't upgraded with the rest; declare it with 'hyve provider add'", label, d.GetNamespace())
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		logf("Adopting the %s provider", name)
		objs, err := ProviderObjects(name)
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			if err := kc.ApplyObject(ctx, o); err != nil {
				return nil, err
			}
		}
		adopted = append(adopted, "the "+name+" provider")
	}
	return adopted, nil
}
