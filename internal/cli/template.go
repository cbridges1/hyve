package cli

import (
	"context"
	"encoding/json"
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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// TemplateCmd is `hyve template`.
func TemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "template",
		Aliases: []string{"templates", "tpl"},
		Short:   "Manage ClusterTemplates: a ClusterClass with defaults, locks, expiry and resources",
	}
	AddGlobalFlags(cmd)
	addTemplateNSFlag(cmd)
	cmd.AddCommand(templateCreateCmd(), applyCmd(), templateListCmd(), templateShowCmd(), templateDeleteCmd(), templateResourceCmd())
	return cmd
}

// getTemplate reads a template from the template namespace; "ns/name"
// names one elsewhere.
func (s *session) getTemplate(ctx context.Context, ref string) (*templates.Template, error) {
	ns, name := s.templateNS(), ref
	if a, b, ok := strings.Cut(ref, "/"); ok {
		ns, name = a, b
	}
	u, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) && !strings.Contains(err.Error(), "could not find the requested resource") {
			return nil, fmt.Errorf("ClusterTemplate %q not found in namespace %q", name, ns)
		}
		return nil, notInstalled(err)
	}
	return templates.FromUnstructured(u)
}

func templateCreateCmd() *cobra.Command {
	var (
		t                         templates.Template
		class, fromClass, classNS string
		workerClass, workerType   string
		workers, controlPlane     int64
		sets, labels              []string
		dryRun                    bool
	)
	cmd := &cobra.Command{
		Use:   "create NAME (--class CLASS | --from-clusterclass CLASS)",
		Short: "Create a ClusterTemplate",
		Long: `Creates a ClusterTemplate in the template namespace, beside its ClusterClass.
--from-clusterclass also copies every variable the ClusterClass declares,
with its default (or null), as a starting point to edit. Add resources with
'hyve template resource add'; for anything more, write the YAML and use
'hyve template apply -f'.`,
		Example: `  hyve template create capd-small --class capd --workers 1 --schedule "0 20 * * *"
  hyve template create gke-small --from-clusterclass gke --set project=my-proj --lock project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (class == "") == (fromClass == "") {
				return errors.New("give exactly one of --class or --from-clusterclass")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			t.Name, t.Namespace = args[0], s.templateNS()
			t.Spec.ClusterClassRef = templates.ClassRef{Name: class + fromClass, Namespace: classNS}
			cls, err := s.getClass(ctx, t.ClassNamespace(), t.Spec.ClusterClassRef.Name)
			if err != nil {
				return err
			}
			t.Spec.Variables = map[string]any{}
			if fromClass != "" {
				t.Spec.Variables = templates.ScaffoldVariables(cls)
			}
			set, err := parseKV(sets, true)
			if err != nil {
				return err
			}
			for k, v := range set {
				t.Spec.Variables[k] = v
			}
			if lbl, err := parseLabels(labels); err != nil {
				return err
			} else if len(lbl) > 0 {
				t.Spec.Labels = lbl
			}
			if cmd.Flags().Changed("workers") || workerClass != "" || workerType != "" {
				t.Spec.Workers = &templates.Workers{Class: workerClass, Type: workerType}
				if cmd.Flags().Changed("workers") {
					t.Spec.Workers.Count = &workers
				}
			}
			if cmd.Flags().Changed("control-plane") {
				t.Spec.ControlPlane = &templates.Replicas{Replicas: controlPlane}
			}
			if err := templates.Validate(&t); err != nil {
				return err
			}
			u, err := t.ToUnstructured()
			if err != nil {
				return err
			}
			if dryRun {
				return printYAML(u.Object)
			}
			if _, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(t.Namespace).Create(ctx, u, metav1.CreateOptions{FieldManager: kube.FieldManager}); err != nil {
				return notInstalled(err)
			}
			logf("Created ClusterTemplate %s/%s", t.Namespace, t.Name)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&class, "class", "", "ClusterClass to build from")
	f.StringVar(&fromClass, "from-clusterclass", "", "ClusterClass to build from, copying its variables and defaults")
	f.StringVar(&classNS, "class-namespace", "", "the ClusterClass's namespace (default: the template's)")
	f.StringVar(&t.Spec.Description, "description", "", "description")
	f.StringVar(&t.Spec.Version, "version", "", "Kubernetes version (default: the class's hyve.io/default-version)")
	f.Int64Var(&controlPlane, "control-plane", 0, "control-plane replicas (default: 1 for a machine-based control plane, none for a managed one)")
	f.Int64Var(&workers, "workers", 1, "worker replicas")
	f.StringArrayVar(&sets, "set", nil, "variable name=value (repeatable; JSON values are parsed)")
	f.StringSliceVar(&t.Spec.LockedVariables, "lock", nil, "variables clusters can't change (repeatable)")
	f.StringVar(&t.Spec.Schedule, "schedule", "", `cron schedule; each cluster expires at its next occurrence (e.g. "0 20 * * 5")`)
	f.StringArrayVar(&labels, "label", nil, "label key=value on every cluster (repeatable)")
	f.BoolVar(&dryRun, "dry-run", false, "print the template instead of creating it")
	f.StringVar(&workerClass, "worker-class", "", "worker class (default: the ClusterClass's first)")
	f.StringVar(&workerType, "worker-type", "", "machineDeployment or machinePool (default: whichever the ClusterClass defines)")
	return cmd
}

func printYAML(v any) error {
	out, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

// applyCmd is `template apply -f` (also `resource apply -f`): it creates
// or updates ClusterTemplates, Resources and ClusterResources from YAML.
func applyCmd() *cobra.Command {
	var files []string
	cmd := &cobra.Command{
		Use:   "apply -f FILE",
		Short: "Create or update ClusterTemplates, Resources and ClusterResources from YAML",
		Long: `Applies hyve objects from YAML files. Objects without a namespace go in the
template namespace (a ClusterResource in the environment). A bundle entry
may give 'path: DIR' instead of resources; the manifests there, relative to
the file, are sent inline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
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
					if err := applyObject(cmd.Context(), s, o, filepath.Dir(f)); err != nil {
						return fmt.Errorf("%s: %s %s: %w", f, o.GetKind(), o.GetName(), err)
					}
					logf("Applied %s %s/%s", o.GetKind(), o.GetNamespace(), o.GetName())
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVarP(&files, "filename", "f", nil, "YAML file (repeatable)")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func applyObject(ctx context.Context, s *session, o *unstructured.Unstructured, baseDir string) error {
	if o.GroupVersionKind().Group != "hyve.io" {
		return errors.New("not a hyve.io object; apply it with kubectl")
	}
	switch o.GetKind() {
	case "ClusterTemplate":
		if o.GetNamespace() == "" {
			o.SetNamespace(s.templateNS())
		}
		t, err := templates.FromUnstructured(o)
		if err != nil {
			return err
		}
		if err := inlineBundlePaths(t.Spec.Resources, baseDir); err != nil {
			return err
		}
		if err := templates.Validate(t); err != nil {
			return err
		}
		spec, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&t.Spec)
		if err != nil {
			return err
		}
		o.Object["spec"] = spec
	case "Resource", "ClusterResource":
		if o.GetNamespace() == "" {
			if o.GetKind() == "Resource" {
				o.SetNamespace(s.templateNS())
			} else {
				o.SetNamespace(s.env())
			}
		}
		e, err := resources.EntryFromSpec(o)
		if err != nil {
			return err
		}
		es := []resources.Entry{e}
		if err := inlineBundlePaths(es, baseDir); err != nil {
			return err
		}
		if err := es[0].Validate(o.GetKind() == "ClusterResource"); err != nil {
			return err
		}
		if o.GetKind() == "ClusterResource" {
			if cn, _, _ := unstructured.NestedString(o.Object, "spec", "clusterName"); cn == "" {
				return errors.New("spec.clusterName is required")
			}
		}
		if es[0].Bundle != nil {
			_ = unstructured.SetNestedField(o.Object, es[0].Bundle, "spec", "bundle")
		}
	default:
		return errors.New("hyve applies ClusterTemplates, Resources and ClusterResources")
	}
	return notInstalled(s.kube.ApplyObject(ctx, o))
}

func templateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List ClusterTemplates",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			list, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(s.templateNS()).List(cmd.Context(), metav1.ListOptions{})
			if err != nil {
				return notInstalled(err)
			}
			sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].GetName() < list.Items[j].GetName() })
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tCLASS\tVERSION\tSCHEDULE\tLOCKED\tRESOURCES\tREADY\tDESCRIPTION")
			for i := range list.Items {
				t, err := templates.FromUnstructured(&list.Items[i])
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", t.Name, t.Spec.ClusterClassRef.Name, dash(t.Spec.Version), dash(t.Spec.Schedule),
					dash(strings.Join(t.Spec.LockedVariables, ",")), len(t.Spec.Resources), readyCondition(&list.Items[i]), t.Spec.Description)
			}
			return w.Flush()
		},
	}
}

// readyCondition is an object's Ready condition: "True", or "False: why".
func readyCondition(u *unstructured.Unstructured) string {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		if m, ok := c.(map[string]any); ok && m["type"] == "Ready" {
			if m["status"] == "True" {
				return "True"
			}
			return fmt.Sprintf("%v: %v", m["status"], m["message"])
		}
	}
	return "-"
}

func templateShowCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a ClusterTemplate, its resources' state and its clusters",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			t, err := s.getTemplate(ctx, args[0])
			if err != nil {
				return err
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			if output != "" {
				u, err := s.kube.Dynamic.Resource(templates.GVR).Namespace(t.Namespace).Get(ctx, t.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				return printObjects(output, tidy(u))
			}
			fmt.Printf("Name:          %s/%s\n", t.Namespace, t.Name)
			if t.Spec.Description != "" {
				fmt.Printf("Description:   %s\n", t.Spec.Description)
			}
			fmt.Printf("ClusterClass:  %s/%s\n", t.ClassNamespace(), t.Spec.ClusterClassRef.Name)
			fmt.Printf("Version:       %s\n", dash(t.Spec.Version))
			if w := t.Spec.Workers; w != nil {
				count := "-"
				if w.Count != nil {
					count = fmt.Sprint(*w.Count)
				}
				fmt.Printf("Workers:       %s × %s (%s)\n", count, dash(w.Class), dash(w.Type))
			}
			names := make([]string, 0, len(t.Spec.Variables))
			for k := range t.Spec.Variables {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				v, _ := json.Marshal(t.Spec.Variables[k])
				lock := ""
				if t.IsLocked(k) {
					lock = " (locked)"
				}
				fmt.Printf("Variable:      %s=%s%s\n", k, v, lock)
			}
			if t.Spec.Schedule != "" {
				fmt.Printf("Schedule:      %s (clusters expire at its next run)\n", t.Spec.Schedule)
			}
			for k, v := range t.Spec.Labels {
				fmt.Printf("Label:         %s=%s\n", k, v)
			}
			items, err := resources.List(ctx, s.kube.Dynamic, resources.Selector{Namespace: t.Namespace, OwnerKind: resources.OwnerTemplate, Owner: t.Name})
			if err == nil {
				byName := map[string]resources.Item{}
				for _, it := range items {
					byName[it.Name] = it
				}
				for _, e := range t.Spec.Resources {
					state := "not created yet"
					if it, ok := byName[e.Name]; ok {
						ready, st := it.Ready()
						state = strings.TrimSpace(dash(ready) + " " + st)
					}
					fmt.Printf("Resource:      %s (%s %s): %s\n", e.Name, e.Kind(), e.Describe(), state)
				}
			}
			list, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).List(ctx, metav1.ListOptions{
				LabelSelector: templates.LabelTemplate + "=" + t.Name + "," + templates.LabelTemplateNamespace + "=" + t.Namespace,
			})
			if err == nil {
				var names []string
				for _, c := range list.Items {
					names = append(names, c.GetNamespace()+"/"+c.GetName())
				}
				fmt.Printf("Clusters:      %s\n", dash(strings.Join(names, ", ")))
			}
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

func templateDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a ClusterTemplate and its resources (clusters made from it stay)",
		Long: `Deletes a ClusterTemplate. The Fleet objects made from its resources go with
it, so Fleet removes those resources from its clusters. The clusters
themselves stay, and keep their variables.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			if err := s.kube.Dynamic.Resource(templates.GVR).Namespace(s.templateNS()).Delete(cmd.Context(), args[0], metav1.DeleteOptions{}); err != nil {
				return notInstalled(err)
			}
			logf("Deleted ClusterTemplate %s/%s", s.templateNS(), args[0])
			return nil
		},
	}
}

func templateResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resource",
		Aliases: []string{"resources", "res"},
		Short:   "Add or remove a resource on every cluster made from a template",
	}
	var f entryFlags
	add := &cobra.Command{
		Use:   "add TEMPLATE (--helm CHART | --git REPO | --path DIR | --ref RESOURCE)",
		Short: "Add a resource to a template, or replace the one of the same name",
		Example: `  hyve template resource add civo --helm traefik --repo https://traefik.github.io/charts --version 37.1.0 -N traefik --set service.type=LoadBalancer
  hyve template resource add civo --ref external-dns --secret cloudflare-dns`,
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
			t, err := s.getTemplate(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			replaced := false
			for i := range t.Spec.Resources {
				if t.Spec.Resources[i].Name == e.Name {
					t.Spec.Resources[i], replaced = e, true
				}
			}
			if !replaced {
				t.Spec.Resources = append(t.Spec.Resources, e)
			}
			if err := saveTemplate(cmd.Context(), s, t); err != nil {
				return err
			}
			logf("Resource %s (%s %s) is on template %s; hyve-controller delivers it to its clusters", e.Name, e.Kind(), e.Describe(), t.Name)
			return nil
		},
	}
	f.add(add, true)
	remove := &cobra.Command{
		Use:     "remove TEMPLATE NAME",
		Aliases: []string{"rm"},
		Short:   "Remove a resource from a template; Fleet removes it from its clusters",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			t, err := s.getTemplate(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			kept := t.Spec.Resources[:0]
			for _, r := range t.Spec.Resources {
				if r.Name != args[1] {
					kept = append(kept, r)
				}
			}
			if len(kept) == len(t.Spec.Resources) {
				return fmt.Errorf("template %s has no resource %q", t.Name, args[1])
			}
			t.Spec.Resources = kept
			if err := saveTemplate(cmd.Context(), s, t); err != nil {
				return err
			}
			logf("Removed resource %s from template %s", args[1], t.Name)
			return nil
		},
	}
	cmd.AddCommand(add, remove)
	return cmd
}

// saveTemplate replaces a template's spec outright: list items removed
// here must go, which server-side apply wouldn't do for fields another
// manager set.
func saveTemplate(ctx context.Context, s *session, t *templates.Template) error {
	if err := templates.Validate(t); err != nil {
		return err
	}
	u, err := t.ToUnstructured()
	if err != nil {
		return err
	}
	ri := s.kube.Dynamic.Resource(templates.GVR).Namespace(t.Namespace)
	cur, err := ri.Get(ctx, t.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	cur.Object["spec"] = u.Object["spec"]
	_, err = ri.Update(ctx, cur, metav1.UpdateOptions{FieldManager: kube.FieldManager})
	return err
}
