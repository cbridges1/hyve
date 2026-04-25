package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/cluster"
	"github.com/cbridges1/hyve/cmd/config"
	gitpkg "github.com/cbridges1/hyve/cmd/git"
	"github.com/cbridges1/hyve/cmd/kubeconfig"
	synccmd "github.com/cbridges1/hyve/cmd/sync"
	"github.com/cbridges1/hyve/cmd/template"
	"github.com/cbridges1/hyve/cmd/workflow"
	"github.com/cbridges1/hyve/internal/database"
)

var hyveHomeFlagValue string

var rootCmd = &cobra.Command{
	Use:   "hyve",
	Short: "Hyve cluster management CLI",
	Long: `A CLI tool for managing Kubernetes clusters on various cloud providers.
Supports cluster creation, modification, deletion, and reconciliation.`,
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		home := resolvedHyveHome()
		if home != "" {
			database.SetConfigDir(home)
		}
		return nil
	},
}

// HyveHome returns the effective Hyve home directory. It respects (in order):
//  1. --home flag
//  2. HYVE_HOME environment variable
//  3. ~/.hyve (default)
func HyveHome() string {
	if home := resolvedHyveHome(); home != "" {
		return home
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".hyve")
}

func resolvedHyveHome() string {
	if hyveHomeFlagValue != "" {
		return hyveHomeFlagValue
	}
	return os.Getenv("HYVE_HOME")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// Inject HyveHome into the git package to avoid circular imports
	gitpkg.SetHyveHomeFunc(HyveHome)

	rootCmd.PersistentFlags().StringVar(&hyveHomeFlagValue, "home", "", "Hyve home directory (default: ~/.hyve). Also read from HYVE_HOME env var.")

	rootCmd.AddCommand(reconcileCmd)
	rootCmd.AddCommand(cluster.Cmd)
	rootCmd.AddCommand(gitpkg.Cmd)
	rootCmd.AddCommand(kubeconfig.Cmd)
	rootCmd.AddCommand(config.Cmd)
	rootCmd.AddCommand(synccmd.Cmd)
	rootCmd.AddCommand(useCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(workflow.Cmd)
	rootCmd.AddCommand(template.Cmd)
	rootCmd.AddCommand(interactiveCmd)
}
