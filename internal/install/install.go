// Package install puts hyve on a management cluster (HYVE-PROPOSAL.md,
// "Three ways to run, one model"): with Rancher, just hyve-controller;
// without it, also what Rancher would have brought, each only if missing:
// cert-manager, the Cluster API Operator with core Cluster API, CAAPF
// (which installs Fleet), and hyve's host endpoint, the Rancher host the
// child clusters' agents dial.
package install

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/kube"
)

// Pinned versions. cert-manager's and the CAPI Operator's manifests are
// embedded (hack/update-install-assets.sh); the operator fetches CAPI and
// CAAPF at these versions, and CAAPF installs Fleet's chart.
const (
	CAPIVersion  = "v1.12.11"
	CAAPFVersion = "v0.15.1"
	FleetVersion = "0.14.11"
)

var (
	//go:embed manifests/cert-manager.yaml
	certManagerManifest []byte
	//go:embed manifests/capi-operator.yaml
	capiOperatorManifest []byte

	deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	podGVR        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

	fleetAddonConfigGVR = schema.GroupVersionResource{Group: "addons.cluster.x-k8s.io", Version: "v1alpha1", Resource: "fleetaddonconfigs"}
	secretGVR           = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	getOpts             = metav1.GetOptions{}
	crdGVR              = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
)

// Options for Install.
type Options struct {
	// Namespace hyve runs in.
	Namespace string
	// Image is hyve's image (repository:tag); empty uses the chart's.
	Image      string
	PullPolicy string
	// HostURL is where child clusters' agents, and users, reach the host
	// endpoint (https://...). Standalone only; empty installs no host.
	HostURL string
	// HostTLSSecret is a kubernetes.io/tls Secret in Namespace for the host
	// to serve; empty serves plain HTTP behind a TLS-terminating proxy.
	HostTLSSecret string
	// HostCACerts is the PEM CA the host's certificate chains to, when it
	// isn't publicly trusted.
	HostCACerts string
	// Values are extra chart values, merged last.
	Values map[string]any
	// Lean installs Cluster API and CAAPF from their release manifests,
	// without the CAPI Operator or cert-manager (local mode).
	Lean bool
	GOGC string
	// OnlyHyve installs hyve's chart and nothing else (tests, or clusters
	// whose admins install the rest themselves).
	OnlyHyve bool
	Log      func(format string, args ...any)
}

// Version is this binary's release version, set by cmd; "dev" (or empty)
// is a build from source, which has no published image.
var Version = "dev"

// DefaultImage is the published image for this release.
const DefaultImage = "ghcr.io/cbridges1/hyve"

// DevImage is the image a source checkout builds for itself (`task image`).
const DevImage = "hyve:dev"

// IsDevBuild says whether this binary was built from source.
func IsDevBuild() bool { return Version == "" || Version == "dev" }

// ErrNoImage is Install's refusal to deploy a dev build without an image.
var ErrNoImage = errors.New("this hyve is a development build, which has no published image: " +
	"build one from the checkout (docker build -t " + DevImage + " .) and pass --image " + DevImage)

// Result says what Install found and did.
type Result struct {
	Rancher   bool
	Installed []string
}

// Install installs hyve and, without Rancher, whatever it needs that's
// missing. Safe to re-run.
func Install(ctx context.Context, kc *kube.Client, o Options) (*Result, error) {
	if o.Image == "" && IsDevBuild() {
		return nil, ErrNoImage
	}
	if o.Namespace == "" {
		o.Namespace = "hyve-system"
	}
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	res := &Result{}
	res.Rancher = hasCRD(ctx, kc, "clusters.management.cattle.io")
	if res.Rancher {
		logf("Rancher is installed: it brings Cluster API (Turtles) and Fleet, and is the agents' host")
		if !hasCRD(ctx, kc, "clusters.cluster.x-k8s.io") {
			return nil, fmt.Errorf("Rancher is installed but Cluster API isn't: install Rancher Turtles first")
		}
		if !o.OnlyHyve {
			// Before anything that gets Turtles' Cluster API ready.
			if err := skipKeyRotationMigration(ctx, kc); err != nil {
				return res, err
			}
			if err := adoptIntoTurtles(ctx, kc, logf); err != nil {
				return res, fmt.Errorf("handing Cluster API to Turtles: %w", err)
			}
			if err := ensureRancherCAAPF(ctx, kc, logf); err != nil {
				return res, err
			}
		}
	} else if o.OnlyHyve {
		logf("Installing only hyve; cert-manager, Cluster API, CAAPF and Fleet are left to you")
	} else {
		type step struct {
			name, crd string
			run       func() error
		}
		var steps []step
		if o.Lean {
			steps = []step{
				{"Cluster API " + CAPIVersion, "clusters.cluster.x-k8s.io", func() error {
					return LeanInstall(ctx, kc, []string{"core"}, LeanOptions{GOGC: o.GOGC, Log: logf})
				}},
				{"CAAPF " + CAAPFVersion, "fleetaddonconfigs.addons.cluster.x-k8s.io", func() error {
					return LeanInstall(ctx, kc, []string{"caapf"}, LeanOptions{GOGC: o.GOGC, Log: logf})
				}},
			}
		} else {
			steps = []step{
				{"cert-manager", "certificates.cert-manager.io", func() error {
					if err := applyManifest(ctx, kc, certManagerManifest); err != nil {
						return err
					}
					return waitDeployments(ctx, kc, "cert-manager", logf, "cert-manager", "cert-manager-webhook", "cert-manager-cainjector")
				}},
				{"the Cluster API Operator", "coreproviders.operator.cluster.x-k8s.io", func() error {
					if err := applyRetrying(ctx, kc, capiOperatorManifest, logf); err != nil {
						return err
					}
					return waitDeployments(ctx, kc, "capi-operator-system", logf, "capi-operator-controller-manager")
				}},
				{"Cluster API " + CAPIVersion, "clusters.cluster.x-k8s.io", func() error {
					if err := applyRetrying(ctx, kc, []byte(providerManifest("CoreProvider", "cluster-api", "capi-system", CAPIVersion)), logf); err != nil {
						return err
					}
					return waitCRD(ctx, kc, "clusters.cluster.x-k8s.io", logf)
				}},
				{"CAAPF " + CAAPFVersion, "fleetaddonconfigs.addons.cluster.x-k8s.io", func() error {
					if err := applyRetrying(ctx, kc, []byte(providerManifest("AddonProvider", "rancher-fleet", "caapf-system", CAAPFVersion)), logf); err != nil {
						return err
					}
					return waitCRD(ctx, kc, "fleetaddonconfigs.addons.cluster.x-k8s.io", logf)
				}},
			}
		}
		for _, s := range steps {
			if hasCRD(ctx, kc, s.crd) {
				logf("%s: already installed", s.name)
				continue
			}
			logf("Installing %s", s.name)
			if err := s.run(); err != nil {
				return res, fmt.Errorf("installing %s: %w", s.name, err)
			}
			res.Installed = append(res.Installed, s.name)
		}
		if err := applyFleetConfig(ctx, kc, o, logf); err != nil {
			return res, err
		}
		// For Rancher added later: Turtles reads use-caapf when Rancher
		// installs it, and imports this install's clusters straight away.
		// Rancher registers a cluster with Fleet itself if it imports it
		// before use-caapf is on, and can't be made to stop, so set it now.
		if err := prepareForRancher(ctx, kc); err != nil {
			return res, err
		}
	}

	values := map[string]any{}
	if o.Image == "" {
		o.Image = DefaultImage + ":" + Version
	}
	if o.Image != "" {
		repo, tag, _ := strings.Cut(o.Image, ":")
		img := map[string]any{"repository": repo}
		if tag != "" {
			img["tag"] = tag
		}
		if o.PullPolicy != "" {
			img["pullPolicy"] = o.PullPolicy
		}
		values["image"] = img
	}
	values["namespace"] = o.Namespace
	if res.Rancher {
		values["rancher"] = map[string]any{"enabled": true}
	}
	if !res.Rancher && o.HostURL != "" {
		host := map[string]any{"enabled": true, "agentURL": o.HostURL}
		if o.HostTLSSecret != "" {
			host["tlsSecret"] = o.HostTLSSecret
		}
		if o.HostCACerts != "" {
			host["caCertsConfigMap"] = "hyve-cacerts"
		}
		values["host"] = host
	}
	mergeValues(values, o.Values)
	logf("Installing hyve in %s", o.Namespace)
	if err := kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": o.Namespace},
	}}); err != nil {
		return res, err
	}
	if o.HostCACerts != "" {
		if err := kc.ApplyObject(ctx, caConfigMap(o.Namespace, o.HostCACerts)); err != nil {
			return res, err
		}
	}
	manifest, err := RenderChart(o.Namespace, values)
	if err != nil {
		return res, err
	}
	if err := applyRetrying(ctx, kc, manifest, logf); err != nil {
		return res, err
	}
	if res.Rancher {
		if err := pruneStandalone(ctx, kc, o.Namespace, values, logf); err != nil {
			return res, err
		}
	}
	if err := ensureDefaultNamespaces(ctx, kc); err != nil {
		return res, err
	}
	names := []string{"hyve-controller"}
	if h, ok := values["host"].(map[string]any); ok && h["enabled"] == true {
		names = append(names, "hyve-host")
	}
	if err := waitDeployments(ctx, kc, o.Namespace, logf, names...); err != nil {
		return res, err
	}
	res.Installed = append(res.Installed, "hyve")
	return res, nil
}

func caConfigMap(ns, pem string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "hyve-cacerts", "namespace": ns},
		"data":     map[string]any{"ca.crt": pem},
	}}
}

// FeatureVariables are the clusterctl variables every provider hyve
// installs gets: ClusterClasses need ClusterTopology, and managed clusters
// (GKE, AKS) need machine pools. Rancher Turtles sets the same.
var FeatureVariables = map[string]string{
	"CLUSTER_TOPOLOGY":         "true",
	"EXP_MACHINE_POOL":         "true",
	"EXP_CLUSTER_RESOURCE_SET": "true",
}

// providerManifest is a CAPI Operator provider object, its namespace and
// the Secret of its variables.
func providerManifest(kind, name, ns, version string) string {
	vars := ""
	for _, k := range []string{"CLUSTER_TOPOLOGY", "EXP_MACHINE_POOL", "EXP_CLUSTER_RESOURCE_SET"} {
		vars += fmt.Sprintf("  %s: %q\n", k, FeatureVariables[k])
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata: {name: %s}
---
apiVersion: v1
kind: Secret
metadata: {name: %s-variables, namespace: %s}
stringData:
%s---
apiVersion: operator.cluster.x-k8s.io/v1alpha2
kind: %s
metadata: {name: %s, namespace: %s}
spec:
  version: %s
  configSecret: {name: %s-variables, namespace: %s}
`, ns, name, ns, vars, kind, name, ns, version, name, ns)
}

// applyFleetConfig points CAAPF at the right Fleet server and has it
// install Fleet when it's missing. Without Rancher, child clusters'
// fleet-agents reach the management cluster through the host's
// /k8s/clusters/local, which passes their service-account tokens through
// (open question 6), so only the host needs to be reachable.
func applyFleetConfig(ctx context.Context, kc *kube.Client, o Options, logf func(string, ...any)) error {
	server := map[string]any{"inferLocal": true}
	if o.HostURL != "" {
		custom := map[string]any{"apiServerUrl": strings.TrimRight(o.HostURL, "/") + "/k8s/clusters/local"}
		if o.HostCACerts != "" {
			if err := kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": o.Namespace},
			}}); err != nil {
				return err
			}
			if err := kc.ApplyObject(ctx, caConfigMap(o.Namespace, o.HostCACerts)); err != nil {
				return err
			}
			custom["apiServerCaConfigRef"] = map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": "hyve-cacerts", "namespace": o.Namespace}
		}
		server = map[string]any{"custom": custom}
	}
	// fleet-agent on the host network, so it runs before the cluster has a
	// CNI and can deliver one: a ClusterResourceSet only reaches clusters
	// in its own namespace, so templates (in their own namespace) put the
	// CNI in their resources instead. applyClassGroup makes CAAPF create
	// the BundleNamespaceMapping that carries a template namespace's
	// bundles to clusters in environments; CAAPF only writes it (and the
	// class's ClusterGroup) with patchResource on.
	// bootstrapLocalCluster registers the management cluster itself with
	// Fleet (fleet-local), so repos can deploy to it: ClusterClasses and
	// their templates, for one. setOwnerReferences makes the CAPI Cluster
	// own its Fleet cluster, which is how a Fleet cluster is deleted with
	// it: CAAPF's own cleanup doesn't, and its default of true only applies
	// when the whole cluster section is left out.
	spec := map[string]any{"config": map[string]any{"server": server, "bootstrapLocalCluster": true}, "cluster": map[string]any{"hostNetwork": true, "applyClassGroup": true, "patchResource": true, "setOwnerReferences": true, "selector": map[string]any{}, "namespaceSelector": map[string]any{}}, "clusterClass": map[string]any{"patchResource": true, "setOwnerReferences": true}}
	// CAAPF installs Fleet if it's missing. Once it has, keep asking it to,
	// or a re-run's apply would drop the field.
	existing, err := kc.Dynamic.Resource(fleetAddonConfigGVR).Get(ctx, "fleet-addon-config", getOpts)
	_, installedByCAAPF, _ := unstructured.NestedMap(existingObject(existing, err), "spec", "install")
	// Agents go where Turtles puts them, so adding Rancher later doesn't
	// move them; an install from before keeps CAAPF's default
	// (agentNamespaceOf).
	if apierrors.IsNotFound(err) {
		spec["cluster"].(map[string]any)["agentNamespace"] = AgentNamespace
	} else if ns, _, _ := unstructured.NestedString(existingObject(existing, err), "spec", "cluster", "agentNamespace"); ns != "" {
		spec["cluster"].(map[string]any)["agentNamespace"] = ns
	}
	if installedByCAAPF || !hasCRD(ctx, kc, "bundles.fleet.cattle.io") {
		if !installedByCAAPF {
			logf("CAAPF will install Fleet %s", FleetVersion)
		}
		spec["install"] = map[string]any{"version": FleetVersion}
	}
	return kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "addons.cluster.x-k8s.io/v1alpha1", "kind": "FleetAddonConfig",
		"metadata": map[string]any{"name": "fleet-addon-config"},
		"spec":     spec,
	}})
}

func existingObject(u *unstructured.Unstructured, err error) map[string]any {
	if err != nil || u == nil {
		return nil
	}
	return u.Object
}

func hasCRD(ctx context.Context, kc *kube.Client, name string) bool {
	_, err := kc.Dynamic.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
	return err == nil
}

// applyManifest applies a manifest's CRDs first, then everything else.
func applyManifest(ctx context.Context, kc *kube.Client, manifest []byte) error {
	objs, err := kube.Decode(manifest)
	if err != nil {
		return err
	}
	for _, first := range []bool{true, false} {
		for _, o := range objs {
			if (o.GetKind() == "CustomResourceDefinition" || o.GetKind() == "Namespace") != first {
				continue
			}
			if err := kc.ApplyObject(ctx, o); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyRetrying applies a manifest until it goes through: new CRDs take a
// moment to be served, and webhooks a moment to answer.
func applyRetrying(ctx context.Context, kc *kube.Client, manifest []byte, logf func(string, ...any)) error {
	return kube.Poll(ctx, 5*time.Second, 5*time.Minute, func(m string) { logf("  %s", m) }, func(ctx context.Context) (bool, string, error) {
		if err := applyManifest(ctx, kc, manifest); err != nil {
			return false, "waiting to apply: " + firstLine(err.Error()), nil
		}
		return true, "", nil
	})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func waitCRD(ctx context.Context, kc *kube.Client, name string, logf func(string, ...any)) error {
	return kube.Poll(ctx, 5*time.Second, 10*time.Minute, func(m string) { logf("  %s", m) }, func(ctx context.Context) (bool, string, error) {
		return hasCRD(ctx, kc, name), "waiting for " + name, nil
	})
}

func waitDeployments(ctx context.Context, kc *kube.Client, ns string, logf func(string, ...any), names ...string) error {
	return kube.Poll(ctx, 5*time.Second, 10*time.Minute, func(m string) { logf("  %s", m) }, func(ctx context.Context) (bool, string, error) {
		for _, n := range names {
			d, err := kc.Dynamic.Resource(deploymentGVR).Namespace(ns).Get(ctx, n, metav1.GetOptions{})
			if err != nil {
				return false, fmt.Sprintf("waiting for %s/%s", ns, n), nil
			}
			want, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas")
			ready, _, _ := unstructured.NestedInt64(d.Object, "status", "availableReplicas")
			if ready < want || want == 0 {
				if err := pullFailure(ctx, kc, ns, d); err != nil {
					return false, "", fmt.Errorf("%s/%s: %w", ns, n, err)
				}
				return false, fmt.Sprintf("waiting for %s/%s (%d/%d)", ns, n, ready, want), nil
			}
		}
		return true, "", nil
	})
}

// pullFailure reports a Deployment's pod whose image doesn't exist or
// can't be had, which waiting won't fix. Other pull errors may be
// transient, so they're left to the timeout.
func pullFailure(ctx context.Context, kc *kube.Client, ns string, d *unstructured.Unstructured) error {
	sel, _, _ := unstructured.NestedStringMap(d.Object, "spec", "selector", "matchLabels")
	if len(sel) == 0 {
		return nil
	}
	pods, err := kc.Dynamic.Resource(podGVR).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(sel).String()})
	if err != nil {
		return nil
	}
	// Pods of an older rollout may be failing on an image that's since
	// changed; only the current ones count.
	current := map[string]bool{}
	containers, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")
	for _, c := range containers {
		if c, ok := c.(map[string]any); ok {
			image, _ := c["image"].(string)
			current[image] = true
		}
	}
	for _, p := range pods.Items {
		// The status's image may be normalized (docker.io/library/...);
		// the pod's spec has it as the Deployment does.
		specImage := map[string]string{}
		podContainers, _, _ := unstructured.NestedSlice(p.Object, "spec", "containers")
		for _, c := range podContainers {
			if c, ok := c.(map[string]any); ok {
				name, _ := c["name"].(string)
				specImage[name], _ = c["image"].(string)
			}
		}
		statuses, _, _ := unstructured.NestedSlice(p.Object, "status", "containerStatuses")
		for _, c := range statuses {
			c, _ := c.(map[string]any)
			reason, _, _ := unstructured.NestedString(c, "state", "waiting", "reason")
			msg, _, _ := unstructured.NestedString(c, "state", "waiting", "message")
			lower := strings.ToLower(msg)
			permanent := strings.Contains(lower, "not found") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "denied")
			name, _, _ := unstructured.NestedString(c, "name")
			image := specImage[name]
			if !current[image] {
				continue
			}
			if reason == "InvalidImageName" || ((reason == "ErrImagePull" || reason == "ImagePullBackOff") && permanent) {
				return fmt.Errorf("can't pull %s (%s): %s", image, reason, msg)
			}
		}
	}
	return nil
}

// providerSet is the CAPI Operator providers a named provider needs.
type providerSet []struct{ kind, name, ns, version string }

// knownProviders are what `hyve local up --providers` (and lazily, local
// mode) knows how to add. kubeadm comes with docker, whose ClusterClasses
// are kubeadm-based.
var knownProviders = map[string]providerSet{
	"docker": {
		{"BootstrapProvider", "kubeadm", "capi-kubeadm-bootstrap-system", CAPIVersion},
		{"ControlPlaneProvider", "kubeadm", "capi-kubeadm-control-plane-system", CAPIVersion},
		{"InfrastructureProvider", "docker", "capd-system", CAPIVersion},
	},
	"kubeadm": {
		{"BootstrapProvider", "kubeadm", "capi-kubeadm-bootstrap-system", CAPIVersion},
		{"ControlPlaneProvider", "kubeadm", "capi-kubeadm-control-plane-system", CAPIVersion},
	},
}

// ProviderObjects returns the objects that install a known provider
// through the CAPI Operator, with hyve's feature variables.
func ProviderObjects(name string) ([]*unstructured.Unstructured, error) {
	set, ok := knownProviders[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (known: docker, kubeadm); add others with 'hyve provider add'", name)
	}
	var out []*unstructured.Unstructured
	for _, p := range set {
		objs, err := kube.Decode([]byte(providerManifest(p.kind, p.name, p.ns, p.version)))
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}
	return out, nil
}

// The namespaces the CLI uses when none is given: the default environment
// and the namespace for templates and shared resources.
const (
	DefaultEnvironment       = "default"
	DefaultTemplateNamespace = "capi-clusters"
	LabelEnvironment         = "hyve.io/environment"
)

// ensureDefaultNamespaces makes the default environment one (labelled, so
// it's listed and hyve's cluster webhook covers it) and creates the
// template namespace, so a new install is usable without setting up either.
func ensureDefaultNamespaces(ctx context.Context, kc *kube.Client) error {
	for _, o := range []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{
			"name": DefaultEnvironment, "labels": map[string]any{LabelEnvironment: "true"}}},
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": DefaultTemplateNamespace}},
	} {
		if err := kc.ApplyObject(ctx, &unstructured.Unstructured{Object: o}); err != nil {
			return err
		}
	}
	return nil
}
