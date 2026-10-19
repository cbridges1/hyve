package install

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/kube"
)

// Adding Rancher to a standalone install (HYVE-PROPOSAL.md, open question
// 3). Rancher's Fleet chart upgrades the Fleet CAAPF installed, and Turtles
// imports the existing clusters, but Turtles doesn't adopt Cluster API: it
// installs its own core next to the CAPI Operator's, and two controllers
// then reconcile every Cluster. So when hyve finds Rancher on a cluster
// where it installed Cluster API through the operator, it hands each
// provider to Turtles: the operator deletes a provider's controllers but
// never its CRDs (cluster-api-operator phase_lifecycle.go, IncludeCRDs:
// false), so clusters and machines stay, and Turtles installs the same
// provider again as a CAPIProvider. Then the operator goes.

var (
	turtlesProviderGVR = schema.GroupVersionResource{Group: "turtles-capi.cattle.io", Version: "v1alpha1", Resource: "capiproviders"}
	// The operator's provider kinds, core last: it's handed over once the
	// others are.
	operatorProviders = []struct{ resource, turtlesType string }{
		{"bootstrapproviders", "bootstrap"},
		{"controlplaneproviders", "controlPlane"},
		{"infrastructureproviders", "infrastructure"},
		{"addonproviders", "addon"},
		{"ipamproviders", "ipam"},
		{"runtimeextensionproviders", "runtimeExtension"},
		{"coreproviders", "core"},
	}
)

func operatorGVR(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: resource}
}

// Providers Turtles knows how to install without a fetch URL.
var turtlesKnows = map[string]bool{"kubeadm": true, "docker": true, "rancher-fleet": true}

// adoptIntoTurtles hands the CAPI Operator's providers to Turtles and
// removes the operator. It does nothing on a cluster without the operator.
func adoptIntoTurtles(ctx context.Context, kc *kube.Client, logf func(string, ...any)) error {
	if !hasCRD(ctx, kc, "coreproviders.operator.cluster.x-k8s.io") {
		return nil
	}
	logf("Cluster API was installed by the CAPI Operator, before Rancher: handing its providers to Turtles")
	coreVersion, err := waitTurtlesCore(ctx, kc, logf)
	if err != nil {
		return err
	}
	for _, p := range operatorProviders {
		list, err := kc.Dynamic.Resource(operatorGVR(p.resource)).List(ctx, metav1.ListOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		for i := range list.Items {
			if err := adoptProvider(ctx, kc, &list.Items[i], p.turtlesType, coreVersion, logf); err != nil {
				return err
			}
		}
	}
	return removeOperator(ctx, kc, logf)
}

// waitTurtlesCore waits for the core Cluster API Turtles installs for
// Rancher, and returns its version.
func waitTurtlesCore(ctx context.Context, kc *kube.Client, logf func(string, ...any)) (string, error) {
	version := ""
	err := kube.Poll(ctx, 5*time.Second, 10*time.Minute, func(m string) { logf("  %s", m) }, func(ctx context.Context) (bool, string, error) {
		list, err := kc.Dynamic.Resource(turtlesProviderGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, "waiting for Turtles", nil
		}
		for _, p := range list.Items {
			if t, _, _ := unstructured.NestedString(p.Object, "spec", "type"); t == "core" {
				phase, _, _ := unstructured.NestedString(p.Object, "status", "phase")
				version, _, _ = unstructured.NestedString(p.Object, "status", "installedVersion")
				return phase == "Ready", "waiting for Turtles' core Cluster API", nil
			}
		}
		return false, "waiting for Turtles to install core Cluster API", nil
	})
	return version, err
}

func adoptProvider(ctx context.Context, kc *kube.Client, op *unstructured.Unstructured, typ, coreVersion string, logf func(string, ...any)) error {
	name, ns := op.GetName(), op.GetNamespace()
	ri := kc.Dynamic.Resource(operatorGVR(strings.ToLower(op.GetKind()) + "s")).Namespace(ns)
	logf("  %s provider %s: from the CAPI Operator to Turtles", typ, name)
	if err := ri.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	// The operator removes the provider's controllers, then the object.
	if err := kube.Poll(ctx, 3*time.Second, 5*time.Minute, func(string) {}, func(ctx context.Context) (bool, string, error) {
		_, err := ri.Get(ctx, name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), "", nil
	}); err != nil {
		return fmt.Errorf("waiting for the CAPI Operator to let go of %s: %w", name, err)
	}
	if typ == "core" {
		// Turtles already runs its own core. Removing the operator's took
		// webhook configurations both used: have Turtles apply its again.
		return nudgeTurtlesCore(ctx, kc)
	}
	spec := map[string]any{"name": name, "type": typ}
	// kubeadm and CAPD release with core Cluster API: keep them at Turtles'
	// core version (left alone, Turtles takes the latest).
	if (name == "kubeadm" || name == "docker") && coreVersion != "" {
		spec["version"] = coreVersion
	}
	if !turtlesKnows[name] {
		if v, ok, _ := unstructured.NestedString(op.Object, "spec", "version"); ok && v != "" {
			spec["version"] = v
		}
		if f, ok, _ := unstructured.NestedMap(op.Object, "spec", "fetchConfig"); ok {
			spec["fetchConfig"] = f
		}
	}
	if cs, ok, _ := unstructured.NestedString(op.Object, "spec", "configSecret", "name"); ok && cs != "" {
		spec["configSecret"] = map[string]any{"name": cs}
	}
	return kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "turtles-capi.cattle.io/v1alpha1", "kind": "CAPIProvider",
		"metadata": map[string]any{"name": turtlesProviderName(name, typ), "namespace": ns},
		"spec":     spec,
	}})
}

// turtlesProviderName follows Turtles' own names: kubeadm-bootstrap,
// kubeadm-control-plane, fleet; otherwise the provider's name.
func turtlesProviderName(name, typ string) string {
	switch {
	case name == "kubeadm" && typ == "bootstrap":
		return "kubeadm-bootstrap"
	case name == "kubeadm" && typ == "controlPlane":
		return "kubeadm-control-plane"
	case name == "rancher-fleet":
		return "fleet"
	}
	return name
}

func nudgeTurtlesCore(ctx context.Context, kc *kube.Client) error {
	list, err := kc.Dynamic.Resource(turtlesProviderGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		p := &list.Items[i]
		if t, _, _ := unstructured.NestedString(p.Object, "spec", "type"); t != "core" {
			continue
		}
		a := p.GetAnnotations()
		if a == nil {
			a = map[string]string{}
		}
		a["hyve.io/adopted-at"] = time.Now().UTC().Format(time.RFC3339)
		p.SetAnnotations(a)
		_, err := kc.Dynamic.Resource(turtlesProviderGVR).Namespace(p.GetNamespace()).Update(ctx, p, metav1.UpdateOptions{})
		return err
	}
	return nil
}

// removeOperator deletes the CAPI Operator hyve installed: everything in
// its manifest, its CRDs last.
func removeOperator(ctx context.Context, kc *kube.Client, logf func(string, ...any)) error {
	objs, err := kube.Decode(capiOperatorManifest)
	if err != nil {
		return err
	}
	logf("  removing the CAPI Operator")
	for _, crds := range []bool{false, true} {
		for _, o := range objs {
			if (o.GetKind() == "CustomResourceDefinition") != crds || o.GetKind() == "Namespace" {
				continue
			}
			if err := kc.DeleteObject(ctx, o); err != nil {
				return err
			}
		}
	}
	return nil
}

// pruneStandalone deletes what hyve's chart renders only without Rancher
// (the host endpoint, hyve's own users and access roles), once Rancher has
// taken over.
func pruneStandalone(ctx context.Context, kc *kube.Client, namespace string, rancherValues map[string]any, logf func(string, ...any)) error {
	if _, err := kc.Dynamic.Resource(deploymentGVR).Namespace(namespace).Get(ctx, "hyve-host", getOpts); apierrors.IsNotFound(err) {
		return nil
	}
	standalone := map[string]any{}
	mergeValues(standalone, rancherValues)
	standalone["rancher"] = map[string]any{"enabled": false}
	standalone["host"] = map[string]any{"enabled": true, "agentURL": "https://hyve.invalid"}
	before, err := RenderChart(namespace, standalone)
	if err != nil {
		return err
	}
	after, err := RenderChart(namespace, rancherValues)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	afterObjs, err := kube.Decode(after)
	if err != nil {
		return err
	}
	for _, o := range afterObjs {
		keep[objectKey(o)] = true
	}
	beforeObjs, err := kube.Decode(before)
	if err != nil {
		return err
	}
	logf("Rancher is the host now: removing hyve's host endpoint")
	for _, o := range beforeObjs {
		if keep[objectKey(o)] || o.GetKind() == "CustomResourceDefinition" || o.GetKind() == "Namespace" {
			continue
		}
		if err := kc.DeleteObject(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

func objectKey(o *unstructured.Unstructured) string {
	return o.GetKind() + "/" + o.GetNamespace() + "/" + o.GetName()
}
