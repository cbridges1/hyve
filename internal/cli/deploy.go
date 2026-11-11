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

	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// DeployCmd is `hyve deploy`: what goes on clusters, as Fleet GitRepos,
// HelmOps and Bundles aimed at templates, classes, environments and
// clusters (internal/delivery).
func DeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deploy",
		Aliases: []string{"deployment", "deployments"},
		Short:   "Deliver Helm charts, git paths and manifests to clusters (Fleet)",
		Long: `A Deployment is one Fleet object: a GitRepo (--git), a HelmOp (--helm) or a
Bundle (--path, manifests sent inline). It's delivered to every cluster its
targets name, now and later:

  template/NAME      every cluster made from a template
  class/NAME         every cluster of a ClusterClass (e.g. every Civo cluster)
  env/NAME           every cluster in an environment
  cluster/ENV/NAME   one cluster
  group/NAME         a Fleet ClusterGroup of your own
  all                every cluster hyve manages
  host               the management cluster

hyve keeps a Fleet ClusterGroup for each template and environment; CAAPF
keeps one for each ClusterClass. A Deployment lives in the template
namespace, or in an environment's namespace when that environment's
clusters are all it targets, or in fleet-local for the host. Deployments
written by hand (in git, with kubectl) are listed the same way.`,
	}
	AddGlobalFlags(cmd)
	cmd.AddCommand(deployListCmd(), deployShowCmd(), deployCreateCmd(), deployUpdateCmd(), deployApplyCmd(),
		deployWaitCmd(), deployPauseCmd(true), deployPauseCmd(false), deployForceCmd(), deployDeleteCmd(), SecretsCmd())
	return cmd
}

// deployNamespaces is where `hyve deploy` looks: the template namespace,
// the environments and fleet-local.
func (s *session) deployNamespaces(ctx context.Context) ([]string, error) {
	ns, err := delivery.Namespaces(ctx, s.kube.Dynamic, s.templateNS())
	if apierrors.IsForbidden(err) {
		// Someone who can't list namespaces (an environment member, a CI
		// user) looks where they work: the template namespace and their
		// environment.
		return []string{s.templateNS(), s.env(), delivery.HostWorkspace}, nil
	}
	if err != nil {
		return nil, notInstalled(err)
	}
	return ns, nil
}

// findDeployment finds a Deployment by name, in ns or wherever it is.
func (s *session) findDeployment(ctx context.Context, name, ns string) (*delivery.Item, error) {
	var nss []string
	if ns != "" {
		nss = []string{ns}
	} else {
		var err error
		if nss, err = s.deployNamespaces(ctx); err != nil {
			return nil, err
		}
	}
	items, err := delivery.List(ctx, s.kube.Dynamic, s.templateNS(), nss)
	if err != nil {
		return nil, err
	}
	var found []delivery.Item
	for _, it := range items {
		if it.Name == name {
			found = append(found, it)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no Deployment %q ('hyve deploy list' lists them)", name)
	case 1:
		return &found[0], nil
	}
	var where []string
	for _, f := range found {
		where = append(where, f.Namespace)
	}
	return nil, fmt.Errorf("Deployment %q is in %s: give -n", name, strings.Join(where, " and "))
}

func deployListCmd() *cobra.Command {
	var ns string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Deployments: source, targets, rollout and how many clusters are ready",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			nss := []string{ns}
			if ns == "" {
				if nss, err = s.deployNamespaces(ctx); err != nil {
					return err
				}
			}
			items, err := delivery.List(ctx, s.kube.Dynamic, s.templateNS(), nss)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAMESPACE\tNAME\tSOURCE\tTARGETS\tROLLOUT\tREADY\tSTATE")
			for _, it := range items {
				state := it.State
				if it.Paused {
					state = "Paused " + state
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", it.Namespace, it.Name, it.Source, dash(delivery.DescribeTargets(it.Targets)),
					it.Rollout, dash(it.Ready), dash(strings.TrimSpace(state)))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "only this namespace (default: the template namespace, the environments and fleet-local)")
	return cmd
}

func deployShowCmd() *cobra.Command {
	var ns, output string
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "A Deployment's source, targets, rollout, and its state on each cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			it, err := s.findDeployment(ctx, args[0], ns)
			if err != nil {
				return err
			}
			if output != "" {
				return printObjects(output, it.Object.Object)
			}
			fmt.Printf("Name:       %s\nNamespace:  %s\nKind:       %s\nSource:     %s\nTargets:    %s\nRollout:    %s\nPaused:     %v\nReady:      %s\n",
				it.Name, it.Namespace, it.Kind, it.Source, dash(delivery.DescribeTargets(it.Targets)), it.Rollout, it.Paused, dash(it.Ready))
			if it.State != "" {
				fmt.Printf("State:      %s\n", it.State)
			}
			for _, t := range it.Targets {
				if t.Kind == delivery.Custom {
					fmt.Printf("  (a custom target: %v)\n", t.Raw)
				}
			}
			p, err := s.progress(ctx, it.Kind, it.Namespace, it.Name)
			if err != nil {
				return err
			}
			fmt.Println()
			if len(p.Clusters) == 0 {
				fmt.Println("No cluster has it yet.")
				return nil
			}
			if !p.Synced {
				fmt.Println("Fleet hasn't taken in its latest change yet.")
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "CLUSTER\tSTATE\tMESSAGE")
			for _, cs := range p.Clusters {
				fmt.Fprintf(w, "%s\t%s\t%s\n", cs.Cluster, cs.State, dash(oneLine(cs.Message)))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "the Deployment's namespace (needed when two share a name)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "print the Fleet object: yaml or json")
	return cmd
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}

// deployFlags are create's and update's.
type deployFlags struct {
	entryFlags
	ns, rollout                   string
	to, addTo, removeTo           []string
	paused                        bool
	username, tokenEnv, tokenFile string
	dryRun                        bool
	waitFlags
}

func (f *deployFlags) add(cmd *cobra.Command, update bool) {
	f.entryFlags.add(cmd, false)
	fs := cmd.Flags()
	_ = fs.MarkHidden("name")
	fs.StringVarP(&f.ns, "namespace", "n", "", "where the Deployment lives (default: picked from its targets)")
	if update {
		fs.StringArrayVar(&f.to, "to", nil, "replace the targets (repeatable)")
		fs.StringArrayVar(&f.addTo, "add-to", nil, "add a target (repeatable)")
		fs.StringArrayVar(&f.removeTo, "remove-to", nil, "remove a target (repeatable)")
	} else {
		fs.StringArrayVar(&f.to, "to", nil, "target: template/NAME, class/NAME, env/NAME, cluster/ENV/NAME, group/NAME, all or host (repeatable)")
	}
	fs.StringVar(&f.rollout, "rollout", "", "all (at once), one-at-a-time, or canary (clusters labelled hyve.io/canary=true first); not for --git, whose fleet.yaml says")
	fs.BoolVar(&f.paused, "paused", false, "create it paused: nothing changes on clusters until 'hyve deploy resume'")
	fs.StringVar(&f.username, "username", "", "git username, for a private --git repo")
	fs.StringVar(&f.tokenEnv, "token-env", "", "environment variable holding the git token")
	fs.StringVar(&f.tokenFile, "token-file", "", "file holding the git token")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the Fleet object instead of saving it")
	f.waitFlags.add(cmd)
}

func (f *deployFlags) hasSource() bool {
	return f.helm != "" || f.git != "" || f.path != ""
}

// patchSource changes only the source fields whose flags are given
// (--version, --set, --revision...); the rest of the source stays.
func (f *deployFlags) patchSource(cmd *cobra.Command, src *resources.Entry) error {
	changed := cmd.Flags().Changed
	kind := src.Kind()
	for want, flags := range map[string][]string{
		"HelmOp":  {"version", "repo", "release-name", "values", "set"},
		"GitRepo": {"branch", "revision", "git-path"},
	} {
		for _, n := range flags {
			if changed(n) && kind != want {
				return fmt.Errorf("--%s is for a %s source; %s is a %s (give --helm, --git or --path to replace its source)",
					n, map[string]string{"HelmOp": "Helm chart", "GitRepo": "git"}[want], src.Name, kind)
			}
		}
	}
	spec := map[string]map[string]any{"HelmOp": src.HelmOp, "GitRepo": src.GitRepo, "Bundle": src.Bundle}[kind]
	if spec == nil {
		return fmt.Errorf("%s has no source hyve can change", src.Name)
	}
	set := func(m map[string]any, k, v string) {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	switch kind {
	case "HelmOp":
		h, _ := spec["helm"].(map[string]any)
		if h == nil {
			h = map[string]any{}
			spec["helm"] = h
		}
		if changed("version") {
			set(h, "version", f.version)
		}
		if changed("repo") {
			set(h, "repo", f.repo)
		}
		if changed("release-name") {
			set(h, "releaseName", f.release)
		}
		if changed("values") || changed("set") {
			vals, err := f.values()
			if err != nil {
				return err
			}
			old, _ := h["values"].(map[string]any)
			h["values"] = mergeValues(old, vals)
		}
	case "GitRepo":
		if changed("branch") {
			set(spec, "branch", f.branch)
			delete(spec, "revision")
		}
		if changed("revision") {
			set(spec, "revision", f.revision)
		}
		if changed("git-path") {
			ps := make([]any, len(f.gitPaths))
			for i, p := range f.gitPaths {
				ps[i] = p
			}
			spec["paths"] = ps
		}
	}
	if changed("target-namespace") {
		key := "defaultNamespace"
		if kind == "GitRepo" {
			key = "targetNamespace"
		}
		set(spec, key, f.namespace)
	}
	if changed("correct-drift") {
		spec["correctDrift"] = map[string]any{"enabled": f.correctDrift}
	}
	src.Secrets = f.secrets
	return nil
}

// mergeValues merges Helm values over old ones: maps merge key by key,
// anything else replaces.
func mergeValues(old, vals map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range old {
		out[k] = v
	}
	for k, v := range vals {
		if vm, ok := v.(map[string]any); ok {
			if om, ok := out[k].(map[string]any); ok {
				out[k] = mergeValues(om, vm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func parseTargets(ss []string) ([]delivery.Target, error) {
	var out []delivery.Target
	for _, s := range ss {
		t, err := delivery.ParseTarget(s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// gitAuth keeps a private repo's credentials in a basic-auth Secret beside
// the GitRepo and points the GitRepo at it.
func (f *deployFlags) gitAuth(ctx context.Context, s *session, name, ns string, src *resources.Entry) error {
	token := ""
	switch {
	case f.tokenEnv != "" && f.tokenFile != "":
		return errors.New("give --token-env or --token-file, not both")
	case f.tokenEnv != "":
		if token = os.Getenv(f.tokenEnv); token == "" {
			return fmt.Errorf("$%s is empty", f.tokenEnv)
		}
	case f.tokenFile != "":
		raw, err := os.ReadFile(f.tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" {
		return nil
	}
	if src.GitRepo == nil {
		return errors.New("--token-env and --token-file are for a --git repo")
	}
	if f.username == "" {
		return errors.New("--username is required with a token")
	}
	secret := name + "-git-auth"
	if err := s.kube.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/basic-auth",
		"metadata":   map[string]any{"name": secret, "namespace": ns},
		"stringData": map[string]any{"username": f.username, "password": token},
	}}); err != nil {
		return err
	}
	src.GitRepo["clientSecretName"] = secret
	return nil
}

func deployCreateCmd() *cobra.Command {
	var f deployFlags
	cmd := &cobra.Command{
		Use:   "create NAME (--helm CHART --repo URL | --git URL | --path DIR) --to TARGET...",
		Short: "Deliver a Helm chart, git paths or manifests to clusters",
		Example: `  hyve deploy create podinfo --helm podinfo --repo https://stefanprodan.github.io/podinfo --version 6.7.1 \
      -N podinfo --to template/civo-podinfo
  hyve deploy create wg --git https://github.com/org/config.git --git-path workloads/civo-wg --to template/civo-wg
  hyve deploy create monitoring --helm kube-prometheus-stack --repo https://prometheus-community.github.io/helm-charts \
      --to all --rollout canary
  hyve deploy create debug --path ./debug --to cluster/dev/web
  GIT_TOKEN=... hyve deploy create nexus-config --git https://github.com/org/nexus-config.git --git-path fleet \
      --username me --token-env GIT_TOKEN --to host`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.name = args[0]
			if len(f.to) == 0 {
				return errors.New("give at least one --to")
			}
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			src, err := f.entry()
			if err != nil {
				return err
			}
			ts, err := parseTargets(f.to)
			if err != nil {
				return err
			}
			d := delivery.Deployment{Name: args[0], Namespace: f.ns, Source: src, Targets: ts, Rollout: f.rollout, Paused: f.paused}
			if d.Namespace == "" {
				if d.Namespace, err = delivery.Home(ts, s.templateNS()); err != nil {
					return err
				}
			}
			if !f.dryRun {
				if err := f.gitAuth(ctx, s, d.Name, d.Namespace, &d.Source); err != nil {
					return err
				}
			}
			u, err := delivery.Object(d, s.templateNS(), nil)
			if err != nil {
				return err
			}
			if f.dryRun {
				return printObjects("yaml", u.Object)
			}
			if _, err := s.kube.Dynamic.Resource(resources.FleetGVRs[u.GetKind()]).Namespace(u.GetNamespace()).Create(ctx, u, metav1.CreateOptions{}); err != nil {
				if apierrors.IsAlreadyExists(err) {
					return fmt.Errorf("%s %s/%s exists: 'hyve deploy update' changes it ('hyve deploy apply' creates or updates from YAML)", u.GetKind(), u.GetNamespace(), u.GetName())
				}
				return notInstalled(err)
			}
			logf("Deployment %s/%s (%s) → %s", u.GetNamespace(), u.GetName(), u.GetKind(), delivery.DescribeTargets(ts))
			return f.then(ctx, s, u)
		},
	}
	f.add(cmd, false)
	return cmd
}

func deployUpdateCmd() *cobra.Command {
	var f deployFlags
	cmd := &cobra.Command{
		Use:   "update NAME [source flags] [--to ... | --add-to ... | --remove-to ...] [--rollout ...]",
		Short: "Change a Deployment's source, targets or rollout; Fleet updates every cluster it reaches",
		Long: `Changes a Deployment; only what's given changes.

--helm, --git or --path replace the whole source (give it all: --helm with
--repo, --version and values). Without them, the other source flags change
just their own field and the rest stays, which is how a release goes out:
  --version, --repo, --release-name   a Helm chart's
  --set, -f                           merged over its Helm values
  --branch, --revision, --git-path    a git source's (--branch clears a pinned revision)
  -N, --secret, --correct-drift       any source's

Targets and the rollout are changed only when given. What hyve doesn't
model (targetCustomizations, diff, ...) is kept. --wait waits until every
cluster runs the change and fails if one can't, for CI.`,
		Example: `  hyve deploy update podinfo --version 6.8.0 --wait
  hyve deploy update web --set image.tag="$GIT_SHA" --rollout canary --wait --timeout 20m
  hyve deploy update config --revision v1.4.3
  hyve deploy update podinfo --helm podinfo --repo https://stefanprodan.github.io/podinfo --version 6.8.0 --rollout one-at-a-time
  hyve deploy update podinfo --add-to class/civo`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			it, err := s.findDeployment(ctx, args[0], f.ns)
			if err != nil {
				return err
			}
			src := delivery.SourceOf(it.Object)
			if f.hasSource() {
				f.name = it.Name
				if src, err = f.entry(); err != nil {
					return err
				}
				// Keep the git credentials unless new ones are given.
				if old := delivery.SourceOf(it.Object).GitRepo; old != nil && src.GitRepo != nil && old["clientSecretName"] != nil {
					src.GitRepo["clientSecretName"] = old["clientSecretName"]
				}
			} else if err := f.patchSource(cmd, &src); err != nil {
				return err
			}
			ts := it.Targets
			if cmd.Flags().Changed("to") {
				if ts, err = parseTargets(f.to); err != nil {
					return err
				}
			}
			add, err := parseTargets(f.addTo)
			if err != nil {
				return err
			}
			ts = append(ts, add...)
			for _, r := range f.removeTo {
				kept := ts[:0]
				for _, t := range ts {
					if t.String() != r {
						kept = append(kept, t)
					}
				}
				if len(kept) == len(ts) {
					return fmt.Errorf("%s isn't one of its targets (%s)", r, delivery.DescribeTargets(ts))
				}
				ts = kept
			}
			if len(ts) == 0 {
				return errors.New("a Deployment needs a target; 'hyve deploy delete' removes it")
			}
			rollout := it.Rollout
			if f.rollout != "" {
				rollout = f.rollout
			}
			paused := it.Paused
			if cmd.Flags().Changed("paused") {
				paused = f.paused
			}
			if err := f.gitAuth(ctx, s, it.Name, it.Namespace, &src); err != nil {
				return err
			}
			d := delivery.Deployment{Name: it.Name, Namespace: it.Namespace, Source: src, Targets: ts, Rollout: rollout, Paused: paused}
			u, err := delivery.Object(d, s.templateNS(), it.Object)
			if err != nil {
				return err
			}
			if f.dryRun {
				return printObjects("yaml", u.Object)
			}
			u.SetResourceVersion(it.Object.GetResourceVersion())
			if _, err := s.kube.Dynamic.Resource(resources.FleetGVRs[u.GetKind()]).Namespace(u.GetNamespace()).Update(ctx, u, metav1.UpdateOptions{}); err != nil {
				return err
			}
			logf("Updated Deployment %s/%s → %s (rollout %s)", u.GetNamespace(), u.GetName(), delivery.DescribeTargets(ts), rollout)
			return f.then(ctx, s, u)
		},
	}
	f.add(cmd, true)
	return cmd
}

// patchDeployment merge-patches a Deployment's spec.
func patchDeployment(cmd *cobra.Command, name, ns string, spec map[string]any, done string, w *waitFlags) error {
	s, err := newSession()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	it, err := s.findDeployment(ctx, name, ns)
	if err != nil {
		return err
	}
	if spec == nil {
		gen, _, _ := unstructured.NestedInt64(it.Object.Object, "spec", "forceSyncGeneration")
		spec = map[string]any{"forceSyncGeneration": gen + 1}
	}
	o := it.Object.DeepCopy()
	for k, v := range spec {
		_ = unstructured.SetNestedField(o.Object, v, "spec", k)
	}
	if _, err := s.kube.Dynamic.Resource(resources.FleetGVRs[o.GetKind()]).Namespace(o.GetNamespace()).Update(ctx, o, metav1.UpdateOptions{}); err != nil {
		return err
	}
	logf("%s %s/%s", done, o.GetNamespace(), o.GetName())
	if w == nil {
		return nil
	}
	return w.then(ctx, s, o)
}

func deployPauseCmd(pause bool) *cobra.Command {
	var ns string
	var w waitFlags
	use, short, done := "pause NAME", "Stop changes reaching clusters (what's deployed stays)", "Paused"
	if !pause {
		use, short, done = "resume NAME", "Let changes reach clusters again", "Resumed"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pause {
				return patchDeployment(cmd, args[0], ns, map[string]any{"paused": true}, done, nil)
			}
			return patchDeployment(cmd, args[0], ns, map[string]any{"paused": false}, done, &w)
		},
	}
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "the Deployment's namespace")
	if !pause {
		w.add(cmd)
	}
	return cmd
}

func deployForceCmd() *cobra.Command {
	var ns string
	var w waitFlags
	cmd := &cobra.Command{
		Use:   "force NAME",
		Short: "Deploy again to every cluster, even where nothing changed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchDeployment(cmd, args[0], ns, nil, "Forced an update of", &w)
		},
	}
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "the Deployment's namespace")
	w.add(cmd)
	return cmd
}

func deployDeleteCmd() *cobra.Command {
	var ns string
	cmd := &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a Deployment; Fleet removes what it deployed from every cluster",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			it, err := s.findDeployment(ctx, args[0], ns)
			if err != nil {
				return err
			}
			if err := s.kube.Dynamic.Resource(resources.FleetGVRs[it.Kind]).Namespace(it.Namespace).Delete(ctx, it.Name, metav1.DeleteOptions{}); err != nil {
				return err
			}
			logf("Deleted Deployment %s/%s", it.Namespace, it.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "the Deployment's namespace")
	return cmd
}

// reaches says why a target reaches a CAPI cluster ("" when it doesn't).
func reaches(t delivery.Target, c *unstructured.Unstructured, groups map[string]map[string]string) string {
	l := c.GetLabels()
	class, _, _ := unstructured.NestedString(c.Object, "spec", "topology", "classRef", "name")
	switch t.Kind {
	case delivery.Template:
		if l[templates.LabelTemplate] == t.Name {
			return "template " + t.Name
		}
	case delivery.Class:
		if class == t.Name {
			return "class " + t.Name
		}
	case delivery.Env:
		if c.GetNamespace() == t.Name {
			return "environment " + t.Name
		}
	case delivery.Cluster:
		if c.GetNamespace() == t.Env && c.GetName() == t.Name {
			return "this cluster"
		}
	case delivery.All:
		return "all clusters"
	case delivery.Group:
		sel, ok := groups[t.Name]
		if !ok {
			return ""
		}
		for k, v := range sel {
			if l[k] != v {
				return ""
			}
		}
		return "group " + t.Name
	case delivery.Custom:
		return "a custom target"
	}
	return ""
}

func clusterDeploymentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deployments CLUSTER",
		Short: "What's delivered to a cluster, why, and its state there",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			fleetNS, fleetName := delivery.HostWorkspace, delivery.HostCluster
			var c *unstructured.Unstructured
			if !isLocal(args[0]) {
				if c, err = s.getCluster(ctx, args[0]); err != nil {
					return err
				}
				fleetNS, fleetName = c.GetNamespace(), c.GetName()
			}
			nss, err := s.deployNamespaces(ctx)
			if err != nil {
				return err
			}
			items, err := delivery.List(ctx, s.kube.Dynamic, s.templateNS(), nss)
			if err != nil {
				return err
			}
			groups := map[string]map[string]string{}
			if gl, err := s.kube.Dynamic.Resource(delivery.ClusterGroupGVR).Namespace(s.templateNS()).List(ctx, metav1.ListOptions{}); err == nil {
				for _, g := range gl.Items {
					ml, _, _ := unstructured.NestedStringMap(g.Object, "spec", "selector", "matchLabels")
					groups[g.GetName()] = ml
				}
			}
			bds, err := s.kube.Dynamic.Resource(resources.BundleDeploymentGVR).List(ctx, metav1.ListOptions{
				LabelSelector: "fleet.cattle.io/cluster-namespace=" + fleetNS + ",fleet.cattle.io/cluster=" + fleetName,
			})
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAMESPACE\tNAME\tSOURCE\tWHY\tSTATE\tMESSAGE")
			for i := range items {
				it := &items[i]
				var why []string
				for _, t := range it.Targets {
					switch {
					case c == nil && t.Kind == delivery.Host:
						why = append(why, "the host")
					case c != nil && it.Namespace != delivery.HostWorkspace:
						if r := reaches(t, c, groups); r != "" {
							why = append(why, r)
						}
					}
				}
				state, msg := "", ""
				for j := range bds.Items {
					bd := &bds.Items[j]
					l := bd.GetLabels()
					if l["fleet.cattle.io/bundle-namespace"] != it.Namespace {
						continue
					}
					if (it.Kind == "GitRepo" && l["fleet.cattle.io/repo-name"] == it.Name) || (it.Kind != "GitRepo" && l["fleet.cattle.io/bundle-name"] == it.Name) {
						st, m := delivery.BDState(bd)
						if state == "" || state == "Ready" {
							state, msg = st, m
						}
					}
				}
				if len(why) == 0 && state == "" {
					continue
				}
				if state == "" {
					state = "NotYet"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", it.Namespace, it.Name, it.Source, dash(strings.Join(why, ", ")), state, dash(oneLine(msg)))
			}
			return w.Flush()
		},
	}
}
