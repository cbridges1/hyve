package cmd

import (
	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile clusters based on YAML files in the active state directory",
	Long: `Reconcile clusters by reading cluster definitions from YAML files in the active
state directory (see 'hyve set-state') and ensuring the actual infrastructure
matches the desired state. No git repository is required — a plain local
directory works the same as a git checkout.

When --path is provided, the given local directory is used directly and all
reconciliation runs locally, bypassing the cicd mode check in hyve.yaml. This is
intended for use inside CI/CD pipelines that have already checked out the repository.`,
	Run: func(cmd *cobra.Command, args []string) {
		repoPath, _ := cmd.Flags().GetString("path")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		shared.RunReconciliation(repoPath, dryRun)
	},
}

func init() {
	reconcileCmd.Flags().StringP("path", "p", "", "Path to a local repository checkout; bypasses cicd mode check and runs reconciliation directly")
	reconcileCmd.Flags().Bool("dry-run", false, "Preview what reconcile would do without changing anything — cluster create/delete/scale/workflows and resource apply/delete are all skipped and logged instead of run")
}
