package cmd

import (
	"github.com/spf13/cobra"

	"hyve/cmd/shared"
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile clusters based on YAML files in Git repository",
	Long: `Reconcile clusters by reading cluster definitions from YAML files in the current Git repository
and ensuring the actual infrastructure matches the desired state.

When --path is provided, the given local repository path is used directly and all
reconciliation runs locally, bypassing the cicd mode check in hyve.yaml. This is
intended for use inside CI/CD pipelines that have already checked out the repository.`,
	Run: func(cmd *cobra.Command, args []string) {
		repoPath, _ := cmd.Flags().GetString("path")
		shared.RunReconciliation(repoPath)
	},
}

func init() {
	reconcileCmd.Flags().StringP("path", "p", "", "Path to a local repository checkout; bypasses cicd mode check and runs reconciliation directly")
}
