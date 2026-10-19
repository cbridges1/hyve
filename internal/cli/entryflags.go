package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/resources"
)

// entryFlags builds a resources.Entry from command-line flags: a Helm
// chart, git paths, local manifests or a ref to a shared Resource.
type entryFlags struct {
	name, namespace                          string
	helm, repo, version, release, valuesFile string
	git, branch, revision, path, ref         string
	gitPaths, sets, secrets                  []string
	correctDrift, allowRef                   bool
}

func (f *entryFlags) add(cmd *cobra.Command, allowRef bool) {
	f.allowRef = allowRef
	fs := cmd.Flags()
	fs.StringVar(&f.name, "name", "", "resource name (default: the chart, repo or ref name)")
	fs.StringVarP(&f.namespace, "target-namespace", "N", "", "namespace for its objects on the cluster")
	fs.BoolVar(&f.correctDrift, "correct-drift", false, "have Fleet undo changes made to its objects on the cluster")
	fs.StringVar(&f.helm, "helm", "", "Helm chart (a name with --repo, or an oci:// URL)")
	fs.StringVar(&f.repo, "repo", "", "Helm repository URL")
	fs.StringVar(&f.version, "version", "", "chart version (pin it in templates)")
	fs.StringVar(&f.release, "release-name", "", "Helm release name (default: the resource name)")
	fs.StringVarP(&f.valuesFile, "values", "f", "", "Helm values file")
	fs.StringArrayVar(&f.sets, "set", nil, "Helm value key=value; dotted keys nest, JSON values are parsed (repeatable)")
	fs.StringVar(&f.git, "git", "", "git repository URL")
	fs.StringVar(&f.branch, "branch", "", "git branch")
	fs.StringVar(&f.revision, "revision", "", "git revision (commit or tag)")
	fs.StringArrayVar(&f.gitPaths, "git-path", nil, "path in the repo (repeatable; default: the whole repo)")
	fs.StringVar(&f.path, "path", "", "local manifests: a file or directory of .yaml/.yml/.json, sent inline")
	fs.StringArrayVar(&f.secrets, "secret", nil, "Secret (in the owner's namespace) for Fleet to copy to each cluster (repeatable)")
	if allowRef {
		fs.StringVar(&f.ref, "ref", "", "a shared Resource (see 'hyve resource create'); --set and -f then override its Helm values")
	}
}

func (f *entryFlags) values() (map[string]any, error) {
	vals := map[string]any{}
	if f.valuesFile != "" {
		raw, err := os.ReadFile(f.valuesFile)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(raw, &vals); err != nil {
			return nil, fmt.Errorf("%s: %w", f.valuesFile, err)
		}
	}
	kv, err := parseKV(f.sets, true)
	if err != nil {
		return nil, err
	}
	for k, v := range kv {
		setPath(vals, k, v)
	}
	return vals, nil
}

func (f *entryFlags) entry() (resources.Entry, error) {
	e := resources.Entry{Name: f.name, Namespace: f.namespace, Secrets: f.secrets}
	n := 0
	for _, set := range []bool{f.helm != "", f.git != "", f.path != "", f.ref != ""} {
		if set {
			n++
		}
	}
	if n != 1 {
		if f.allowRef {
			return e, errors.New("give one of --helm, --git, --path or --ref")
		}
		return e, errors.New("give one of --helm, --git or --path")
	}
	vals, err := f.values()
	if err != nil {
		return e, err
	}
	drift := func(spec map[string]any) {
		if f.correctDrift {
			spec["correctDrift"] = map[string]any{"enabled": true}
		}
	}
	switch {
	case f.helm != "":
		h := map[string]any{}
		repo, chart := f.repo, f.helm
		// Fleet wants an OCI chart as the whole reference in repo.
		switch {
		case strings.HasPrefix(chart, "oci://"):
			repo, chart = chart, ""
		case strings.HasPrefix(repo, "oci://"):
			repo, chart = strings.TrimRight(repo, "/")+"/"+chart, ""
		}
		if repo != "" {
			h["repo"] = repo
		}
		if chart != "" {
			h["chart"] = chart
		}
		if f.version != "" {
			h["version"] = f.version
		}
		if f.release != "" {
			h["releaseName"] = f.release
		}
		if len(vals) > 0 {
			h["values"] = vals
		}
		e.HelmOp = map[string]any{"helm": h}
		drift(e.HelmOp)
		if e.Name == "" {
			e.Name = lastSegment(f.helm)
		}
	case f.git != "":
		g := map[string]any{"repo": f.git}
		if f.branch != "" {
			g["branch"] = f.branch
		}
		if f.revision != "" {
			g["revision"] = f.revision
		}
		if len(f.gitPaths) > 0 {
			ps := make([]any, len(f.gitPaths))
			for i, p := range f.gitPaths {
				ps[i] = p
			}
			g["paths"] = ps
		}
		e.GitRepo = g
		drift(e.GitRepo)
		if e.Name == "" {
			e.Name = strings.ToLower(strings.TrimSuffix(lastSegment(f.git), ".git"))
		}
	case f.path != "":
		files, err := resources.ReadBundle(f.path)
		if err != nil {
			return e, err
		}
		e.Bundle = map[string]any{"resources": files}
		drift(e.Bundle)
		if e.Name == "" {
			abs, _ := filepath.Abs(f.path)
			e.Name = strings.ToLower(strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
		}
	case f.ref != "":
		e.Ref = f.ref
		if len(vals) > 0 {
			e.Values = vals
		}
		if e.Name == "" {
			e.Name = f.ref
		}
	}
	if len(vals) > 0 && f.helm == "" && f.ref == "" {
		return e, errors.New("--set and -f are Helm values; use them with --helm or --ref")
	}
	if err := resources.ValidName(e.Name); err != nil {
		return e, err
	}
	return e, e.Validate(f.allowRef)
}

func lastSegment(s string) string {
	s = strings.TrimRight(s, "/")
	return s[strings.LastIndex(s, "/")+1:]
}

// inlineBundlePaths replaces a CLI-only `bundle: {path: DIR}` in an entry
// with the manifests it names, read relative to baseDir. Fleet's Bundle has
// no path; this is so templates kept as files can point at manifests next
// to them.
func inlineBundlePaths(entries []resources.Entry, baseDir string) error {
	for i := range entries {
		b := entries[i].Bundle
		if b == nil {
			continue
		}
		p, _ := b["path"].(string)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(baseDir, p)
		}
		files, err := resources.ReadBundle(p)
		if err != nil {
			return fmt.Errorf("resource %s: %w", entries[i].Name, err)
		}
		delete(b, "path")
		b["resources"] = files
	}
	return nil
}
