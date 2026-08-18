package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/cluster"
	"github.com/cbridges1/hyve/cmd/clusterconfig"
	modcmd "github.com/cbridges1/hyve/cmd/module"
	"github.com/cbridges1/hyve/cmd/shared"
	statecmd "github.com/cbridges1/hyve/cmd/state"
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

	rootCmd.AddCommand(reconcileCmd)
	rootCmd.AddCommand(cluster.Cmd)
	rootCmd.AddCommand(workflow.Cmd)
	rootCmd.AddCommand(template.Cmd)
	rootCmd.AddCommand(modcmd.Cmd)
	rootCmd.AddCommand(statecmd.SetStateCmd)
	rootCmd.AddCommand(statecmd.Cmd)
	rootCmd.AddCommand(clusterconfig.Cmd)
}
