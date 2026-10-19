package cli

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
)

// InstallCmd is `hyve install`.
func InstallCmd() *cobra.Command {
	var (
		o                                   install.Options
		kubeconfig, kubeContext             string
		tlsCert, tlsKey, caFile, valuesFile string
		timeout                             time.Duration
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install hyve on a management cluster, with whatever it needs that's missing",
		Long: `Installs hyve on the cluster your kubeconfig points at.

With Rancher installed, that's hyve-controller: Rancher brings Cluster API
(through Turtles) and Fleet, and its agents connect to Rancher. hyve also
adds CAAPF through Turtles if it's missing, and turns on Turtles' use-caapf
(in cattle-system/rancher-config), so CAAPF alone registers clusters with
Fleet. Turning it on reinstalls fleet-agent on clusters Turtles already
imported.

Without Rancher, it also installs what's missing of cert-manager, the
Cluster API Operator with core Cluster API, and CAAPF (which installs
Fleet), and hyve's host endpoint: the server users sign in to and the
child clusters' agents connect to. --host-url is where both reach it.

Run it as a cluster admin. Safe to re-run.`,
		Example: `  hyve install --host-url https://hyve.example.com --host-tls-cert tls.crt --host-tls-key tls.key
  hyve install --kubeconfig rancher.yaml          # alongside Rancher`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (tlsCert == "") != (tlsKey == "") {
				return errors.New("--host-tls-cert and --host-tls-key go together")
			}
			rules := clientcmd.NewDefaultClientConfigLoadingRules()
			if kubeconfig != "" {
				rules.ExplicitPath = kubeconfig
			}
			cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
			if err != nil {
				return err
			}
			kc, err := kube.New(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if caFile != "" {
				b, err := os.ReadFile(caFile)
				if err != nil {
					return err
				}
				o.HostCACerts = string(b)
			}
			if valuesFile != "" {
				raw, err := os.ReadFile(valuesFile)
				if err != nil {
					return err
				}
				objs, err := kube.Decode(raw)
				if err != nil || len(objs) != 1 {
					return errors.New("--values: want one YAML map of chart values")
				}
				o.Values = objs[0].Object
			}
			if tlsCert != "" {
				if o.HostURL == "" {
					return errors.New("--host-tls-cert needs --host-url")
				}
				crt, err := os.ReadFile(tlsCert)
				if err != nil {
					return err
				}
				key, err := os.ReadFile(tlsKey)
				if err != nil {
					return err
				}
				ns := o.Namespace
				for _, obj := range []*unstructured.Unstructured{
					{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}},
					{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls",
						"metadata":   map[string]any{"name": "hyve-host-tls", "namespace": ns},
						"stringData": map[string]any{"tls.crt": string(crt), "tls.key": string(key)}}},
				} {
					if err := kc.ApplyObject(ctx, obj); err != nil {
						return err
					}
				}
				o.HostTLSSecret = "hyve-host-tls"
			}
			o.Log = logf
			res, err := install.Install(ctx, kc, o)
			if err != nil {
				return err
			}
			switch {
			case res.Rancher:
				logf("\nhyve is installed alongside Rancher. Sign in with 'hyve login <your Rancher URL>'.")
			case o.HostURL != "":
				logf("\nhyve is installed. Sign in as admin:")
				logf("  kubectl -n %s get secret bootstrap-secret -o jsonpath='{.data.bootstrapPassword}' | base64 -d", o.Namespace)
				logf("  hyve login %s --username admin", o.HostURL)
			default:
				logf("\nhyve-controller is installed, with no host endpoint (no --host-url): use 'hyve --kubeconfig ...'.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig for the management cluster (default: $KUBECONFIG or ~/.kube/config)")
	f.StringVar(&kubeContext, "context", "", "kubeconfig context")
	f.StringVarP(&o.Namespace, "namespace", "n", "hyve-system", "namespace for hyve")
	f.StringVar(&o.Image, "image", "", "hyve image (default: the release's)")
	f.StringVar(&o.PullPolicy, "image-pull-policy", "", "image pull policy")
	f.StringVar(&o.HostURL, "host-url", "", "without Rancher: the host endpoint's URL, as users and child clusters reach it")
	f.StringVar(&tlsCert, "host-tls-cert", "", "certificate for the host to serve (default: plain HTTP, for a TLS-terminating proxy in front)")
	f.StringVar(&tlsKey, "host-tls-key", "", "key for --host-tls-cert")
	f.StringVar(&caFile, "host-cacerts", "", "PEM CA the host's certificate chains to, when it isn't publicly trusted")
	f.StringVarP(&valuesFile, "values", "f", "", "extra chart values (YAML)")
	f.BoolVar(&o.OnlyHyve, "only-hyve", false, "install hyve's chart only, not cert-manager, Cluster API, CAAPF or Fleet (with Rancher: leave CAAPF and use-caapf alone)")
	f.DurationVar(&timeout, "timeout", 20*time.Minute, "how long to wait")
	return cmd
}
