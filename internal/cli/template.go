package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// TemplateCmd is `hyve template`.
func TemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "template",
		Aliases: []string{"templates", "tpl"},
		Short:   "Manage ClusterTemplates: a ClusterClass with defaults, locks, expiry and adoption",
	}
	AddGlobalFlags(cmd)
	addTemplateNSFlag(cmd)
	cmd.AddCommand(templateCreateCmd(), applyCmd(), templateListCmd(), templateShowCmd(), templateDeleteCmd(), templateResourceCmd(),
		RenderCmd(), ConvertCmd())
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
with its default (or null), as a starting point to edit. For anything
more, write the YAML and use 'hyve template apply -f'. What its clusters
run is deployed to it: 'hyve deploy create NAME ... --to template/NAME'.`,
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

// applyCmd is `template apply -f`: ClusterTemplates and ClusterClasses
// (with their provider templates) from YAML. 'hyve apply' takes
// Deployments and groups too.
func applyCmd() *cobra.Command {
	var files []string
	cmd := &cobra.Command{
		Use:   "apply -f FILE|DIR|-",
		Short: "Create or update ClusterTemplates and ClusterClasses from YAML",
		Long: `Applies ClusterTemplates from YAML files, and ClusterClasses with their
provider templates (CivoClusterTemplate, DockerMachineTemplate...), classes
first. Objects without a namespace go in the template namespace. For
Deployments and ClusterGroups use 'hyve deploy apply', or 'hyve apply' for
everything at once.

A template's resources (before Deployments) are still accepted:
hyve-controller turns each into a Deployment aimed at the template.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return applyManifests(cmd, files, applyTemplates, waitFlags{})
		},
	}
	cmd.Flags().StringArrayVarP(&files, "filename", "f", nil, "YAML file, directory, or - for standard input (repeatable)")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func applyObject(ctx context.Context, s *session, o *unstructured.Unstructured, baseDir string) error {
	// A ClusterClass of your own, with its provider templates, goes beside
	// the templates built on it.
	if install.IsClassObject(o) {
		if o.GetNamespace() == "" {
			o.SetNamespace(s.templateNS())
		}
		return s.kube.ApplyObject(ctx, o)
	}
	// Deployments and groups (Fleet's own objects) go in the template
	// namespace unless they name one.
	if g := o.GroupVersionKind().Group; g == "fleet.cattle.io" {
		switch o.GetKind() {
		case "GitRepo", "HelmOp", "Bundle", "ClusterGroup":
			if o.GetNamespace() == "" {
				o.SetNamespace(s.templateNS())
			}
			return s.kube.ApplyObject(ctx, o)
		}
		return errors.New("only Fleet GitRepos, HelmOps, Bundles and ClusterGroups; apply others with kubectl")
	}
	if o.GroupVersionKind().Group != "hyve.io" {
		return errors.New("not a hyve.io object, a Deployment (Fleet GitRepo, HelmOp or Bundle) or a ClusterClass; apply it with kubectl")
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
		return errors.New("hyve applies ClusterTemplates, Resources, ClusterResources and ClusterClasses")
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
			items, _ := delivery.List(cmd.Context(), s.kube.Dynamic, s.templateNS(), []string{s.templateNS()})
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tCLASS\tVERSION\tSCHEDULE\tLOCKED\tDEPLOYMENTS\tDESCRIPTION")
			for i := range list.Items {
				t, err := templates.FromUnstructured(&list.Items[i])
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", t.Name, t.Spec.ClusterClassRef.Name, dash(t.Spec.Version), dash(t.Spec.Schedule),
					dash(strings.Join(t.Spec.LockedVariables, ",")), len(delivery.ForTemplate(items, t.Name, t.Spec.ClusterClassRef.Name)), t.Spec.Description)
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
		Short: "Show a ClusterTemplate, the Deployments aimed at it and its clusters",
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
			if items, err := delivery.List(ctx, s.kube.Dynamic, t.Namespace, []string{t.Namespace}); err == nil {
				for _, it := range delivery.ForTemplate(items, t.Name, t.Spec.ClusterClassRef.Name) {
					fmt.Printf("Deployment:    %s (%s %s, to %s): %s\n", it.Name, it.Kind, it.Source, delivery.DescribeTargets(it.Targets),
						strings.TrimSpace(dash(it.Ready)+" "+it.State))
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
		Short: "Delete a ClusterTemplate (clusters made from it stay)",
		Long: `Deletes a ClusterTemplate, and the Fleet group hyve keeps for it, so
Deployments aimed only at the template reach none of its clusters any more
and Fleet removes them there. The Deployments themselves, and the clusters,
stay; the clusters keep their variables.`,
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
	return movedCmd("resource", "Replaced by 'hyve deploy'",
		"a template's resources are Deployments now: 'hyve deploy create NAME ... --to template/TEMPLATE' adds one, "+
			"'hyve deploy update NAME --remove-to template/TEMPLATE' or 'hyve deploy delete NAME' removes one",
		"add", "remove")
}
