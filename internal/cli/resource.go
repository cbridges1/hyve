package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// ResourceCmd is `hyve resource`: shared Resources, and what's delivered
// where.
func ResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resource",
		Aliases: []string{"resources", "res"},
		Short:   "Shared resources that templates refer to, and what's delivered where",
		Long: `What hyve puts on a cluster is its template's resources plus that cluster's
own extras. A shared Resource is a Fleet HelmOp, Bundle or GitRepo spec
defined once, which templates and extras refer to by name ('--ref').
It has no targets of its own.

  hyve template resource add   every cluster made from a template
  hyve cluster resource add    one cluster
  hyve resource create         a shared definition to refer to`,
	}
	AddGlobalFlags(cmd)
	addEnvFlag(cmd)
	addTemplateNSFlag(cmd)
	cmd.AddCommand(resourceCreateCmd(), applyCmd(), resourceListCmd(), resourceShowCmd(), resourceStatusCmd(), resourceDeleteCmd())
	return cmd
}

func entryObject(kind, name, ns string, e resources.Entry, extra map[string]any) (*unstructured.Unstructured, error) {
	e.Name = ""
	spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&e)
	if err != nil {
		return nil, err
	}
	for k, v := range extra {
		spec[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "hyve.io/v1alpha1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       spec,
	}}, nil
}

func resourceCreateCmd() *cobra.Command {
	var f entryFlags
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create (--helm CHART | --git REPO | --path DIR) --name NAME",
		Short: "Create or replace a shared Resource in the template namespace",
		Example: `  hyve resource create --name external-dns --helm external-dns --repo https://kubernetes-sigs.github.io/external-dns/ \
      --version 1.19.0 -N external-dns -f external-dns-values.yaml --secret cloudflare-dns`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := f.entry()
			if err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			u, err := entryObject("Resource", e.Name, s.templateNS(), e, nil)
			if err != nil {
				return err
			}
			if dryRun {
				return printYAML(u.Object)
			}
			if err := s.kube.ApplyObject(cmd.Context(), u); err != nil {
				return notInstalled(err)
			}
			logf("Resource %s/%s (%s %s) saved; refer to it with --ref %s", u.GetNamespace(), e.Name, e.Kind(), e.Describe(), e.Name)
			return nil
		},
	}
	f.add(cmd, false)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the Resource instead of creating it")
	return cmd
}

func resourceListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List shared Resources, and every template and cluster resource hyve has delivered",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			shared, err := s.kube.Dynamic.Resource(resources.ResourceGVR).Namespace(s.templateNS()).List(ctx, metav1.ListOptions{})
			if err != nil {
				return notInstalled(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			if len(shared.Items) > 0 {
				fmt.Fprintln(w, "SHARED RESOURCE\tKIND\tSOURCE\tSECRETS")
				for i := range shared.Items {
					e, err := resources.EntryFromSpec(&shared.Items[i])
					if err != nil {
						return err
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, e.Kind(), e.Describe(), dash(strings.Join(e.Secrets, ",")))
				}
				fmt.Fprintln(w)
			}
			ns := ""
			if !all {
				ns = s.templateNS()
			}
			items, err := resources.List(ctx, s.kube.Dynamic, resources.Selector{Namespace: ns})
			if err != nil {
				return err
			}
			if !all {
				more, err := resources.List(ctx, s.kube.Dynamic, resources.Selector{Namespace: s.env()})
				if err != nil {
					return err
				}
				if s.env() != s.templateNS() {
					items = append(items, more...)
				}
			}
			fmt.Fprintln(w, "ON\tNAME\tKIND\tSOURCE\tREADY\tSTATE")
			for _, it := range items {
				on := "template " + it.Object.GetNamespace() + "/" + it.Owner
				if it.OwnerKind == resources.OwnerClusterResource {
					on = "cluster " + it.Object.GetNamespace() + "/" + targetCluster(it.Object)
				}
				ready, state := it.Ready()
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", on, it.Name, it.Kind, it.Source, dash(ready), dash(state))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVarP(&all, "all-namespaces", "A", false, "delivered resources in every namespace")
	return cmd
}

func resourceShowCmd() *cobra.Command {
	var template, cluster, output string
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a resource: a shared Resource, or one on a template or cluster, as written and as delivered",
		Long: `Shows a shared Resource and the templates and clusters that refer to it.
A resource that isn't shared (one written into a template, or an extra on
a cluster) is shown as written there, with the Fleet object hyve delivers
it as; --template or --cluster picks one when several have that name.`,
		Example: `  hyve resource show podinfo
  hyve resource show flannel --template capd-small
  hyve resource show monitoring --cluster demo -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			if template != "" && cluster != "" {
				return errors.New("give --template or --cluster, not both")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if template == "" && cluster == "" {
				u, err := s.kube.Dynamic.Resource(resources.ResourceGVR).Namespace(s.templateNS()).Get(ctx, args[0], metav1.GetOptions{})
				if err == nil {
					return showShared(cmd, s, u, output)
				}
				if !apierrors.IsNotFound(err) {
					return notInstalled(err)
				}
			}
			return showDelivered(cmd, s, args[0], template, cluster, output)
		},
	}
	cmd.Flags().StringVar(&template, "template", "", "the resource written into this template")
	cmd.Flags().StringVar(&cluster, "cluster", "", "the extra on this cluster (in the environment)")
	addOutputFlag(cmd, &output)
	return cmd
}

// showShared prints a shared Resource and who refers to it.
func showShared(cmd *cobra.Command, s *session, u *unstructured.Unstructured, output string) error {
	ctx := cmd.Context()
	if output != "" {
		return printObjects(output, tidy(u))
	}
	if err := printYAML(tidy(u)); err != nil {
		return err
	}
	name := u.GetName()
	tl, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(s.templateNS()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	var users []string
	for i := range tl.Items {
		t, err := templates.FromUnstructured(&tl.Items[i])
		if err != nil {
			continue
		}
		for _, e := range t.Spec.Resources {
			if e.Ref == name && (e.RefNamespace == "" || e.RefNamespace == s.templateNS()) {
				users = append(users, "template "+t.Name)
			}
		}
	}
	crs, err := s.kube.Dynamic.Resource(resources.ClusterResourceGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, cr := range crs.Items {
			if ref, _, _ := unstructured.NestedString(cr.Object, "spec", "ref"); ref == name {
				cn, _, _ := unstructured.NestedString(cr.Object, "spec", "clusterName")
				users = append(users, "cluster "+cr.GetNamespace()+"/"+cn)
			}
		}
	}
	fmt.Printf("# used by: %s\n", dash(strings.Join(users, ", ")))
	return nil
}

// showDelivered prints a template's or cluster's resource: as its owner
// has it (and the shared Resource it refers to), and the Fleet object hyve
// made from it.
func showDelivered(cmd *cobra.Command, s *session, name, template, cluster, output string) error {
	ctx := cmd.Context()
	sel := resources.Selector{}
	owners := map[string]bool{}
	switch {
	case template != "":
		t, err := s.getTemplate(ctx, template)
		if err != nil {
			return err
		}
		sel = resources.Selector{Namespace: t.Namespace, OwnerKind: resources.OwnerTemplate, Owner: t.Name}
	case cluster != "":
		sel = resources.Selector{Namespace: s.env(), OwnerKind: resources.OwnerClusterResource}
		crs, err := s.kube.Dynamic.Resource(resources.ClusterResourceGVR).Namespace(s.env()).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, cr := range crs.Items {
			if cn, _, _ := unstructured.NestedString(cr.Object, "spec", "clusterName"); cn == cluster {
				owners[cr.GetName()] = true
			}
		}
	}
	items, err := resources.List(ctx, s.kube.Dynamic, sel)
	if err != nil {
		return notInstalled(err)
	}
	var found []resources.Item
	for _, it := range items {
		if it.Name == name && (cluster == "" || owners[it.Owner]) {
			found = append(found, it)
		}
	}
	switch len(found) {
	case 0:
		where := "in the template namespace, or on any template or cluster"
		if template != "" {
			where = "on template " + template
		} else if cluster != "" {
			where = "on cluster " + cluster
		}
		return fmt.Errorf("no resource %q %s", name, where)
	case 1:
	default:
		var opts []string
		for _, it := range found {
			if it.OwnerKind == resources.OwnerTemplate {
				opts = append(opts, "--template "+it.Owner)
			} else {
				opts = append(opts, "--env "+it.Object.GetNamespace()+" (extra "+it.Owner+")")
			}
		}
		return fmt.Errorf("several resources are called %q; pick one: %s", name, strings.Join(opts, ", "))
	}
	it := found[0]
	ns := it.Object.GetNamespace()
	type doc struct {
		comment string
		obj     any
	}
	var docs []doc
	var entry *resources.Entry
	if it.OwnerKind == resources.OwnerTemplate {
		u, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(ns).Get(ctx, it.Owner, metav1.GetOptions{})
		if err == nil {
			if t, err := templates.FromUnstructured(u); err == nil {
				for i, e := range t.Spec.Resources {
					if e.Name == name || (e.Name == "" && e.Ref == name) {
						entry = &t.Spec.Resources[i]
						docs = append(docs, doc{"as written in template " + ns + "/" + it.Owner, e})
						break
					}
				}
			}
		}
	} else {
		cr, err := s.kube.Dynamic.Resource(resources.ClusterResourceGVR).Namespace(ns).Get(ctx, it.Owner, metav1.GetOptions{})
		if err == nil {
			docs = append(docs, doc{"as written, ClusterResource " + ns + "/" + it.Owner, tidy(cr)})
			if e, err := resources.EntryFromSpec(cr); err == nil {
				entry = &e
			}
		}
	}
	if entry != nil && entry.Ref != "" {
		refNS := entry.RefNamespace
		if refNS == "" {
			refNS = s.templateNS()
		}
		if u, err := s.kube.Dynamic.Resource(resources.ResourceGVR).Namespace(refNS).Get(ctx, entry.Ref, metav1.GetOptions{}); err == nil {
			docs = append(docs, doc{"the shared Resource it refers to", tidy(u)})
		}
	}
	docs = append(docs, doc{"delivered as Fleet " + it.Kind + " " + ns + "/" + it.Object.GetName(), tidy(it.Object)})
	if output != "" {
		objs := make([]any, 0, len(docs))
		for _, d := range docs {
			objs = append(objs, d.obj)
		}
		return printObjects(output, objs...)
	}
	for i, d := range docs {
		if i > 0 {
			fmt.Println("---")
		}
		fmt.Printf("# %s\n", d.comment)
		if err := printYAML(d.obj); err != nil {
			return err
		}
	}
	ready, state := it.Ready()
	fmt.Printf("# ready: %s  state: %s\n", dash(ready), dash(state))
	return nil
}

// resourceStatusCmd shows every resource on one cluster, or a matrix of
// clusters × resources for a template.
func resourceStatusCmd() *cobra.Command {
	var template string
	cmd := &cobra.Command{
		Use:   "status (CLUSTER | --template TEMPLATE)",
		Short: "What's on a cluster and why, or each resource's state on each of a template's clusters",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (template != "") {
				return errors.New("give a cluster or --template")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			if template != "" {
				t, err := s.getTemplate(ctx, template)
				if err != nil {
					return err
				}
				items, err := resources.List(ctx, s.kube.Dynamic, resources.Selector{Namespace: t.Namespace, OwnerKind: resources.OwnerTemplate, Owner: t.Name})
				if err != nil {
					return err
				}
				cl, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).List(ctx, metav1.ListOptions{
					LabelSelector: templates.LabelTemplate + "=" + t.Name + "," + templates.LabelTemplateNamespace + "=" + t.Namespace,
				})
				if err != nil {
					return err
				}
				head := "CLUSTER"
				for _, it := range items {
					head += "\t" + strings.ToUpper(it.Name)
				}
				fmt.Fprintln(w, head)
				for _, c := range cl.Items {
					states, _ := resources.ClusterStates(ctx, s.kube.Dynamic, c.GetNamespace(), c.GetName())
					row := c.GetNamespace() + "/" + c.GetName()
					for i := range items {
						row += "\t" + dash(items[i].StateOn(states))
					}
					fmt.Fprintln(w, row)
				}
				return w.Flush()
			}
			c, err := s.getCluster(ctx, args[0])
			if err != nil {
				return err
			}
			items, err := clusterItems(ctx, s, c)
			if err != nil {
				return err
			}
			states, err := resources.ClusterStates(ctx, s.kube.Dynamic, c.GetNamespace(), c.GetName())
			if err != nil {
				return err
			}
			if states == nil {
				logf("Cluster %s isn't registered with Fleet yet, so nothing has been delivered.", c.GetName())
			}
			fmt.Fprintln(w, "NAME\tWHY\tKIND\tSOURCE\tSTATE")
			for _, it := range items {
				why := "template " + it.Owner
				if it.OwnerKind == resources.OwnerClusterResource {
					why = "extra (" + it.Owner + ")"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", it.Name, why, it.Kind, it.Source, dash(it.StateOn(states)))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&template, "template", "", "a matrix of this template's clusters and resources")
	return cmd
}

// clusterItems returns what hyve delivers to one cluster: its template's
// resources and its own extras.
func clusterItems(ctx context.Context, s *session, c *unstructured.Unstructured) ([]resources.Item, error) {
	var out []resources.Item
	if tn, tns := templates.Ref(c); tn != "" {
		items, err := resources.List(ctx, s.kube.Dynamic, resources.Selector{Namespace: tns, OwnerKind: resources.OwnerTemplate, Owner: tn})
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	crs, err := s.kube.Dynamic.Resource(resources.ClusterResourceGVR).Namespace(c.GetNamespace()).List(ctx, metav1.ListOptions{LabelSelector: resources.LabelCluster + "=" + c.GetName()})
	if err != nil {
		return nil, notInstalled(err)
	}
	for _, cr := range crs.Items {
		items, err := resources.List(ctx, s.kube.Dynamic, resources.Selector{Namespace: c.GetNamespace(), OwnerKind: resources.OwnerClusterResource, Owner: cr.GetName()})
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func resourceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a shared Resource (templates and clusters referring to it report it missing)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			if err := s.kube.Dynamic.Resource(resources.ResourceGVR).Namespace(s.templateNS()).Delete(cmd.Context(), args[0], metav1.DeleteOptions{}); err != nil {
				return notInstalled(err)
			}
			logf("Deleted Resource %s/%s", s.templateNS(), args[0])
			return nil
		},
	}
}

// clusterResourceCmd is `hyve cluster resource`: per-cluster extras.
func clusterResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resource",
		Aliases: []string{"resources", "res"},
		Short:   "Add or remove extras on one cluster, without changing its template",
	}
	var f entryFlags
	add := &cobra.Command{
		Use:   "add CLUSTER (--helm CHART | --git REPO | --path DIR | --ref RESOURCE)",
		Short: "Add an extra to a cluster, or replace the one of the same name",
		Example: `  hyve cluster resource add civo-test --helm podinfo --repo https://stefanprodan.github.io/podinfo --version 6.7.1
  hyve cluster resource add web --path ./manifests --name base`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := f.entry()
			if err != nil {
				return err
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
			name := c.GetName() + "-" + e.Name
			u, err := entryObject("ClusterResource", name, c.GetNamespace(), e, map[string]any{"clusterName": c.GetName()})
			if err != nil {
				return err
			}
			u.SetLabels(map[string]string{resources.LabelCluster: c.GetName(), resources.LabelResource: e.Name, resources.ClusterIndexedLabel: "true"})
			if err := s.kube.ApplyObject(ctx, u); err != nil {
				return notInstalled(err)
			}
			logf("Extra %s (%s %s) added to cluster %s", e.Name, e.Kind(), e.Describe(), c.GetName())
			return nil
		},
	}
	f.add(add, true)
	remove := &cobra.Command{
		Use:     "remove CLUSTER NAME",
		Aliases: []string{"rm"},
		Short:   "Remove an extra; Fleet removes it from the cluster",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ri := s.kube.Dynamic.Resource(resources.ClusterResourceGVR).Namespace(s.env())
			list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: resources.LabelCluster + "=" + args[0] + "," + resources.LabelResource + "=" + args[1]})
			if err != nil {
				return notInstalled(err)
			}
			if len(list.Items) == 0 {
				err := ri.Delete(ctx, args[0]+"-"+args[1], metav1.DeleteOptions{})
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("cluster %s has no extra %q", args[0], args[1])
				}
				return err
			}
			for _, cr := range list.Items {
				if err := ri.Delete(ctx, cr.GetName(), metav1.DeleteOptions{}); err != nil {
					return err
				}
			}
			logf("Removed extra %s from cluster %s", args[1], args[0])
			return nil
		},
	}
	list := &cobra.Command{
		Use:     "list CLUSTER",
		Aliases: []string{"ls"},
		Short:   "List what's on a cluster: its template's resources and its extras",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return resourceStatusCmd().RunE(cmd, args)
		},
	}
	cmd.AddCommand(add, remove, list)
	return cmd
}

// targetCluster is the cluster a ClusterResource's Fleet object targets.
func targetCluster(u *unstructured.Unstructured) string {
	ts, _, _ := unstructured.NestedSlice(u.Object, "spec", "targets")
	if len(ts) > 0 {
		if m, ok := ts[0].(map[string]any); ok {
			cn, _ := m["clusterName"].(string)
			return cn
		}
	}
	return ""
}
