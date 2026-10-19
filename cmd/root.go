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
ClusterClass with defaults, locked variables, expiry and resources) and
puts things on them with Fleet. It runs with Rancher, or acts as the
Rancher host itself when Rancher isn't installed.

Sign in with 'hyve login <url>' (a hyve host or Rancher), then:

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

	// Clusters and what goes on them.
	rootCmd.AddCommand(cli.ClusterCmd(), cli.TemplateCmd(), cli.ResourceCmd(), cli.EnvCmd(), cli.AccessCmd(),
		cli.ProviderCmd(), cli.RepoCmd(), cli.SecretsCmd(), cli.RenderCmd(), cli.ImportCmd(), cli.InstallCmd(), cli.LocalCmd(), cli.UICmd())

	// Signing in (HYVE-PROPOSAL.md, "Signing in"): one URL, a hyve host or
	// Rancher.
	rootCmd.AddCommand(logincmd.LoginCmd, logincmd.LogoutCmd, cli.ContextCmd(),
		logincmd.TokenCmd, logincmd.KubeconfigCmd, logincmd.UserCmd)

	// What the chart runs.
	rootCmd.AddCommand(cli.ControllerCmd(), host.Cmd)

	// Local mode saves its state after each change.
	rootCmd.PersistentPostRun = func(cmd *cobra.Command, _ []string) { cli.SaveLocalStateAfter(cmd) }
}
