package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/install"
)

// With Rancher, providers are Turtles CAPIProviders; without, the Cluster
// API Operator's provider kinds, one per provider type.
var (
	turtlesProviderGVR = schema.GroupVersionResource{Group: "turtles-capi.cattle.io", Version: "v1alpha1", Resource: "capiproviders"}
	operatorKinds      = map[string]string{
		"core": "CoreProvider", "bootstrap": "BootstrapProvider", "controlPlane": "ControlPlaneProvider",
		"infrastructure": "InfrastructureProvider", "addon": "AddonProvider", "ipam": "IPAMProvider",
		"runtimeExtension": "RuntimeExtensionProvider",
	}
)

func operatorGVR(kind string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2", Resource: strings.ToLower(kind) + "s"}
}

// ProviderCmd is `hyve provider`.
func ProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "provider",
		Aliases: []string{"providers"},
		Short:   "Add, list and remove Cluster API providers",
		Long: `Installs Cluster API providers on the management cluster: as Rancher Turtles
CAPIProviders when Rancher is installed, otherwise through the Cluster API
Operator. Variables are what the provider's clusterctl configuration reads:
credentials and feature flags.`,
	}
	AddGlobalFlags(cmd)
	cmd.AddCommand(providerAddCmd(), providerListCmd(), providerRemoveCmd())
	return cmd
}

func (s *session) turtles() bool {
	return s.kube.HasKind("turtles-capi.cattle.io", "v1alpha1", "CAPIProvider")
}

func providerAddCmd() *cobra.Command {
	var (
		typ, version, namespace, providerName, fetchURL string
		vars, varFiles, varB64Files                     []string
	)
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Install a Cluster API provider",
		Example: `  hyve provider add docker --version v1.12.0
  hyve provider add gcp --variable-b64-file GCP_B64ENCODED_CREDENTIALS=./key.json --variable EXP_CAPG_GKE=true
  hyve provider add civo --fetch-url https://github.com/cbridges1/cluster-api-provider-civo/releases/latest/download/infrastructure-components.yaml \
      --variable-env CIVO_API_KEY`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			name := args[0]
			if providerName == "" {
				providerName = name
			}
			if namespace == "" {
				namespace = name + "-system"
			}
			// ClusterClasses need ClusterTopology; --variable can override.
			data := map[string]any{}
			for k, v := range install.FeatureVariables {
				data[k] = v
			}
			for _, kv := range vars {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					// KEY alone: from the environment, so it stays out of
					// shell history.
					v, ok = os.LookupEnv(kv)
					if !ok {
						return fmt.Errorf("--variable %s: want KEY=value, or KEY set in the environment", kv)
					}
				}
				data[k] = v
			}
			for _, kv := range append(varFiles, varB64Files...) {
				k, path, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("%q: want KEY=path", kv)
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				v := strings.TrimRight(string(raw), "\n")
				for _, b := range varB64Files {
					if b == kv {
						v = base64.StdEncoding.EncodeToString(raw)
					}
				}
				data[k] = v
			}
			ctx := cmd.Context()
			objs := []*unstructured.Unstructured{{Object: map[string]any{
				"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace},
			}}}
			secret := ""
			if len(data) > 0 {
				secret = name + "-variables"
				objs = append(objs, &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "v1", "kind": "Secret",
					"metadata":   map[string]any{"name": secret, "namespace": namespace},
					"stringData": data,
				}})
			}
			spec := map[string]any{}
			if version != "" {
				spec["version"] = version
			}
			if fetchURL != "" {
				spec["fetchConfig"] = map[string]any{"url": fetchURL}
			}
			var obj *unstructured.Unstructured
			if s.turtles() {
				spec["name"], spec["type"] = providerName, typ
				if secret != "" {
					spec["configSecret"] = map[string]any{"name": secret}
				}
				obj = &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "turtles-capi.cattle.io/v1alpha1", "kind": "CAPIProvider",
					"metadata": map[string]any{"name": name, "namespace": namespace}, "spec": spec,
				}}
			} else {
				kind, ok := operatorKinds[typ]
				if !ok {
					return fmt.Errorf("--type %q: want one of core, bootstrap, controlPlane, infrastructure, addon, ipam, runtimeExtension", typ)
				}
				if !s.kube.HasKind("operator.cluster.x-k8s.io", "v1alpha2", kind) {
					return fmt.Errorf("neither Rancher Turtles nor the Cluster API Operator is installed; the hyve chart installs the operator")
				}
				if secret != "" {
					spec["configSecret"] = map[string]any{"name": secret, "namespace": namespace}
				}
				// The operator names a provider after its object.
				obj = &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "operator.cluster.x-k8s.io/v1alpha2", "kind": kind,
					"metadata": map[string]any{"name": providerName, "namespace": namespace}, "spec": spec,
				}}
			}
			objs = append(objs, obj)
			for _, o := range objs {
				if err := s.kube.ApplyObject(ctx, o); err != nil {
					return err
				}
			}
			logf("Applied %s %s/%s; 'hyve provider list' shows when it's ready", obj.GetKind(), namespace, obj.GetName())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "infrastructure", "infrastructure, bootstrap, controlPlane, core, addon, ipam, runtimeExtension")
	f.StringVar(&version, "version", "", "provider version (default: the latest the installer knows)")
	f.StringVar(&namespace, "namespace", "", "namespace (default: NAME-system)")
	f.StringVar(&providerName, "provider-name", "", "clusterctl provider name, when it differs from NAME")
	f.StringVar(&fetchURL, "fetch-url", "", "where to fetch the provider's components, for providers clusterctl doesn't know")
	f.StringArrayVar(&vars, "variable", nil, "provider variable KEY=value, or KEY to read it from the environment (repeatable)")
	f.StringArrayVar(&varFiles, "variable-file", nil, "provider variable KEY=path, the file's contents (repeatable)")
	f.StringArrayVar(&varB64Files, "variable-b64-file", nil, "provider variable KEY=path, the file's contents base64-encoded (repeatable)")
	return cmd
}

type providerRow struct {
	ns, name, typ, provider, version, phase string
}

func listProviders(cmd *cobra.Command, s *session) ([]providerRow, error) {
	ctx := cmd.Context()
	var rows []providerRow
	get := func(u *unstructured.Unstructured, f ...string) string {
		v, _, _ := unstructured.NestedString(u.Object, f...)
		return v
	}
	if s.turtles() {
		list, err := s.kube.Dynamic.Resource(turtlesProviderGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			p := &list.Items[i]
			rows = append(rows, providerRow{p.GetNamespace(), p.GetName(), get(p, "spec", "type"), get(p, "spec", "name"), get(p, "status", "installedVersion"), get(p, "status", "phase")})
		}
		return rows, nil
	}
	for typ, kind := range operatorKinds {
		list, err := s.kube.Dynamic.Resource(operatorGVR(kind)).List(ctx, metav1.ListOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			p := &list.Items[i]
			phase := "Installing"
			for _, c := range []string{"Ready"} {
				conds, _, _ := unstructured.NestedSlice(p.Object, "status", "conditions")
				for _, x := range conds {
					if m, ok := x.(map[string]any); ok && m["type"] == c {
						if m["status"] == "True" {
							phase = "Ready"
						} else if msg, _ := m["message"].(string); msg != "" {
							phase = msg
						}
					}
				}
			}
			rows = append(rows, providerRow{p.GetNamespace(), p.GetName(), typ, p.GetName(), get(p, "status", "installedVersion"), phase})
		}
	}
	return rows, nil
}

func providerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Cluster API providers and their state",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			rows, err := listProviders(cmd, s)
			if err != nil {
				return err
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].typ+rows[i].name < rows[j].typ+rows[j].name })
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAMESPACE\tNAME\tTYPE\tPROVIDER\tVERSION\tSTATE")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ns, r.name, r.typ, r.provider, dash(r.version), dash(r.phase))
			}
			return w.Flush()
		},
	}
}

func providerRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm"},
		Short:   "Uninstall a Cluster API provider (delete its clusters first)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			rows, err := listProviders(cmd, s)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if r.name != args[0] {
					continue
				}
				gvr := turtlesProviderGVR
				if !s.turtles() {
					gvr = operatorGVR(operatorKinds[r.typ])
				}
				if err := s.kube.Dynamic.Resource(gvr).Namespace(r.ns).Delete(cmd.Context(), r.name, metav1.DeleteOptions{}); err != nil {
					return err
				}
				logf("Removing provider %s/%s", r.ns, r.name)
				return nil
			}
			return fmt.Errorf("no provider %q", args[0])
		},
	}
}
