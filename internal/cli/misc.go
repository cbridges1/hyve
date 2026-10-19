package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/operator"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

var secretGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

// SecretsCmd is `hyve secrets`. hyve holds no secret values: resources name
// the Kubernetes Secrets Fleet copies to their clusters, and whatever
// fills those Secrets is up to the install (HYVE-PROPOSAL.md, "Secrets").
func SecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret"},
		Short:   "Which Secrets resources need, and whether they exist (names only)",
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
clusters, and whether each exists. hyve never reads or prints values.
Create missing ones however your install manages secrets: kubectl, an
operator such as Infisical's or External Secrets, sealed-secrets.`,
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
	cmd.AddCommand(list)
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
			var tpls []*templates.Template
			tplDir := map[*templates.Template]string{}
			shared := map[string]*resources.Entry{}
			classes := map[string]*unstructured.Unstructured{}
			for _, f := range files {
				raw, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				objs, err := kube.Decode(raw)
				if err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}
				for _, o := range objs {
					switch o.GetKind() {
					case "ClusterTemplate":
						if o.GetNamespace() == "" {
							o.SetNamespace(DefaultTemplateNamespace)
						}
						t, err := templates.FromUnstructured(o)
						if err != nil {
							return err
						}
						tpls = append(tpls, t)
						tplDir[t] = filepath.Dir(f)
					case "Resource":
						e, err := resources.EntryFromSpec(o)
						if err != nil {
							return err
						}
						shared[o.GetName()] = &e
					case "ClusterClass":
						classes[o.GetName()] = o
					}
				}
			}
			var t *templates.Template
			for _, x := range tpls {
				if tname == "" || x.Name == tname {
					if t != nil && tname == "" {
						return errors.New("the files hold more than one ClusterTemplate; pick one with --template")
					}
					t = x
				}
			}
			if t == nil {
				return errors.New("no ClusterTemplate found in the files")
			}
			if err := inlineBundlePaths(t.Spec.Resources, tplDir[t]); err != nil {
				return err
			}
			if err := templates.Validate(t); err != nil {
				return err
			}
			class, ok := classes[t.Spec.ClusterClassRef.Name]
			if !ok {
				logf("note: ClusterClass %s isn't among the files; class defaults are left out", t.Spec.ClusterClassRef.Name)
				class = &unstructured.Unstructured{Object: map[string]any{}}
			}
			c := clusters.New(name, env)
			set, err := parseKV(sets, true)
			if err != nil {
				return err
			}
			if err := clusters.SetVariables(c, set); err != nil {
				return err
			}
			templates.Label(c, t)
			if err := templates.Fill(c, t, class, time.Now()); err != nil {
				return err
			}
			if err := printYAML(c.Object); err != nil {
				return err
			}
			lookup := func(_ context.Context, _, n string) (*resources.Entry, error) {
				if e, ok := shared[n]; ok {
					return e, nil
				}
				return nil, fmt.Errorf("Resource %s isn't among the files", n)
			}
			owner := resources.Owner{Kind: resources.OwnerTemplate, Name: t.Name, Namespace: t.Namespace}
			for _, e := range t.Spec.Resources {
				r, err := resources.Resolve(cmd.Context(), e, t.Namespace, lookup)
				if err != nil {
					return err
				}
				obj, err := resources.Object(r, owner)
				if err != nil {
					return err
				}
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
	f.StringVar(&o.MutatingWebhookName, "mutating-webhook", "hyve-controller", "MutatingWebhookConfiguration to patch the CA into")
	f.StringVar(&o.ValidatingWebhook, "validating-webhook", "hyve-controller", "ValidatingWebhookConfiguration to patch the CA into")
	f.BoolVar(&o.LazyProviders, "lazy-providers", false, "add known Cluster API providers when a ClusterClass or a Fleet bundle first needs them (local mode)")
	f.BoolVar(&o.NoWebhook, "no-webhook", false, "run without the webhooks (templates then aren't filled in server-side)")
	f.StringVar(&o.Agents.HostURL, "host-url", "", "the hyve host's URL as child clusters reach it; installs cattle-cluster-agent on each cluster, pointed there (leave unset with Rancher)")
	f.StringVar(&hostCAFile, "host-cacerts", "", "PEM CA the host's certificate chains to, when it isn't publicly trusted")
	f.StringVar(&o.Agents.Image, "agent-image", operator.DefaultAgentImage, "cattle-cluster-agent image")
	f.StringVar(&o.Agents.Namespace, "agent-namespace", "hyve-system", "namespace of the host's agent Secrets")
	f.StringVar(&o.Agents.AdminUser, "admin-user", "admin", "hyve user who is cluster-admin on every child cluster")
	return cmd
}
