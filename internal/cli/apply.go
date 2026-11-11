package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/resources"
)

// manifest is one object read for apply, with the directory its file is
// in (a template's 'bundle: {path: ...}' is read relative to it).
type manifest struct {
	obj *unstructured.Unstructured
	dir string
}

// What each apply takes.
type applyKinds int

const (
	applyAll         applyKinds = iota
	applyTemplates              // templates and ClusterClasses ('hyve template apply')
	applyDeployments            // Deployments and ClusterGroups ('hyve deploy apply')
)

func isFleetKind(o *unstructured.Unstructured) bool {
	if o.GroupVersionKind().Group != "fleet.cattle.io" {
		return false
	}
	switch o.GetKind() {
	case "GitRepo", "HelmOp", "Bundle", "ClusterGroup":
		return true
	}
	return false
}

// accepts says why an apply doesn't take o, or "".
func (k applyKinds) accepts(o *unstructured.Unstructured) string {
	switch k {
	case applyTemplates:
		if isFleetKind(o) {
			return "a Deployment or ClusterGroup: apply it with 'hyve deploy apply' or 'hyve apply'"
		}
	case applyDeployments:
		if !isFleetKind(o) {
			return "not a Deployment (Fleet GitRepo, HelmOp or Bundle) or a ClusterGroup: apply it with 'hyve apply'"
		}
	}
	return ""
}

// applyOrder is when an object is applied: ClusterClasses (and their
// provider templates) before the templates built on them, ClusterGroups
// before the Deployments that target them.
func applyOrder(o *unstructured.Unstructured) int {
	switch {
	case install.IsClassObject(o):
		return 0
	case o.GetKind() == "ClusterGroup":
		return 1
	case isFleetKind(o):
		return 3
	}
	return 2
}

// applyManifests applies the objects in files, those kinds takes, in
// order, then waits for the Deployments among them when w says.
func applyManifests(cmd *cobra.Command, files []string, kinds applyKinds, w waitFlags) error {
	s, err := newSession()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	var all []manifest
	for _, f := range files {
		ms, err := readManifests(f)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if why := kinds.accepts(m.obj); why != "" {
				return fmt.Errorf("%s: %s %s: %s", f, m.obj.GetKind(), m.obj.GetName(), why)
			}
			if m.obj.GetName() == "" {
				return fmt.Errorf("%s: a %s has no metadata.name", f, m.obj.GetKind())
			}
			all = append(all, m)
		}
	}
	if len(all) == 0 {
		return errors.New("no objects in the files given")
	}
	var deployments []*unstructured.Unstructured
	for order := 0; order <= 3; order++ {
		for _, m := range all {
			if applyOrder(m.obj) != order {
				continue
			}
			if err := applyObject(ctx, s, m.obj, m.dir); err != nil {
				return fmt.Errorf("%s %s: %w", m.obj.GetKind(), m.obj.GetName(), err)
			}
			logf("Applied %s %s/%s", m.obj.GetKind(), m.obj.GetNamespace(), m.obj.GetName())
			if order == 3 {
				deployments = append(deployments, m.obj)
			}
		}
	}
	if !w.wait {
		return nil
	}
	deadline := time.Now().Add(w.timeout)
	for _, o := range deployments {
		if err := waitDeployment(ctx, s, o.GetKind(), o.GetNamespace(), o.GetName(), deadline); err != nil {
			return err
		}
	}
	return nil
}

// ApplyCmd is `hyve apply`: anything hyve knows, from YAML.
func ApplyCmd() *cobra.Command {
	var files []string
	var w waitFlags
	cmd := &cobra.Command{
		Use:   "apply -f FILE|DIR|-",
		Short: "Create or update templates, ClusterClasses, Deployments and ClusterGroups from YAML",
		Long: `Creates or updates, from YAML, whatever hyve knows: ClusterTemplates,
ClusterClasses with their provider templates (CivoClusterTemplate,
DockerMachineTemplate...), Deployments (Fleet GitRepos, HelmOps and
Bundles) and ClusterGroups. It's the same whether they exist or not, so a
pipeline or a repository of them can run it every time. ClusterClasses go
before the templates built on them, and groups before the Deployments
that target them. Objects without a namespace go in the template
namespace. '-f -' reads standard input; a directory's .yaml, .yml and
.json files are read, below it too.

'hyve template apply' takes only templates and classes, and 'hyve deploy
apply' only Deployments and groups.`,
		Example: `  hyve apply -f platform/
  sed "s/VERSION/$VERSION/" web.yaml | hyve apply -f - --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return applyManifests(cmd, files, applyAll, w)
		},
	}
	AddGlobalFlags(cmd)
	addTemplateNSFlag(cmd)
	cmd.Flags().StringArrayVarP(&files, "filename", "f", nil, "YAML file, directory, or - for standard input (repeatable)")
	_ = cmd.MarkFlagRequired("filename")
	w.add(cmd)
	return cmd
}

// readManifests reads the objects in a file, in the .yaml, .yml and .json
// files under a directory, or on standard input for "-".
func readManifests(path string) ([]manifest, error) {
	type file struct{ name, dir, content string }
	var files []file
	switch fi, err := os.Stat(path); {
	case path == "-":
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		wd, _ := os.Getwd()
		files = []file{{"standard input", wd, string(raw)}}
	case err != nil:
		return nil, err
	case fi.IsDir():
		read, err := resources.ReadBundle(path)
		if err != nil {
			return nil, err
		}
		for _, f := range read {
			m, _ := f.(map[string]any)
			content, _ := m["content"].(string)
			name, _ := m["name"].(string)
			files = append(files, file{name, filepath.Dir(filepath.Join(path, name)), content})
		}
	default:
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files = []file{{path, filepath.Dir(path), string(raw)}}
	}
	var out []manifest
	for _, f := range files {
		objs, err := kube.Decode([]byte(f.content))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
		for _, o := range objs {
			out = append(out, manifest{obj: o, dir: f.dir})
		}
	}
	return out, nil
}
