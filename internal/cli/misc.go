package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/operator"
	"github.com/cbridges1/hyve/internal/render"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

var secretGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

// SecretsCmd is `hyve secrets`: resources name the Kubernetes Secrets Fleet
// copies to their clusters. hyve can make and change them, or the install
// can fill them some other way (HYVE-PROPOSAL.md, "Secrets").
func SecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret"},
		Short:   "Create, change, list and delete the Secrets resources copy to clusters",
	}
	AddGlobalFlags(cmd)
	addEnvFlag(cmd)
	addTemplateNSFlag(cmd)
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the Secrets templates and extras copy to clusters, and which are missing",
		Long: `Lists, by name only, the Secrets that template resources (in the template
namespace) and cluster extras (in the environment) have Fleet copy to their
clusters, and whether each exists, with their keys; it never reads values
('hyve secrets show NAME --reveal' does).
Create missing ones with 'hyve secrets create', or however your install
manages secrets: an operator such as Infisical's or External Secrets,
sealed-secrets.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			type need struct{ ns, name string }
			users := map[need][]string{}
			lookup := resources.SharedLookup(s.kube.Dynamic)
			tl, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(s.templateNS()).List(ctx, metav1.ListOptions{})
			if err != nil {
				return notInstalled(err)
			}
			for i := range tl.Items {
				t, err := templates.FromUnstructured(&tl.Items[i])
				if err != nil {
					continue
				}
				for _, e := range t.Spec.Resources {
					if r, err := resources.Resolve(ctx, e, t.Namespace, lookup); err == nil {
						e = r
					}
					for _, sec := range e.Secrets {
						k := need{t.Namespace, sec}
						users[k] = append(users[k], "template "+t.Name+"/"+e.Name)
					}
				}
			}
			crs, err := s.kube.Dynamic.Resource(resources.ClusterResourceGVR).Namespace(s.env()).List(ctx, metav1.ListOptions{})
			if err == nil {
				for i := range crs.Items {
					e, err := resources.EntryFromSpec(&crs.Items[i])
					if err != nil {
						continue
					}
					if r, err := resources.Resolve(ctx, e, s.templateNS(), lookup); err == nil {
						e = r
					}
					for _, sec := range e.Secrets {
						k := need{crs.Items[i].GetNamespace(), sec}
						users[k] = append(users[k], "extra "+crs.Items[i].GetName())
					}
				}
			}
			keys := make([]need, 0, len(users))
			for k := range users {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i].ns+"/"+keys[i].name < keys[j].ns+"/"+keys[j].name })
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAMESPACE\tSECRET\tEXISTS\tKEYS\tUSED BY")
			for _, k := range keys {
				exists, keyNames := "no", "-"
				// Metadata only: the values are never decoded or printed.
				sec, err := s.kube.Dynamic.Resource(secretGVR).Namespace(k.ns).Get(ctx, k.name, metav1.GetOptions{})
				switch {
				case err == nil:
					exists = "yes"
					var names []string
					if d, ok := sec.Object["data"].(map[string]any); ok {
						for n := range d {
							names = append(names, n)
						}
					}
					sort.Strings(names)
					keyNames = dash(strings.Join(names, ","))
				case apierrors.IsForbidden(err):
					exists = "can't tell"
				case !apierrors.IsNotFound(err):
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", k.ns, k.name, exists, keyNames, strings.Join(users[k], ", "))
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(list, secretsShowCmd(), secretsCreateCmd(), secretsSetCmd(), secretsDeleteCmd())
	return cmd
}

// RenderCmd is `hyve render`: the Cluster and Fleet objects a template
// produces, from files, with no cluster at all.
func RenderCmd() *cobra.Command {
	var (
		files            []string
		name, env, tname string
		sets             []string
	)
	cmd := &cobra.Command{
		Use:   "render -f FILE... [--template NAME] [--name CLUSTER]",
		Short: "Print the Cluster and Fleet objects a template produces, without a cluster",
		Long: `Reads ClusterTemplates, shared Resources and ClusterClasses from YAML files and
prints what hyve would create for a cluster made from the template: the
filled-in Cluster, and one Fleet object per resource. Nothing is contacted.
Without the ClusterClass among the files, its defaults (version, workers)
can't be filled in.`,
		Example: `  hyve render -f fleet/capi/templates/civo.yaml -f fleet/capi/classes/civo.yaml --name demo -e ci`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var objs []*unstructured.Unstructured
			dirs := map[string]string{}
			for _, f := range files {
				raw, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				found, err := kube.Decode(raw)
				if err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}
				for _, o := range found {
					if o.GetKind() == "ClusterTemplate" {
						dirs[o.GetName()] = filepath.Dir(f)
					}
				}
				objs = append(objs, found...)
			}
			set, err := parseKV(sets, true)
			if err != nil {
				return err
			}
			res, err := render.Render(cmd.Context(), objs, render.Options{
				Template: tname, Name: name, Env: env, Variables: set, TemplateNamespace: DefaultTemplateNamespace,
				Prepare: func(t *templates.Template) error { return inlineBundlePaths(t.Spec.Resources, dirs[t.Name]) },
			})
			if err != nil {
				if strings.Contains(err.Error(), "more than one ClusterTemplate") {
					return errors.New("the files hold more than one ClusterTemplate; pick one with --template")
				}
				return err
			}
			for _, n := range res.Notes {
				logf("note: %s", n)
			}
			if err := printYAML(res.Cluster.Object); err != nil {
				return err
			}
			for _, obj := range res.Fleet {
				fmt.Println("---")
				if err := printYAML(obj.Object); err != nil {
					return err
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVarP(&files, "filename", "f", nil, "YAML file with ClusterTemplates, Resources or ClusterClasses (repeatable)")
	f.StringVar(&tname, "template", "", "which template, when the files hold several")
	f.StringVar(&name, "name", "example", "the cluster's name")
	f.StringVarP(&env, "env", "e", DefaultEnvironment, "the cluster's environment")
	f.StringArrayVar(&sets, "set", nil, "ClusterClass variable name=value (repeatable)")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

// ControllerCmd is `hyve controller`, which the chart's Deployment runs.
func ControllerCmd() *cobra.Command {
	var o operator.Options
	var kubeconfig, hostCAFile string
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Run hyve-controller on the management cluster (the chart runs it)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctrl.SetLogger(zap.New())
			var cfg *rest.Config
			var err error
			if kubeconfig != "" {
				cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			} else {
				cfg, err = ctrl.GetConfig()
			}
			if err != nil {
				return err
			}
			if hostCAFile != "" {
				b, err := os.ReadFile(hostCAFile)
				if err != nil {
					return err
				}
				o.Agents.CACerts = string(b)
			}
			return operator.Run(ctrl.SetupSignalHandler(), cfg, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig (default: in-cluster, or $KUBECONFIG)")
	f.IntVar(&o.WebhookPort, "webhook-port", 9443, "webhook port")
	f.StringVar(&o.CertDir, "cert-dir", "/tmp/hyve-webhook", "where the webhook's generated certificate is written")
	f.StringVar(&o.ServiceName, "service-name", "hyve-controller", "webhook Service name")
	f.StringVar(&o.ServiceNamespace, "service-namespace", "hyve-system", "webhook Service namespace")
	f.StringVar(&o.MutatingWebhookName, "mutating-webhook", install.MutatingWebhook, "MutatingWebhookConfiguration to patch the CA into")
	f.StringVar(&o.ValidatingWebhook, "validating-webhook", "hyve-controller", "ValidatingWebhookConfiguration to patch the CA into")
	f.BoolVar(&o.LazyProviders, "lazy-providers", false, "add known Cluster API providers when a ClusterClass or a Fleet bundle first needs them (local mode)")
	f.StringVar(&o.FleetImage, "fleet-image", "", "image for push delivery's fleet commands (default: the image fleet-controller runs)")
	f.BoolVar(&o.NoWebhook, "no-webhook", false, "run without the webhooks (templates then aren't filled in server-side)")
	f.StringVar(&o.Agents.HostURL, "host-url", "", "the hyve host's URL as child clusters reach it; installs cattle-cluster-agent on each cluster, pointed there")
	f.StringVar(&hostCAFile, "host-cacerts", "", "PEM CA the host's certificate chains to, when it isn't publicly trusted")
	f.StringVar(&o.Agents.Image, "agent-image", operator.DefaultAgentImage, "cattle-cluster-agent image")
	f.StringVar(&o.Agents.Namespace, "agent-namespace", "hyve-system", "namespace of the host's agent Secrets")
	f.StringVar(&o.Agents.AdminUser, "admin-user", "admin", "hyve user who is cluster-admin on every child cluster")
	return cmd
}
