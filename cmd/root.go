package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/cluster"
	"github.com/cbridges1/hyve/cmd/clusterconfig"
	"github.com/cbridges1/hyve/cmd/env"
	modcmd "github.com/cbridges1/hyve/cmd/module"
	"github.com/cbridges1/hyve/cmd/organization"
	"github.com/cbridges1/hyve/cmd/reconcilingcluster"
	rescmd "github.com/cbridges1/hyve/cmd/resource"
	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/cmd/template"
	"github.com/cbridges1/hyve/cmd/workflow"
	"github.com/cbridges1/hyve/internal/database"
)

var rootCmd = &cobra.Command{
	Use:   "hyve",
	Short: "Hyve — Kubernetes cluster lifecycle management",
	Long: `hyve manages the full lifecycle of Kubernetes clusters — creation,
configuration, reconciliation, and teardown — across any cloud provider.
Run it as a GitOps CLI against a local/git-backed directory, or deploy it
as a cluster-native controller + API (see 'hyve cluster-config') for
team/multi-tenant use. Both modes share the same YAML and the same
reconcile engine.`,
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		home := shared.HyveHome()
		if home != "" {
			database.SetConfigDir(home)
		}
		shared.LoadEnvironmentSecrets() // higher precedence — see 'hyve env secrets'
		shared.LoadLegacyRepoEnvFile()  // lower precedence, relocated from main.go
		return nil
	},
}

// HyveHome returns the effective Hyve home directory — see
// shared.HyveHome's doc comment for the resolution order. Kept as a thin
// re-export so existing callers in this package don't need to change.
func HyveHome() string {
	return shared.HyveHome()
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&shared.HomeFlagValue, "home", "", "Hyve home directory (default: ~/.hyve). Also read from HYVE_HOME env var.")

	// Verbs that do something, first (apply/migrate self-register via
	// their own file's init()). login/logout/whoami moved under 'hyve
	// env' (cmd/env/login.go, cmd/env/whoami.go) — identity is still a
	// separate, global session independent of which environment is
	// current (see cmd/env/login.go's own doc comment), just reachable
	// from the same command group as environment selection now.
	rootCmd.AddCommand(reconcileCmd)

	// Identity/environment selection — almost everything below depends on
	// which environment is active.
	rootCmd.AddCommand(env.Cmd)

	// Resource-type command groups, in dependency order: a cluster is the
	// thing you ultimately want, templates/workflows support it, modules
	// are the driver layer underneath.
	rootCmd.AddCommand(cluster.Cmd)
	rootCmd.AddCommand(template.Cmd)
	rootCmd.AddCommand(workflow.Cmd)
	rootCmd.AddCommand(rescmd.Cmd)
	rootCmd.AddCommand(modcmd.Cmd)

	// Multi-tenant control-plane management (Milestone 2/6,
	// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs) —
	// cluster-mode only, unlike everything above.
	rootCmd.AddCommand(organization.Cmd)
	rootCmd.AddCommand(reconcilingcluster.Cmd)

	// 'controller'/'api' run are ops-only (Helm's own Deployment args call
	// them directly — `hyve cluster-config controller run` / `... api
	// run`), but 'api' also nests the local-user management commands
	// (create-user, ...) a real operator runs interactively when
	// bootstrapping a cluster-mode install, so the whole tree is visible
	// in `hyve --help` rather than Cmd.Hidden — see cmd/clusterconfig's
	// own doc comment.
	rootCmd.AddCommand(clusterconfig.Cmd)
}
