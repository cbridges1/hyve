package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/cluster"
	"github.com/cbridges1/hyve/cmd/clusterconfig"
	"github.com/cbridges1/hyve/cmd/env"
	modcmd "github.com/cbridges1/hyve/cmd/module"
	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/cmd/template"
	"github.com/cbridges1/hyve/cmd/workflow"
	"github.com/cbridges1/hyve/internal/database"
)

var rootCmd = &cobra.Command{
	Use:   "hyve",
	Short: "Hyve cluster management CLI",
	Long: `A CLI tool for managing Kubernetes clusters on various cloud providers.
Supports cluster creation, modification, deletion, and reconciliation.`,
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		home := shared.HyveHome()
		if home != "" {
			database.SetConfigDir(home)
		}
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

	// Verbs that do something, first (login/logout/whoami/apply/migrate
	// self-register via their own file's init()).
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
	rootCmd.AddCommand(modcmd.Cmd)

	// Ops-only — runs inside Helm-deployed pods, not an interactive
	// end-user surface.
	rootCmd.AddCommand(clusterconfig.Cmd)
}
