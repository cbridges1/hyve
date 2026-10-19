package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/login"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// ClusterCmd is `hyve cluster`.
func ClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster",
		Aliases: []string{"clusters"},
		Short:   "Create, inspect, reach, scale and delete clusters",
	}
	AddGlobalFlags(cmd)
	addEnvFlag(cmd)
	addTemplateNSFlag(cmd)
	cmd.AddCommand(clusterCreateCmd(), clusterListCmd(), clusterShowCmd(), clusterDeleteCmd(),
		clusterKubeconfigCmd(), clusterExpireCmd(), clusterScaleCmd(), clusterResourceCmd())
	return cmd
}

func (s *session) getCluster(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	c, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(s.env()).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) && !strings.Contains(err.Error(), "could not find the requested resource") {
		return nil, fmt.Errorf("cluster %q not found in environment %q", name, s.env())
	}
	return c, err
}

func (s *session) getClass(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
	class, err := s.kube.Dynamic.Resource(clusters.ClusterClassGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("ClusterClass %q not found in namespace %q", name, ns)
	}
	return class, err
}

func clusterCreateCmd() *cobra.Command {
	var (
		template, class, classNS, version string
		workerClass, workerType           string
		workers, controlPlane             int64
		ttl                               time.Duration
		noExpiry, noWait, dryRun          bool
		secretsEnv                        bool
		sets, labels                      []string
		timeout                           time.Duration
	)
	cmd := &cobra.Command{
		Use:   "create NAME (--template TEMPLATE | --class CLASS)",
		Short: "Create a cluster from a ClusterTemplate or a ClusterClass",
		Long: `Creates a Cluster API Cluster in the environment. With --template it's
labelled with the template, and filled in from it and its ClusterClass:
the same thing hyve-controller does for a labelled Cluster created any
other way (kubectl, GitOps, the UI). The template's resources then reach
it through Fleet.

--set sets ClusterClass variables; values are parsed as JSON when they are
valid JSON (numbers, lists, objects), otherwise used as strings. A variable
the template locks can't be changed.

By default waits until the cluster is ACTIVE and registered with Fleet.`,
		Example: `  hyve cluster create demo --template capd-small --ttl 4h
  hyve cluster create web --template civo --set region=LON1 --workers 3 -e prod
  hyve cluster create demo --class capd --class-namespace capi-clusters`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (class == "") == (template == "") {
				return errors.New("give exactly one of --template or --class")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c := clusters.New(args[0], s.env())
			if version != "" {
				_ = unstructured.SetNestedField(c.Object, version, "spec", "topology", "version")
			}
			if cmd.Flags().Changed("control-plane") {
				_ = unstructured.SetNestedField(c.Object, controlPlane, "spec", "topology", "controlPlane", "replicas")
			}
			set, err := parseKV(sets, true)
			if err != nil {
				return err
			}
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
			switch {
			case noExpiry:
				c.SetAnnotations(map[string]string{templates.AnnotationExpiresAt: templates.Never})
			case ttl > 0:
				c.SetAnnotations(map[string]string{templates.AnnotationExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339)})
			}

			var t *templates.Template
			var cls *unstructured.Unstructured
			if template != "" {
				if t, err = s.getTemplate(ctx, template); err != nil {
					return err
				}
				if cls, err = s.getClass(ctx, t.ClassNamespace(), t.Spec.ClusterClassRef.Name); err != nil {
					return err
				}
				templates.Label(c, t)
			} else {
				if classNS == "" {
					classNS = s.env()
				}
				if cls, err = s.getClass(ctx, classNS, class); err != nil {
					return err
				}
				_ = unstructured.SetNestedField(c.Object, class, "spec", "topology", "classRef", "name")
				if classNS != c.GetNamespace() {
					_ = unstructured.SetNestedField(c.Object, classNS, "spec", "topology", "classRef", "namespace")
				}
			}
			if cmd.Flags().Changed("workers") || workerClass != "" || workerType != "" {
				typ, wc := clusters.DefaultWorkers(cls)
				count := int64(1)
				if t != nil && t.Spec.Workers != nil {
					if t.Spec.Workers.Type != "" {
						typ, wc = t.Spec.Workers.Type, clusters.FirstWorkerClass(cls, t.Spec.Workers.Type)
					}
					if t.Spec.Workers.Class != "" {
						wc = t.Spec.Workers.Class
					}
					if t.Spec.Workers.Count != nil {
						count = *t.Spec.Workers.Count
					}
				}
				if workerType != "" {
					typ, wc = workerType, clusters.FirstWorkerClass(cls, workerType)
				}
				if workerClass != "" {
					wc = workerClass
				}
				if cmd.Flags().Changed("workers") {
					count = workers
				}
				if wc == "" {
					return fmt.Errorf("ClusterClass %s has no %s workers", cls.GetName(), typ)
				}
				if err := clusters.SetWorkers(c, typ, wc, count); err != nil {
					return err
				}
			}
			// The controller's webhook does this too when the Cluster is
			// created; doing it here shows what's coming and catches
			// mistakes before anything is sent.
			if t != nil {
				err = templates.Fill(c, t, cls, time.Now())
			} else {
				err = clusters.FillDefaults(c, cls, true)
			}
			if err != nil {
				return err
			}
			if secretsEnv && t != nil {
				if err := secretsFromEnv(ctx, s, t); err != nil {
					return err
				}
			}
			if dryRun {
				out, err := yaml.Marshal(c.Object)
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(out)
				return err
			}
			if _, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(c.GetNamespace()).Create(ctx, c, metav1.CreateOptions{FieldManager: kube.FieldManager}); err != nil {
				return err
			}
			logf("Created cluster %s in environment %s", c.GetName(), c.GetNamespace())
			if exp := c.GetAnnotations()[templates.AnnotationExpiresAt]; exp != "" && exp != templates.Never {
				logf("It expires at %s", exp)
			}
			if noWait {
				return nil
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := waitReady(ctx, s, c.GetNamespace(), c.GetName(), timeout); err != nil {
				return err
			}
			logf("Cluster %s is ready. 'hyve cluster kubeconfig %s' gives kubectl access.", c.GetName(), c.GetName())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&template, "template", "t", "", "ClusterTemplate to build from (in the template namespace)")
	f.StringVar(&class, "class", "", "ClusterClass to build from, without a template")
	f.StringVar(&classNS, "class-namespace", "", "the ClusterClass's namespace with --class (default: the environment)")
	f.DurationVar(&ttl, "ttl", 0, "expire the cluster this long after creation (replaces the template's schedule)")
	f.BoolVar(&noExpiry, "no-expiry", false, "don't expire the cluster, whatever the template's schedule")
	f.StringVar(&version, "version", "", "Kubernetes version (default: the template's, or the class's hyve.io/default-version)")
	f.Int64Var(&controlPlane, "control-plane", 0, "control-plane replicas (default: 1 for a machine-based control plane, none for a managed one)")
	f.Int64Var(&workers, "workers", 1, "worker replicas")
	f.StringVar(&workerClass, "worker-class", "", "worker class (default: the template's, or the class's first)")
	f.StringVar(&workerType, "worker-type", "", "machineDeployment or machinePool")
	f.StringArrayVar(&sets, "set", nil, "ClusterClass variable name=value (repeatable)")
	f.StringArrayVar(&labels, "label", nil, "label key=value on the Cluster (repeatable; Fleet sees it through CAAPF)")
	f.BoolVar(&noWait, "no-wait", false, "return once the Cluster object is created")
	f.BoolVar(&dryRun, "dry-run", false, "print the Cluster instead of creating it")
	f.BoolVar(&secretsEnv, "secrets-env", false, "create the Secrets the template's resources need from environment variables: Secret my-secret from MY_SECRET (a JSON object of keys, or one value under key \"value\")")
	f.DurationVar(&timeout, "timeout", 30*time.Minute, "how long to wait")
	return cmd
}

// waitReady waits until the cluster is ACTIVE and, when Fleet is
// installed, CAAPF has registered it and its Fleet agent is ready.
func waitReady(ctx context.Context, s *session, ns, name string, timeout time.Duration) error {
	fleet := s.kube.HasKind("fleet.cattle.io", "v1alpha1", "Cluster")
	return kube.Poll(ctx, 10*time.Second, timeout, func(m string) { logf("  %s", m) }, func(ctx context.Context) (bool, string, error) {
		c, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, "", err
		}
		phase := clusters.Phase(c)
		if phase == "FAILED" || phase == "DELETING" {
			return false, "", fmt.Errorf("cluster is %s", phase)
		}
		if phase != "ACTIVE" {
			return false, "provisioning: " + phase, nil
		}
		if !fleet {
			return true, "", nil
		}
		fc, err := clusters.FleetCluster(ctx, s.kube.Dynamic, ns, name)
		if err != nil {
			return false, "", err
		}
		if fc == nil {
			return false, "waiting for CAAPF to register it with Fleet", nil
		}
		if !clusters.FleetReady(fc) {
			return false, "waiting for its Fleet agent", nil
		}
		return true, "", nil
	})
}

func clusterKubeconfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kubeconfig NAME",
		Short: "Print a kubeconfig that reaches a cluster through the proxy, as you",
		Long: `Prints a kubeconfig for <url>/k8s/clusters/<id> on the signed-in server:
the same proxy and the same sign-in as the management cluster, so kubectl
runs under your own permissions. It holds no secret; it runs 'hyve token'.
The cluster's admin credentials are never handed out.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			if s.server == nil {
				return errors.New("needs a signed-in server ('hyve login'), not --kubeconfig")
			}
			ctx := cmd.Context()
			c, err := s.getCluster(ctx, args[0])
			if err != nil {
				return err
			}
			var id string
			switch s.server.Kind {
			case login.KindRancher:
				if id, err = rancherClusterID(ctx, s.server, c.GetNamespace(), c.GetName()); err != nil {
					return err
				}
			default:
				// hyve's host serves child clusters through the agent's tunnel
				// once it's connected (HYVE-PROPOSAL.md, "hyve as the Rancher
				// host"); its ID is <environment>.<name>.
				id = c.GetNamespace() + "." + c.GetName()
			}
			bin, err := os.Executable()
			if err != nil {
				return errors.New("can't find the hyve binary's path for the exec credential")
			}
			out, err := login.Kubeconfig(s.server, id, bin, ExplicitHome())
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(out)
			return err
		},
	}
}

func clusterListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List clusters",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ns := s.env()
			if all {
				ns = ""
			}
			list, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(ns).List(cmd.Context(), metav1.ListOptions{})
			if err != nil {
				return err
			}
			sort.Slice(list.Items, func(i, j int) bool {
				a, b := list.Items[i], list.Items[j]
				return a.GetNamespace()+"/"+a.GetName() < b.GetNamespace()+"/"+b.GetName()
			})
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			head := "NAME\tTEMPLATE\tCLASS\tVERSION\tPHASE\tEXPIRES\tAGE"
			if all {
				head = "ENVIRONMENT\t" + head
			}
			fmt.Fprintln(w, head)
			for i := range list.Items {
				c := &list.Items[i]
				class, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "classRef", "name")
				version, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "version")
				tn, _ := templates.Ref(c)
				row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s", c.GetName(), dash(tn), dash(class), dash(version), clusters.Phase(c), expiresAt(c), clusters.Age(c.GetCreationTimestamp().Time))
				if all {
					row = c.GetNamespace() + "\t" + row
				}
				fmt.Fprintln(w, row)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVarP(&all, "all-environments", "A", false, "list clusters in every environment")
	return cmd
}

func expiresAt(c *unstructured.Unstructured) string {
	return dash(c.GetAnnotations()[templates.AnnotationExpiresAt])
}

func clusterShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show NAME",
		Short: "Show a cluster's spec, Fleet and Rancher state, resources and conditions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, err := s.getCluster(ctx, args[0])
			if err != nil {
				return err
			}
			topo := func(f ...string) string {
				v, _, _ := unstructured.NestedFieldNoCopy(c.Object, append([]string{"spec", "topology"}, f...)...)
				if v == nil {
					return "-"
				}
				return fmt.Sprint(v)
			}
			tn, tns := templates.Ref(c)
			if tn != "" {
				tn = tns + "/" + tn
			}
			classNS := topo("classRef", "namespace")
			if classNS == "-" {
				classNS = c.GetNamespace()
			}
			fmt.Printf("Name:          %s\n", c.GetName())
			fmt.Printf("Environment:   %s\n", c.GetNamespace())
			fmt.Printf("Template:      %s\n", dash(tn))
			fmt.Printf("Class:         %s/%s\n", classNS, topo("classRef", "name"))
			fmt.Printf("Expires:       %s\n", expiresAt(c))
			fmt.Printf("Version:       %s\n", topo("version"))
			fmt.Printf("Control plane: %s\n", topo("controlPlane", "replicas"))
			for _, typ := range []string{clusters.MachineDeployment, clusters.MachinePool} {
				ws, _, _ := unstructured.NestedSlice(c.Object, "spec", "topology", "workers", typ+"s")
				for _, w := range ws {
					if m, ok := w.(map[string]any); ok {
						fmt.Printf("Workers:       %v × %v (%v)\n", m["replicas"], m["class"], m["name"])
					}
				}
			}
			vars := clusters.Variables(c)
			names := make([]string, 0, len(vars))
			for n := range vars {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				val, _ := json.Marshal(vars[n])
				fmt.Printf("Variable:      %s=%s\n", n, val)
			}
			fmt.Printf("Phase:         %s\n", clusters.Phase(c))
			if fc, err := clusters.FleetCluster(ctx, s.kube.Dynamic, c.GetNamespace(), c.GetName()); err == nil {
				switch {
				case fc == nil:
					fmt.Println("Fleet:         not registered")
				case clusters.FleetReady(fc):
					fmt.Println("Fleet:         agent ready")
				default:
					fmt.Println("Fleet:         agent not ready")
				}
			}
			if rc, err := clusters.RancherCluster(ctx, s.kube.Dynamic, c.GetNamespace(), c.GetName()); err == nil && rc != nil {
				fmt.Printf("Rancher:       %s (%s)\n", rc.GetName(), clusters.RancherState(rc))
			}
			var keys []string
			for k := range c.GetLabels() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("Label:         %s=%s\n", k, c.GetLabels()[k])
			}
			fmt.Println("Conditions:")
			for _, l := range clusters.Conditions(c) {
				fmt.Println("  " + l)
			}
			return nil
		},
	}
}

func clusterDeleteCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a cluster, its extras and what Fleet deployed for it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ns, name := s.env(), args[0]
			ri := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(ns)
			if err := ri.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
				return err
			}
			logf("Deleting cluster %s in environment %s", name, ns)
			if !wait {
				return nil
			}
			err = kube.Poll(ctx, 5*time.Second, timeout, nil, func(ctx context.Context) (bool, string, error) {
				_, err := ri.Get(ctx, name, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return true, "", nil
				}
				return false, "", err
			})
			if err != nil {
				return err
			}
			logf("Deleted")
			return nil
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", true, "wait until the cluster is gone")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "how long to wait")
	return cmd
}

func clusterExpireCmd() *cobra.Command {
	var in time.Duration
	var at string
	var never bool
	cmd := &cobra.Command{
		Use:   "expire NAME (--in DURATION | --at TIME | --never)",
		Short: "Set when a cluster expires (hyve-controller deletes it then), or stop it expiring",
		Example: `  hyve cluster expire web --in 8h
  hyve cluster expire web --at 2026-12-01T17:00:00Z
  hyve cluster expire web --never`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := 0
			for _, set := range []bool{in != 0, at != "", never} {
				if set {
					n++
				}
			}
			if n != 1 {
				return errors.New("give exactly one of --in, --at or --never")
			}
			value := templates.Never
			if in != 0 {
				value = time.Now().Add(in).UTC().Format(time.RFC3339)
			} else if at != "" {
				t, err := time.Parse(time.RFC3339, at)
				if err != nil {
					return fmt.Errorf("--at: want RFC 3339 (2026-12-01T17:00:00Z): %w", err)
				}
				value = t.UTC().Format(time.RFC3339)
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{templates.AnnotationExpiresAt: value}}})
			if _, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(s.env()).Patch(cmd.Context(), args[0], types.MergePatchType, patch, metav1.PatchOptions{FieldManager: kube.FieldManager}); err != nil {
				return err
			}
			if value == templates.Never {
				logf("Cluster %s no longer expires", args[0])
			} else {
				logf("Cluster %s expires at %s", args[0], value)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&in, "in", 0, "expire this long from now")
	cmd.Flags().StringVar(&at, "at", "", "expire at this RFC 3339 time")
	cmd.Flags().BoolVar(&never, "never", false, "never expire, whatever the template's schedule")
	return cmd
}

func clusterScaleCmd() *cobra.Command {
	var workers, controlPlane int64
	cmd := &cobra.Command{
		Use:   "scale NAME (--workers N | --control-plane N)",
		Short: "Change a cluster's worker or control-plane replicas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wc, cc := cmd.Flags().Changed("workers"), cmd.Flags().Changed("control-plane")
			if !wc && !cc {
				return errors.New("give --workers and/or --control-plane")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, err := s.getCluster(ctx, args[0])
			if err != nil {
				return err
			}
			if cc {
				if err := unstructured.SetNestedField(c.Object, controlPlane, "spec", "topology", "controlPlane", "replicas"); err != nil {
					return err
				}
			}
			if wc {
				found := false
				for _, typ := range []string{clusters.MachineDeployment, clusters.MachinePool} {
					ws, ok, _ := unstructured.NestedSlice(c.Object, "spec", "topology", "workers", typ+"s")
					if !ok || len(ws) == 0 {
						continue
					}
					if len(ws) > 1 {
						return fmt.Errorf("cluster %s has %d worker groups; scale them with kubectl", c.GetName(), len(ws))
					}
					m := ws[0].(map[string]any)
					m["replicas"] = workers
					if err := unstructured.SetNestedSlice(c.Object, ws, "spec", "topology", "workers", typ+"s"); err != nil {
						return err
					}
					found = true
				}
				if !found {
					return fmt.Errorf("cluster %s has no workers to scale", c.GetName())
				}
			}
			if _, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(c.GetNamespace()).Update(ctx, c, metav1.UpdateOptions{FieldManager: kube.FieldManager}); err != nil {
				return err
			}
			logf("Scaling cluster %s", c.GetName())
			return nil
		},
	}
	cmd.Flags().Int64Var(&workers, "workers", 0, "worker replicas")
	cmd.Flags().Int64Var(&controlPlane, "control-plane", 0, "control-plane replicas")
	return cmd
}

// secretsFromEnv creates or updates, in the template's namespace, each
// Secret the template's resources name for which an environment variable
// of the same name (upper case, '-' as '_') is set. Values are never
// printed.
func secretsFromEnv(ctx context.Context, s *session, t *templates.Template) error {
	lookup := resources.SharedLookup(s.kube.Dynamic)
	seen := map[string]bool{}
	for _, e := range t.Spec.Resources {
		if r, err := resources.Resolve(ctx, e, t.Namespace, lookup); err == nil {
			e = r
		}
		for _, name := range e.Secrets {
			if seen[name] {
				continue
			}
			seen[name] = true
			env := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
			raw, ok := os.LookupEnv(env)
			if !ok {
				continue
			}
			data := map[string]any{}
			var obj map[string]any
			if err := json.Unmarshal([]byte(raw), &obj); err == nil && len(obj) > 0 {
				for k, v := range obj {
					data[k] = fmt.Sprint(v)
				}
			} else {
				data["value"] = raw
			}
			sec := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
				"metadata":   map[string]any{"name": name, "namespace": t.Namespace, "labels": map[string]any{clusters.LabelManagedBy: clusters.ManagedBy}},
				"stringData": data,
			}}
			if err := s.kube.ApplyObject(ctx, sec); err != nil {
				return fmt.Errorf("Secret %s from $%s: %w", name, env, err)
			}
			logf("Secret %s/%s set from $%s (%d keys)", t.Namespace, name, env, len(data))
		}
	}
	return nil
}

// rancherClusterID finds the Rancher cluster Turtles imported a CAPI
// cluster as, through Rancher's Steve API, which lists only the clusters
// the user can see (listing them through the Kubernetes API needs rights
// on all of them).
func rancherClusterID(ctx context.Context, srv *login.Server, ns, name string) (string, error) {
	hc, err := login.HTTPClient(srv.CACertFile, srv.InsecureSkipVerify)
	if err != nil {
		return "", err
	}
	q := url.Values{"labelSelector": {"cluster-api.cattle.io/capi-cluster-owner=" + name + ",cluster-api.cattle.io/capi-cluster-owner-ns=" + ns}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/management.cattle.io.clusters?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+srv.Token)
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("looking up cluster %s in Rancher: %s", name, resp.Status)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", fmt.Errorf("cluster %s isn't imported into Rancher yet, or you have no access to it there", name)
	}
	return out.Data[0].ID, nil
}
