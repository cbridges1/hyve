package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
)

// What the UI's View shows, from the CLI: each object a page lists, as
// YAML or JSON.

var deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

func addOutputFlag(cmd *cobra.Command, output *string) {
	cmd.Flags().StringVarP(output, "output", "o", "", `"yaml" or "json" for the object itself`)
}

func checkOutput(output string) error {
	switch output {
	case "", "yaml", "json":
		return nil
	}
	return fmt.Errorf(`--output is "yaml" or "json", not %q`, output)
}

// tidy drops what 'kubectl get' users don't need to see: managedFields,
// and kubectl's copy of the last object it applied (a Secret's includes its
// values).
func tidy(u *unstructured.Unstructured) map[string]any {
	o := u.DeepCopy().Object
	unstructured.RemoveNestedField(o, "metadata", "managedFields")
	unstructured.RemoveNestedField(o, "metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration")
	if a, ok, _ := unstructured.NestedMap(o, "metadata", "annotations"); ok && len(a) == 0 {
		unstructured.RemoveNestedField(o, "metadata", "annotations")
	}
	return o
}

// printObjects prints objects as YAML documents, or as JSON (one object,
// or a list).
func printObjects(output string, objs ...any) error {
	if output == "json" {
		var v any = objs
		if len(objs) == 1 {
			v = objs[0]
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Println(string(out))
		return err
	}
	for i, o := range objs {
		if i > 0 {
			fmt.Println("---")
		}
		if err := printYAML(o); err != nil {
			return err
		}
	}
	return nil
}

// repoShowCmd shows one Fleet GitRepo.
func repoShowCmd() *cobra.Command {
	var workspace, output string
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Fleet GitRepo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var found []*unstructured.Unstructured
			if workspace != "" {
				g, err := s.kube.Dynamic.Resource(resources.GitRepoGVR).Namespace(workspace).Get(ctx, args[0], metav1.GetOptions{})
				if err != nil {
					return err
				}
				found = append(found, g)
			} else {
				list, err := s.kube.Dynamic.Resource(resources.GitRepoGVR).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for i := range list.Items {
					if list.Items[i].GetName() == args[0] {
						found = append(found, &list.Items[i])
					}
				}
			}
			switch len(found) {
			case 0:
				return fmt.Errorf("no GitRepo %q", args[0])
			case 1:
				return printObjects(output, tidy(found[0]))
			}
			var where []string
			for _, g := range found {
				where = append(where, g.GetNamespace())
			}
			return fmt.Errorf("GitRepo %q is in several workspaces (%s); pick one with --workspace", args[0], strings.Join(where, ", "))
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "the Fleet workspace (default: wherever it is)")
	addOutputFlag(cmd, &output)
	return cmd
}

// providerShowCmd shows a provider: the object it was installed from
// (Turtles' or the CAPI Operator's), when there is one, and its controller.
func providerShowCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Cluster API provider and its controller",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			rows, err := listProviders(cmd, s)
			if err != nil {
				return err
			}
			var objs []any
			for _, r := range rows {
				if r.name != args[0] {
					continue
				}
				var gvr schema.GroupVersionResource
				switch {
				case s.turtles():
					gvr = turtlesProviderGVR
				case operatorKinds[r.typ] != "":
					gvr = operatorGVR(operatorKinds[r.typ])
				}
				if !gvr.Empty() {
					if p, err := s.kube.Dynamic.Resource(gvr).Namespace(r.ns).Get(ctx, r.name, metav1.GetOptions{}); err == nil {
						objs = append(objs, tidy(p))
					}
				}
				ds, err := s.kube.Dynamic.Resource(deploymentGVR).Namespace(r.ns).List(ctx, metav1.ListOptions{LabelSelector: "cluster.x-k8s.io/provider"})
				if err == nil {
					for i := range ds.Items {
						objs = append(objs, tidy(&ds.Items[i]))
					}
				}
			}
			if len(objs) == 0 {
				return fmt.Errorf("no provider %q", args[0])
			}
			return printObjects(output, objs...)
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

// secretsShowCmd shows one Secret: its metadata and keys, and with
// --reveal its values.
func secretsShowCmd() *cobra.Command {
	var (
		namespace, output string
		reveal            bool
	)
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a Secret's keys (and with --reveal, its values)",
		Long: `Shows a Secret in the template namespace (or --namespace): its type,
labels and keys. Values stay hidden unless you pass --reveal. It's read as
you, so it needs RBAC to get Secrets there.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ns := namespace
			if ns == "" {
				ns = s.templateNS()
			}
			sec, err := s.kube.Dynamic.Resource(secretGVR).Namespace(ns).Get(cmd.Context(), args[0], metav1.GetOptions{})
			if err != nil {
				return err
			}
			o := tidy(sec)
			data, _, _ := unstructured.NestedMap(o, "data")
			keys := make([]string, 0, len(data))
			for k := range data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			delete(o, "stringData")
			if reveal {
				// Decoded, so they read as written.
				plain := map[string]any{}
				for _, k := range keys {
					b, err := base64.StdEncoding.DecodeString(fmt.Sprint(data[k]))
					if err != nil {
						plain[k] = data[k]
						continue
					}
					plain[k] = string(b)
				}
				delete(o, "data")
				o["stringData"] = plain
			} else {
				hidden := map[string]any{}
				for _, k := range keys {
					hidden[k] = "(hidden; --reveal shows it)"
				}
				o["data"] = hidden
			}
			return printObjects(output, o)
		},
	}
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "the Secret's namespace (default: the template namespace)")
	cmd.Flags().BoolVar(&reveal, "reveal", false, "print the values")
	addOutputFlag(cmd, &output)
	return cmd
}

// envShowCmd shows an environment: its namespace and the clusters in it.
func envShowCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show an environment (its namespace) and the clusters in it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ns, err := s.kube.Dynamic.Resource(namespaceGVR).Get(ctx, args[0], metav1.GetOptions{})
			if err != nil {
				return err
			}
			cl, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(args[0]).List(ctx, metav1.ListOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			if output != "" {
				return printObjects(output, tidy(ns))
			}
			if err := printYAML(tidy(ns)); err != nil {
				return err
			}
			var names []string
			if cl != nil {
				for _, c := range cl.Items {
					names = append(names, c.GetName())
				}
			}
			sort.Strings(names)
			_, err = fmt.Fprintf(os.Stdout, "# clusters: %s\n", dash(strings.Join(names, ", ")))
			return err
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}
