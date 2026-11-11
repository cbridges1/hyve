package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/host"
	logincmd "github.com/cbridges1/hyve/cmd/login"
	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/cli"
)

var rootCmd = &cobra.Command{
	Use:   "hyve",
	Short: "Clusters from Cluster API templates, delivered to with Fleet",
	Long: `hyve makes Kubernetes clusters from ClusterTemplates (a Cluster API
ClusterClass with defaults, locked variables and expiry) and puts things
on them with Fleet: Deployments aimed at templates, environments and
clusters. Its host endpoint is the server users sign
in to and child clusters' agents connect to.

Sign in with 'hyve login <url>', then:

  hyve template list                     templates in the template namespace
  hyve cluster create demo -t capd-small a cluster in the current environment
  hyve cluster kubeconfig demo           kubectl access, as you`,
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	SilenceUsage:      true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&shared.HomeFlagValue, "home", "", "hyve home directory (default: ~/.hyve). Also read from HYVE_HOME.")
	cli.Home, cli.ExplicitHome = shared.HyveHome, shared.ExplicitHome

	// Help lists the commands in these groups.
	rootCmd.AddGroup(
		&cobra.Group{ID: "clusters", Title: "Clusters:"},
		&cobra.Group{ID: "delivery", Title: "Delivering to clusters:"},
		&cobra.Group{ID: "access", Title: "Signing in and access:"},
		&cobra.Group{ID: "setup", Title: "Setting up hyve:"},
		&cobra.Group{ID: "server", Title: "Run by the chart:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			rootCmd.AddCommand(c)
		}
	}
	add("clusters", cli.ClusterCmd(), cli.TemplateCmd(), cli.EnvCmd(), cli.ProviderCmd())
	add("delivery", cli.DeployCmd(), cli.ApplyCmd())
	// Signing in (HYVE-PROPOSAL.md, "Signing in"): one URL, the hyve host.
	add("access", logincmd.LoginCmd, logincmd.LogoutCmd, cli.ContextCmd(), logincmd.UserCmd, cli.AccessCmd(), cli.UICmd())
	add("setup", cli.InstallCmd(), cli.LocalCmd())
	add("server", cli.ControllerCmd(), host.Cmd)

	// kubectl runs 'hyve token' (the kubeconfigs hyve hands out call it),
	// so it stays, out of the help.
	logincmd.TokenCmd.Hidden = true
	rootCmd.AddCommand(logincmd.TokenCmd)

	// Where commands used to be: they still work, hidden, saying where
	// they are now.
	rootCmd.AddCommand(
		cli.MovedTo(logincmd.KubeconfigCmd, "kubeconfig", "cluster kubeconfig"),
		cli.MovedTo(cli.RenderCmd(), "render", "template render"),
		cli.MovedTo(cli.ConvertCmd(), "import", "template convert"),
		cli.MovedTo(cli.SecretsCmd(), "secrets", "deploy secret"),
		cli.RepoCmd(), cli.ResourceCmd(),
	)

	// Local mode saves its state after each change.
	rootCmd.PersistentPostRun = func(cmd *cobra.Command, _ []string) { cli.SaveLocalStateAfter(cmd) }
}
