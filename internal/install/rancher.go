package install

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/kube"
)

// With Rancher, CAAPF is still the one thing that registers clusters with
// Fleet (HYVE-PROPOSAL.md, "One registration path"): hyve's template
// resources target the Fleet Cluster CAAPF makes, named after the CAPI
// Cluster. Rancher 2.14 doesn't install CAAPF, and Turtles registers
// clusters with Fleet itself unless its use-caapf feature is on.

var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

const (
	// Rancher reads extra values for the charts it installs, Turtles among
	// them, from this ConfigMap in cattle-system, one YAML document per
	// chart name (rancher/rancher pkg/controllers/dashboard/chart).
	rancherConfigNS   = "cattle-system"
	rancherConfigName = "rancher-config"
	turtlesChartKey   = "rancher-turtles"
	// Where Turtles' own providers chart puts CAAPF.
	caapfRancherNS = "fleet-addon-system"
)

// ensureRancherCAAPF turns on Turtles' use-caapf and installs CAAPF as a
// Turtles CAPIProvider if it's missing. Safe to re-run.
func ensureRancherCAAPF(ctx context.Context, kc *kube.Client, logf func(string, ...any)) error {
	changed, err := setTurtlesUseCAAPF(ctx, kc)
	if err != nil {
		return fmt.Errorf("turning on Turtles' use-caapf: %w", err)
	}
	if changed {
		logf("Turned on Turtles' use-caapf: Rancher reinstalls Turtles with it, and clusters it already imported get their fleet-agent from CAAPF")
	} else {
		logf("Turtles' use-caapf: already on")
	}

	if hasCRD(ctx, kc, "fleetaddonconfigs.addons.cluster.x-k8s.io") {
		logf("CAAPF: already installed")
		// A standalone install's configuration has CAAPF install Fleet and
		// point agents at hyve's host; Rancher's Fleet replaces both.
		existing, err := kc.Dynamic.Resource(fleetAddonConfigGVR).Get(ctx, "fleet-addon-config", getOpts)
		obj := existingObject(existing, err)
		_, hasInstall, _ := unstructured.NestedMap(obj, "spec", "install")
		_, hasServer, _ := unstructured.NestedMap(obj, "spec", "config", "server")
		if hasInstall || hasServer {
			logf("Switching CAAPF to Rancher's Fleet")
			return kc.ApplyObject(ctx, rancherFleetAddonConfig(agentNamespaceOf(obj)))
		}
		return nil
	}
	logf("Installing CAAPF through Turtles")
	for _, o := range []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": caapfRancherNS}},
		{"apiVersion": "turtles-capi.cattle.io/v1alpha1", "kind": "CAPIProvider",
			"metadata": map[string]any{"name": "fleet", "namespace": caapfRancherNS},
			"spec":     map[string]any{"name": "rancher-fleet", "type": "addon"}},
	} {
		if err := kc.ApplyObject(ctx, &unstructured.Unstructured{Object: o}); err != nil {
			return err
		}
	}
	if err := waitCRD(ctx, kc, "fleetaddonconfigs.addons.cluster.x-k8s.io", logf); err != nil {
		return err
	}
	return kc.ApplyObject(ctx, rancherFleetAddonConfig(AgentNamespace))
}

// AgentNamespace is where CAAPF puts each cluster's fleet-agent, the same
// as Turtles does. CAAPF's own default is caapfDefaultAgentNamespace.
const (
	AgentNamespace             = "cattle-fleet-system"
	caapfDefaultAgentNamespace = "fleet-addon-agent"
)

// agentNamespaceOf is the agent namespace an existing FleetAddonConfig
// uses. It never changes once clusters are registered: Fleet records it in
// the ownership of everything an agent deploys, so an agent moved to
// another namespace no longer owns what it deployed.
func agentNamespaceOf(obj map[string]any) string {
	if ns, _, _ := unstructured.NestedString(obj, "spec", "cluster", "agentNamespace"); ns != "" {
		return ns
	}
	return caapfDefaultAgentNamespace
}

// rancherFleetAddonConfig is Turtles' own CAAPF configuration (its
// providers chart's), with what hyve needs on top: every cluster Turtles
// imports, and a template namespace's bundles carried to clusters in other
// namespaces (applyClassGroup). Fleet itself is Rancher's, so CAAPF
// doesn't install it.
func rancherFleetAddonConfig(agentNamespace string) *unstructured.Unstructured {
	autoImport := map[string]any{"matchLabels": map[string]any{"cluster-api.cattle.io/rancher-auto-import": "true"}}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "addons.cluster.x-k8s.io/v1alpha1", "kind": "FleetAddonConfig",
		"metadata": map[string]any{"name": "fleet-addon-config"},
		"spec": map[string]any{
			"config": map[string]any{
				"featureGates": map[string]any{
					"configMap":              map[string]any{"ref": map[string]any{"kind": "ConfigMap", "apiVersion": "v1", "name": rancherConfigName, "namespace": rancherConfigNS}},
					"experimentalOciStorage": true,
					"experimentalHelmOps":    true,
				},
			},
			"clusterClass": map[string]any{"patchResource": true, "setOwnerReferences": true},
			"cluster": map[string]any{
				"agentNamespace":     agentNamespace,
				"applyClassGroup":    true,
				"patchResource":      true,
				"setOwnerReferences": true,
				"hostNetwork":        true,
				"selector":           autoImport,
				"namespaceSelector":  map[string]any{},
			},
		},
	}}
}

// prepareForRancher writes Turtles' use-caapf into rancher-config on a
// standalone install, ready for Rancher being added later.
func prepareForRancher(ctx context.Context, kc *kube.Client) error {
	if err := kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": rancherConfigNS},
	}}); err != nil {
		return err
	}
	if _, err := setTurtlesUseCAAPF(ctx, kc); err != nil {
		return err
	}
	return skipKeyRotationMigration(ctx, kc)
}

var rkeControlPlaneGVR = schema.GroupVersionResource{Group: "rke.cattle.io", Version: "v1", Resource: "rkecontrolplanes"}

// skipKeyRotationMigration marks one of Rancher's one-time migrations done
// when there's nothing for it to migrate. Rancher 2.14 runs it once Cluster
// API is ready, and it looks up each management cluster's RKE2 control
// plane in the cluster's Fleet workspace (pkg/rancher/migrations.go,
// migrateEncryptionKeyRotationLeader). With use-caapf, Turtles imports
// clusters without a Fleet workspace, so on a cluster where hyve's clusters
// exist before Rancher's first start that lookup fails and Rancher exits,
// every start. The migration only moves an annotation on RKE2 control
// planes, so with none of those, it has nothing to do.
func skipKeyRotationMigration(ctx context.Context, kc *kube.Client) error {
	const name, key = "migrateencryptionkeyrotationleadertostatus", "encryptionKeyRotationStatusMigrated"
	ri := kc.Dynamic.Resource(configMapGVR).Namespace(rancherConfigNS)
	if cm, err := ri.Get(ctx, name, metav1.GetOptions{}); err == nil {
		if v, _, _ := unstructured.NestedString(cm.Object, "data", key); v == "true" {
			return nil
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	if hasCRD(ctx, kc, "rkecontrolplanes.rke.cattle.io") {
		list, err := kc.Dynamic.Resource(rkeControlPlaneGVR).List(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			return err
		}
		if len(list.Items) > 0 {
			return nil
		}
	}
	return kc.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": rancherConfigNS},
		"data":     map[string]any{key: "true"},
	}})
}

// setTurtlesUseCAAPF sets features.use-caapf.enabled in the Turtles values
// Rancher merges in, keeping everything else there. It reports whether it
// changed anything.
func setTurtlesUseCAAPF(ctx context.Context, kc *kube.Client) (bool, error) {
	ri := kc.Dynamic.Resource(configMapGVR).Namespace(rancherConfigNS)
	cm, err := ri.Get(ctx, rancherConfigName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		cm = nil
	} else if err != nil {
		return false, err
	}
	values := map[string]any{}
	if cm != nil {
		raw, _, _ := unstructured.NestedString(cm.Object, "data", turtlesChartKey)
		if raw != "" {
			if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
				return false, fmt.Errorf("%s/%s %s isn't YAML: %w", rancherConfigNS, rancherConfigName, turtlesChartKey, err)
			}
		}
	}
	if on, _, _ := unstructured.NestedBool(values, "features", "use-caapf", "enabled"); on {
		return false, nil
	}
	if err := unstructured.SetNestedField(values, true, "features", "use-caapf", "enabled"); err != nil {
		return false, err
	}
	out, err := yaml.Marshal(values)
	if err != nil {
		return false, err
	}
	if cm == nil {
		_, err = ri.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": rancherConfigName, "namespace": rancherConfigNS},
			"data":     map[string]any{turtlesChartKey: string(out)},
		}}, metav1.CreateOptions{})
		return err == nil, err
	}
	// Only this key: the ConfigMap holds other charts' values too.
	patch, err := json.Marshal(map[string]any{"data": map[string]any{turtlesChartKey: string(out)}})
	if err != nil {
		return false, err
	}
	_, err = ri.Patch(ctx, rancherConfigName, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: kube.FieldManager})
	return err == nil, err
}
