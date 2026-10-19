package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/login"
)

var namespaceGVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}

// LabelEnvironment marks a namespace as a hyve environment.
const LabelEnvironment = "hyve.io/environment"

// EnvCmd is `hyve env`: environments are namespaces (HYVE-PROPOSAL.md,
// "Environments: namespaces, not organizations").
func EnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "env",
		Aliases: []string{"environment", "environments"},
		Short:   "Environments: namespaces that hold clusters and their extras",
	}
	AddGlobalFlags(cmd)
	cmd.AddCommand(envListCmd(), envCreateCmd(), envDeleteCmd(), envUseCmd())
	return cmd
}

func envListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List environments and how many clusters each holds",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			nss, err := s.kube.Dynamic.Resource(namespaceGVR).List(ctx, metav1.ListOptions{LabelSelector: LabelEnvironment})
			if err != nil {
				return err
			}
			count := map[string]int{}
			if cl, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).List(ctx, metav1.ListOptions{}); err == nil {
				for _, c := range cl.Items {
					count[c.GetNamespace()]++
				}
			}
			names := map[string]bool{}
			for _, ns := range nss.Items {
				names[ns.GetName()] = true
			}
			// Namespaces holding clusters are environments too, labelled or not.
			for ns := range count {
				names[ns] = true
			}
			sorted := make([]string, 0, len(names))
			for n := range names {
				sorted = append(sorted, n)
			}
			sort.Strings(sorted)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ENVIRONMENT\tCLUSTERS\tCURRENT")
			for _, n := range sorted {
				cur := ""
				if n == s.env() {
					cur = "*"
				}
				fmt.Fprintf(w, "%s\t%d\t%s\n", n, count[n], cur)
			}
			return w.Flush()
		},
	}
}

func envCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create NAME",
		Short: "Create an environment (a namespace labelled hyve.io/environment)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession()
			if err != nil {
				return err
			}
			ns := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Namespace",
				"metadata": map[string]any{"name": args[0], "labels": map[string]any{LabelEnvironment: "true", clusters.LabelManagedBy: clusters.ManagedBy}},
			}}
			if err := s.kube.ApplyObject(cmd.Context(), ns); err != nil {
				return err
			}
			logf("Environment %s is ready; 'hyve env use %s' makes it the default", args[0], args[0])
			return nil
		},
	}
}

func envDeleteCmd() *cobra.Command {
	var force bool
	return func() *cobra.Command {
		cmd := &cobra.Command{
			Use:   "delete NAME",
			Short: "Delete an empty environment (its namespace)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := newSession()
				if err != nil {
					return err
				}
				ctx := cmd.Context()
				cl, err := s.kube.Dynamic.Resource(clusters.ClusterGVR).Namespace(args[0]).List(ctx, metav1.ListOptions{})
				if err == nil && len(cl.Items) > 0 && !force {
					return fmt.Errorf("environment %s still holds %d clusters; delete them first (or --force deletes them with it)", args[0], len(cl.Items))
				}
				if err := s.kube.Dynamic.Resource(namespaceGVR).Delete(ctx, args[0], metav1.DeleteOptions{}); err != nil {
					return err
				}
				logf("Deleting environment %s", args[0])
				return nil
			},
		}
		cmd.Flags().BoolVar(&force, "force", false, "delete it with the clusters in it")
		return cmd
	}()
}

func envUseCmd() *cobra.Command {
	var tplNS string
	cmd := &cobra.Command{
		Use:   "use NAME",
		Short: "Make an environment the default for the current server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kubeconfigFlag != "" {
				return errors.New("env use saves to a signed-in server; with --kubeconfig pass --env each time")
			}
			cfg, err := login.Load(Home())
			if err != nil {
				return err
			}
			s, err := cfg.Get(serverFlag)
			if err != nil {
				return err
			}
			s.Environment = args[0]
			if tplNS != "" {
				s.TemplateNamespace = tplNS
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			logf("Environment %s is now the default on %s", args[0], s.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&tplNS, "template-namespace", "", "also set the template namespace for this server")
	return cmd
}

// ContextCmd is `hyve context`: the servers `hyve login` saved.
func ContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"contexts", "ctx"},
		Short:   "List, switch and remove the servers you've signed in to",
	}
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List saved servers",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := login.Load(Home())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "CURRENT\tNAME\tKIND\tURL\tUSER\tENVIRONMENT\tSESSION")
			for _, s := range cfg.Servers {
				cur := ""
				if s.Name == cfg.Current {
					cur = "*"
				}
				sess := "signed out"
				if s.SignedIn(time.Now()) {
					sess = "signed in"
					if !s.ExpiresAt.IsZero() {
						sess += " until " + s.ExpiresAt.Local().Format("Jan 2 15:04")
					}
				} else if s.Token != "" {
					sess = "expired"
				}
				env := s.Environment
				if env == "" {
					env = DefaultEnvironment
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", cur, s.Name, s.Kind, s.URL, dash(s.Username), env, sess)
			}
			return w.Flush()
		},
	}
	use := &cobra.Command{
		Use:   "use NAME",
		Short: "Make a saved server the current one",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := login.Load(Home())
			if err != nil {
				return err
			}
			if _, err := cfg.Get(args[0]); err != nil {
				return err
			}
			cfg.Current = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			logf("Now using %s", args[0])
			return nil
		},
	}
	current := &cobra.Command{
		Use:   "current",
		Short: "Print the current server's name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := login.Load(Home())
			if err != nil {
				return err
			}
			s, err := cfg.Get("")
			if err != nil {
				return err
			}
			fmt.Println(s.Name)
			return nil
		},
	}
	remove := &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm"},
		Short:   "Forget a saved server (run 'hyve logout' first to end its session)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := login.Load(Home())
			if err != nil {
				return err
			}
			if !cfg.Remove(args[0]) {
				return fmt.Errorf("no saved server %q", args[0])
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			logf("Removed %s", args[0])
			return nil
		},
	}
	cmd.AddCommand(list, use, current, remove)
	return cmd
}
