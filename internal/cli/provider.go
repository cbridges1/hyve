package cli

import (
	"context"
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
	operatorKinds      = install.OperatorKinds
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
	cmd.AddCommand(providerAddCmd(), providerListCmd(), providerShowCmd(), providerRemoveCmd())
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
			data := map[string]any{}
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
			r := install.ProviderRequest{
				Name: name, Version: version, Namespace: namespace, ProviderName: providerName, FetchURL: fetchURL,
				Variables: map[string]string{},
			}
			if cmd.Flags().Changed("type") {
				r.Type = typ
			}
			for k, v := range data {
				r.Variables[k] = fmt.Sprint(v)
			}
			msg, err := install.AddProvider(cmd.Context(), s.kube, r)
			if err != nil {
				return err
			}
			logf("%s; 'hyve provider list' shows when it's ready", msg)
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

// leanProviders lists a lean install's providers from their Deployments,
// which the release manifests label with the provider's clusterctl name
// (infrastructure-docker, control-plane-kubeadm, cluster-api).
func leanProviders(ctx context.Context, s *session) ([]providerRow, error) {
	list, err := s.kube.Dynamic.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).
		List(ctx, metav1.ListOptions{LabelSelector: "cluster.x-k8s.io/provider"})
	if err != nil {
		return nil, err
	}
	var rows []providerRow
	for i := range list.Items {
		d := &list.Items[i]
		full := d.GetLabels()["cluster.x-k8s.io/provider"]
		typ, name := "core", full
		if full == "fleet" {
			// CAAPF labels itself with the bare name.
			typ = "addon"
		}
		for _, t := range []string{"infrastructure", "bootstrap", "control-plane", "addon", "ipam", "runtime-extension"} {
			if n, ok := strings.CutPrefix(full, t+"-"); ok {
				typ, name = t, n
			}
		}
		version := ""
		if cs, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers"); len(cs) > 0 {
			if c, ok := cs[0].(map[string]any); ok {
				img, _ := c["image"].(string)
				if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
					version = img[i+1:]
				}
			}
		}
		phase := "Installing"
		ready, _, _ := unstructured.NestedInt64(d.Object, "status", "availableReplicas")
		if want, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas"); ready >= want && want > 0 {
			phase = "Ready"
		}
		rows = append(rows, providerRow{d.GetNamespace(), name, typ, full, version, phase})
	}
	return rows, nil
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
	if install.DetectCAPI(s.kube) == install.CAPILean {
		return leanProviders(ctx, s)
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
