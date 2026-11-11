package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/importer"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/templates"
)

func clusterImportCmd() *cobra.Command {
	var (
		from, fromContext, apiServer, template string
		labels                                 []string
		noWait                                 bool
		timeout                                time.Duration
	)
	cmd := &cobra.Command{
		Use:   "import NAME [--from-kubeconfig FILE] [--from-context CONTEXT]",
		Short: "Bring an existing cluster under hyve, without managing its lifecycle",
		Long: `Imports a cluster hyve didn't create (EKS, GKE, AKS, on-prem, kind...),
reached with a kubeconfig you have: your current context unless
--from-kubeconfig or --from-context say otherwise.

On the cluster, a ServiceAccount (kube-system/hyve-import, cluster-admin)
gives hyve credentials of its own, so a kubeconfig that runs a command
(aws, gcloud) works too. In the environment, a Cluster API Cluster points
at it; Fleet registers it, and access, Deployments, kubeconfigs and the
Explorer work as for any cluster. --template puts it in a template's group,
so it gets what's deployed to the template (not its variables, version or
expiry).

hyve never creates, scales, upgrades or deletes an imported cluster.
'hyve cluster delete' detaches it: it removes hyve's and Fleet's agents and
hyve's ServiceAccount from the cluster, and leaves the rest, including what
Fleet deployed.

hyve's host must reach the cluster's API server. When it reaches it at
another address than you do (a private DNS name, host.docker.internal),
give it with --api-server.`,
		Example: `  hyve cluster import legacy-eks -e prod --from-context arn:aws:eks:us-east-1:123456789012:cluster/legacy
  hyve cluster import lab --from-kubeconfig lab.yaml --template base
  hyve cluster import kind-dev --from-context kind-dev --api-server https://host.docker.internal:6443`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if isLocal(name) {
				return fmt.Errorf("%q is the host cluster's name; pick another", name)
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c := clusters.NewImported(name, s.env())
			lbl, err := parseLabels(labels)
			if err != nil {
				return err
			}
			l := c.GetLabels()
			for k, v := range lbl {
				l[k] = v
			}
			c.SetLabels(l)
			if template != "" {
				t, err := s.getTemplate(ctx, template)
				if err != nil {
					return err
				}
				templates.Label(c, t)
			}
			if !s.kube.HasKind(clusters.ImportedGroup, "v1alpha1", clusters.ImportedControlPlaneKind) {
				return errors.New("this hyve server has no ImportedControlPlane kind: upgrade it to import clusters")
			}
			if _, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(s.env()).Get(ctx, name, metav1.GetOptions{}); err == nil {
				return fmt.Errorf("cluster %q already exists in environment %q", name, s.env())
			}

			rules := clientcmd.NewDefaultClientConfigLoadingRules()
			if from != "" {
				rules.ExplicitPath = from
			}
			cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: fromContext}).ClientConfig()
			if err != nil {
				return fmt.Errorf("the cluster's kubeconfig: %w", err)
			}
			logf("Giving hyve access to the cluster at %s (ServiceAccount %s/%s)", cfg.Host, importer.AccessNamespace, importer.AccessName)
			access, err := importer.Prepare(ctx, cfg, apiServer)
			if err != nil {
				return err
			}
			if err := importer.Create(ctx, s.kube.Dynamic, c, access.Kubeconfig); err != nil {
				return err
			}
			logf("Imported cluster %s (Kubernetes %s) into environment %s", name, access.Version, s.env())
			if noWait {
				return nil
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := waitReady(ctx, s, s.env(), name, timeout); err != nil {
				return fmt.Errorf("%w ('hyve cluster show %s' says why; hyve's host must reach the API server: see --api-server)", err, name)
			}
			logf("Cluster %s is ready. 'hyve cluster auth %s' points kubectl at it.", name, name)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&from, "from-kubeconfig", "", "kubeconfig that reaches the cluster (default: $KUBECONFIG or ~/.kube/config)")
	f.StringVar(&fromContext, "from-context", "", "context in it (default: its current one)")
	f.StringVar(&apiServer, "api-server", "", "the API server's URL as hyve's host reaches it (default: the kubeconfig's)")
	f.StringVarP(&template, "template", "t", "", "put the cluster in this template's group, so it gets what's deployed to the template")
	f.StringArrayVar(&labels, "label", nil, "label key=value on the Cluster (repeatable; Fleet sees it through CAAPF)")
	f.BoolVar(&noWait, "no-wait", false, "return once the objects are created")
	f.DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait")
	return cmd
}

func clusterAdoptCmd() *cobra.Command {
	var (
		template, id, version string
		sets, labels          []string
		ttl                   time.Duration
		noWait, dryRun        bool
		timeout               time.Duration
	)
	cmd := &cobra.Command{
		Use:   "adopt NAME --template TEMPLATE --id ID",
		Short: "Take over an existing cluster through its provider, by ID",
		Long: `Creates a cluster from a template that sets 'adopt', whose ClusterClass's
provider takes over the existing cluster with this ID (a Civo cluster's
ID, for cluster-api-provider-civo) instead of creating one. NAME needn't
be the cluster's own name. From then on hyve manages it like any other:
scaling, upgrades and deletion go through the provider.

The template's adopt.variables replace its variables on an adopted
cluster, and adopt.unset leaves some unset: usually so the provider keeps
the cluster when the hyve cluster is deleted (deletionPolicy: Retain) and
leaves its size alone (unset: [nodeCount]).
An adopted cluster takes no expiry schedule; --ttl gives it one.

Give --version as the cluster's Kubernetes version: the template's is
used otherwise. For a cluster with no provider that adopts, use
'hyve cluster import'.`,
		Example: `  hyve cluster adopt web -t civo -e prod --id 7f3c0d6e-0b5d-4b51-9b8a-1c2d3e4f5a6b --set region=NYC1 --version v1.33.0`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if template == "" || id == "" {
				return errors.New("give --template and --id")
			}
			if isLocal(name) {
				return fmt.Errorf("%q is the host cluster's name; pick another", name)
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			t, err := s.getTemplate(ctx, template)
			if err != nil {
				return err
			}
			if t.Spec.Adopt == nil {
				return fmt.Errorf("ClusterTemplate %s doesn't set adopt: its provider's ID variable isn't known (see 'hyve template create --help')", t.Name)
			}
			cls, err := s.getClass(ctx, t.ClassNamespace(), t.Spec.ClusterClassRef.Name)
			if err != nil {
				return err
			}
			if !classHasVariable(cls, t.Spec.Adopt.IDVariable) {
				return fmt.Errorf("ClusterClass %s has no variable %s: its provider may be too old to adopt", cls.GetName(), t.Spec.Adopt.IDVariable)
			}
			c := clusters.New(name, s.env())
			c.SetAnnotations(map[string]string{clusters.AnnotationAdopted: "true"})
			if ttl > 0 {
				ann := c.GetAnnotations()
				ann[templates.AnnotationExpiresAt] = time.Now().Add(ttl).UTC().Format(time.RFC3339)
				c.SetAnnotations(ann)
			}
			if version != "" {
				if !strings.HasPrefix(version, "v") {
					version = "v" + version
				}
				_ = unstructured.SetNestedField(c.Object, version, "spec", "topology", "version")
			}
			set, err := parseKV(sets, true)
			if err != nil {
				return err
			}
			set[t.Spec.Adopt.IDVariable] = id
			if err := clusters.SetVariables(c, set); err != nil {
				return err
			}
			lbl, err := parseLabels(labels)
			if err != nil {
				return err
			}
			if len(lbl) > 0 {
				c.SetLabels(lbl)
			}
			templates.Label(c, t)
			if err := templates.Fill(c, t, cls, time.Now()); err != nil {
				return err
			}
			if dryRun {
				return printObjects("yaml", c)
			}
			if _, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(c.GetNamespace()).Create(ctx, c, metav1.CreateOptions{FieldManager: kube.FieldManager}); err != nil {
				return err
			}
			logf("Adopting %s as cluster %s in environment %s", id, name, c.GetNamespace())
			if noWait {
				return nil
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := waitReady(ctx, s, c.GetNamespace(), name, timeout); err != nil {
				return err
			}
			logf("Cluster %s is ready. 'hyve cluster auth %s' points kubectl at it.", name, name)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&template, "template", "t", "", "ClusterTemplate with adopt set (in the template namespace)")
	f.StringVar(&id, "id", "", "the existing cluster's ID at its provider")
	f.StringVar(&version, "version", "", "the cluster's Kubernetes version (default: the template's)")
	f.StringArrayVar(&sets, "set", nil, "ClusterClass variable name=value (repeatable), e.g. its region")
	f.StringArrayVar(&labels, "label", nil, "label key=value on the Cluster (repeatable)")
	f.DurationVar(&ttl, "ttl", 0, "expire (delete) the cluster this long from now")
	f.BoolVar(&noWait, "no-wait", false, "return once the Cluster object is created")
	f.BoolVar(&dryRun, "dry-run", false, "print the Cluster instead of creating it")
	f.DurationVar(&timeout, "timeout", 15*time.Minute, "how long to wait")
	return cmd
}

// classHasVariable reports whether a ClusterClass declares a variable.
func classHasVariable(class *unstructured.Unstructured, name string) bool {
	vars, _, _ := unstructured.NestedSlice(class.Object, "spec", "variables")
	for _, v := range vars {
		if m, ok := v.(map[string]any); ok && m["name"] == name {
			return true
		}
	}
	return false
}
