package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/hyveimport"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/templates"
)

var (
	oldHyveTemplateGVR = schema.GroupVersionResource{Group: "hyve.io", Version: "v1alpha1", Resource: "templates"}
	herdTemplateGVR    = schema.GroupVersionResource{Group: "herd.io", Version: "v1alpha1", Resource: "clustertemplates"}
)

// ImportCmd is `hyve import`: old hyve Templates and herd ClusterTemplates
// into hyve ClusterTemplates.
func ImportCmd() *cobra.Command {
	var (
		files                         []string
		fromCluster, apply, prefixNS  bool
		relabel                       bool
		kubeconfig, kubeContext, srNS string
	)
	cmd := &cobra.Command{
		Use:   "import (-f FILE|DIR ... | --from-cluster)",
		Short: "Convert old hyve Templates and herd ClusterTemplates into hyve ClusterTemplates",
		Long: `Reads old hyve Templates (built with hyve-capi-module, hyve-civo-module or
hyve-unraid-k3s-module) and herd ClusterTemplates (herd.io), from YAML files
or from a cluster, and converts them into hyve ClusterTemplates: flavor
presets expanded, params mapped onto the ClusterClass, lockParams onto
locked variables, the schedule, and Helm and git resources as Fleet specs.
Prints them as YAML, or creates them in the template namespace with
--apply. What hyve can't express (workflows, other modules, locks on
non-variables) is listed on stderr.

--relabel-clusters (with --apply) also labels the clusters herd made from
each herd template with the new hyve.io/template labels, so they get the
template's resources and locks.`,
		Example: `  hyve import -f ./templates                                # print
  hyve import --from-cluster --kubeconfig unraid.yaml --apply --relabel-clusters`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (len(files) > 0) == fromCluster {
				return errors.New("give -f or --from-cluster")
			}
			ctx := cmd.Context()
			var objs []*unstructured.Unstructured
			if fromCluster {
				rules := clientcmd.NewDefaultClientConfigLoadingRules()
				if kubeconfig != "" {
					rules.ExplicitPath = kubeconfig
				}
				cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
				if err != nil {
					return err
				}
				dc, err := dynamic.NewForConfig(cfg)
				if err != nil {
					return err
				}
				for _, gvr := range []schema.GroupVersionResource{oldHyveTemplateGVR, herdTemplateGVR} {
					list, err := dc.Resource(gvr).Namespace(srNS).List(ctx, metav1.ListOptions{})
					if err != nil {
						logf("note: no %s on that cluster (%v)", gvr.GroupResource(), err)
						continue
					}
					for i := range list.Items {
						objs = append(objs, &list.Items[i])
					}
				}
			} else {
				for _, f := range files {
					found, err := readImportable(f)
					if err != nil {
						return err
					}
					objs = append(objs, found...)
				}
			}
			if len(objs) == 0 {
				return errors.New("no hyve Templates or herd ClusterTemplates found")
			}

			var s *session
			ns := tplNSFlag
			if apply {
				var err error
				if s, err = newSession(); err != nil {
					return err
				}
				ns = s.templateNS()
			}
			if ns == "" {
				ns = DefaultTemplateNamespace
			}

			var out []string
			imported, skipped := 0, 0
			for _, r := range hyveimport.ConvertAll(objs, ns, prefixNS) {
				o := r.Object
				name := ""
				if r.Template != nil {
					name = r.Template.Name
				}
				src := strings.TrimPrefix(r.Source, "/")
				if r.Skipped != "" {
					logf("skip %s: %s", src, r.Skipped)
					skipped++
					continue
				}
				for _, n := range r.Notes {
					logf("note %s → %s: %s", src, name, n)
				}
				u, err := r.Template.ToUnstructured()
				if err != nil {
					return err
				}
				if apply {
					if err := applyObject(ctx, s, u, "."); err != nil {
						logf("skip %s: %v", src, err)
						skipped++
						continue
					}
					logf("imported %s → ClusterTemplate %s/%s", src, ns, name)
					if relabel && r.Herd {
						if err := relabelHerdClusters(ctx, s, o, r.Template); err != nil {
							return err
						}
					}
				} else {
					if err := templates.Validate(r.Template); err != nil && !strings.Contains(err.Error(), "bundle.resources is empty") {
						logf("skip %s: %v", src, err)
						skipped++
						continue
					}
					raw, err := yaml.Marshal(u.Object)
					if err != nil {
						return err
					}
					out = append(out, fmt.Sprintf("# from %s %s\n%s", o.GetKind(), src, raw))
				}
				imported++
			}
			if !apply {
				fmt.Print(strings.Join(out, "---\n"))
			}
			logf("%d imported, %d skipped", imported, skipped)
			return nil
		},
	}
	f := cmd.Flags()
	AddGlobalFlags(cmd)
	addTemplateNSFlag(cmd)
	f.StringArrayVarP(&files, "filename", "f", nil, "file or directory of old hyve Templates or herd ClusterTemplates (repeatable)")
	f.BoolVar(&fromCluster, "from-cluster", false, "read them from a cluster (see --source-kubeconfig)")
	f.StringVar(&kubeconfig, "source-kubeconfig", "", "kubeconfig for --from-cluster")
	f.StringVar(&kubeContext, "source-context", "", "kubeconfig context for --from-cluster")
	f.StringVar(&srNS, "source-namespace", "", "only this namespace's templates (default: all)")
	f.BoolVar(&prefixNS, "prefix-namespace", false, "name every ClusterTemplate <old namespace>-<name>")
	f.BoolVar(&apply, "apply", false, "create the ClusterTemplates instead of printing them")
	f.BoolVar(&relabel, "relabel-clusters", false, "with --apply, label clusters made from herd templates with the hyve.io/template labels")
	return cmd
}

// relabelHerdClusters labels each Cluster herd made from a herd template
// with the matching hyve template.
func relabelHerdClusters(ctx context.Context, s *session, herdTpl *unstructured.Unstructured, t *templates.Template) error {
	list, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(herdTpl.GetNamespace()).List(ctx, metav1.ListOptions{LabelSelector: "herd.io/template=" + herdTpl.GetName()})
	if err != nil {
		return err
	}
	for _, c := range list.Items {
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"labels": map[string]any{
			templates.LabelTemplate: t.Name, templates.LabelTemplateNamespace: t.Namespace,
		}}})
		if _, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(c.GetNamespace()).Patch(ctx, c.GetName(), types.MergePatchType, patch, metav1.PatchOptions{FieldManager: kube.FieldManager}); err != nil {
			return err
		}
		logf("  labelled cluster %s/%s with template %s/%s", c.GetNamespace(), c.GetName(), t.Namespace, t.Name)
	}
	return nil
}

func readImportable(path string) ([]*unstructured.Unstructured, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	if info.IsDir() {
		for _, ext := range []string{"*.yaml", "*.yml"} {
			m, _ := filepath.Glob(filepath.Join(path, ext))
			paths = append(paths, m...)
		}
	} else {
		paths = []string{path}
	}
	var out []*unstructured.Unstructured
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		objs, err := kube.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, o := range objs {
			if (o.GetKind() == "Template" && strings.HasPrefix(o.GetAPIVersion(), "hyve.io/")) || hyveimport.IsHerdTemplate(o) {
				out = append(out, o)
			}
		}
	}
	return out, nil
}
