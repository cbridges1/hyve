package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
)

// RepoCmd is `hyve repo`: Fleet GitRepos you add yourself. They're plain
// Fleet; hyve lists them but doesn't own what they deliver.
func RepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repo",
		Aliases: []string{"repos"},
		Short:   "Point Fleet at git repositories (plain Fleet GitRepos)",
	}
	AddGlobalFlags(cmd)
	cmd.AddCommand(repoAddCmd(), repoListCmd(), repoRemoveCmd())
	return cmd
}

func repoAddCmd() *cobra.Command {
	var (
		repo, branch, workspace, username, tokenEnv, tokenFile string
		paths                                                  []string
		correctDrift                                           bool
	)
	cmd := &cobra.Command{
		Use:   "add NAME --repo URL",
		Short: "Create or update a Fleet GitRepo (with credentials for a private repo)",
		Long: `Creates a Fleet GitRepo in a workspace. In fleet-local (the default) it
deploys to the management cluster itself. Fleet makes one bundle per
directory with a fleet.yaml under the given paths.

For a private repo give a username and a token, read from an environment
variable or a file; hyve keeps them in a basic-auth Secret the GitRepo uses.`,
		Example: `  GIT_TOKEN=... hyve repo add nexus-config --repo https://github.com/org/nexus-config.git \
      --path fleet --username me --token-env GIT_TOKEN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			name := args[0]
			token := ""
			switch {
			case tokenEnv != "" && tokenFile != "":
				return errors.New("give --token-env or --token-file, not both")
			case tokenEnv != "":
				if token = os.Getenv(tokenEnv); token == "" {
					return fmt.Errorf("$%s is empty", tokenEnv)
				}
			case tokenFile != "":
				raw, err := os.ReadFile(tokenFile)
				if err != nil {
					return err
				}
				token = strings.TrimSpace(string(raw))
			}
			spec := map[string]any{"repo": repo, "branch": branch}
			if len(paths) > 0 {
				ps := make([]any, len(paths))
				for i, p := range paths {
					ps[i] = p
				}
				spec["paths"] = ps
			}
			if correctDrift {
				spec["correctDrift"] = map[string]any{"enabled": true}
			}
			if token != "" {
				if username == "" {
					return errors.New("--username is required with a token")
				}
				secretName := name + "-git-auth"
				if err := s.kube.ApplyObject(ctx, &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/basic-auth",
					"metadata":   map[string]any{"name": secretName, "namespace": workspace},
					"stringData": map[string]any{"username": username, "password": token},
				}}); err != nil {
					return err
				}
				spec["clientSecretName"] = secretName
			}
			gr := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "fleet.cattle.io/v1alpha1", "kind": "GitRepo",
				"metadata": map[string]any{"name": name, "namespace": workspace,
					"labels": map[string]any{clusters.LabelManagedBy: clusters.ManagedBy}},
				"spec": spec,
			}}
			if err := s.kube.ApplyObject(ctx, gr); err != nil {
				return err
			}
			logf("GitRepo %s/%s → %s@%s %v", workspace, name, repo, branch, paths)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "git repository URL (required)")
	f.StringVar(&branch, "branch", "main", "branch")
	f.StringArrayVar(&paths, "path", nil, "path in the repo (repeatable; default: the whole repo)")
	f.StringVar(&workspace, "workspace", "fleet-local", "Fleet workspace (fleet-local is the management cluster; an environment namespace targets its clusters)")
	f.StringVar(&username, "username", "", "git username for a private repo")
	f.StringVar(&tokenEnv, "token-env", "", "environment variable holding the git token")
	f.StringVar(&tokenFile, "token-file", "", "file holding the git token")
	f.BoolVar(&correctDrift, "correct-drift", false, "have Fleet undo changes made to what it deployed")
	_ = cmd.MarkFlagRequired("repo")
	return cmd
}

func repoListCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Fleet GitRepos and their bundles' state",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			list, err := s.kube.Dynamic.Resource(resources.GitRepoGVR).Namespace(workspace).List(cmd.Context(), metav1.ListOptions{})
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "WORKSPACE\tNAME\tREPO\tBRANCH\tCOMMIT\tREADY\tSTATE")
			for i := range list.Items {
				g := &list.Items[i]
				if g.GetLabels()["hyve.io/owner-kind"] != "" {
					continue // a template's gitRepo resource; see 'hyve resource list'
				}
				get := func(f ...string) string { v, _, _ := unstructured.NestedString(g.Object, f...); return v }
				commit := get("status", "commit")
				if len(commit) > 8 {
					commit = commit[:8]
				}
				it := resources.Item{Object: g}
				ready, state := it.Ready()
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", g.GetNamespace(), g.GetName(), get("spec", "repo"), dash(get("spec", "branch")), dash(commit), dash(ready), dash(state))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "only this Fleet workspace (default: all)")
	return cmd
}

func repoRemoveCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a Fleet GitRepo; Fleet removes what it deployed",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			if err := s.kube.Dynamic.Resource(resources.GitRepoGVR).Namespace(workspace).Delete(cmd.Context(), args[0], metav1.DeleteOptions{}); err != nil {
				return err
			}
			logf("Removed GitRepo %s/%s", workspace, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "fleet-local", "Fleet workspace")
	return cmd
}
