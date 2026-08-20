package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile clusters based on YAML files in the active state directory",
	Long: `Reconcile clusters by reading cluster definitions from YAML files in the active
environment's directory (see 'hyve env') and ensuring the actual
infrastructure matches the desired state. No git repository is required —
a plain local directory works the same as a git checkout.

When --path is provided, the given local directory is used directly and all
reconciliation runs locally, bypassing the cicd mode check in hyve.yaml. This is
intended for use inside CI/CD pipelines that have already checked out the repository.

Cluster mode (a valid 'hyve login' session exists) and no --path given: this
command is a no-op. There's no local checkout to reconcile against, and
nothing to trigger — the deployed controller already watches every
ClusterDefinition and reconciles it continuously via Kubernetes' own
reconcile loop, so a one-shot CLI trigger doesn't map onto anything
meaningful. Use 'kubectl get clusterdefinitions' / 'kubectl describe' to
check status instead. --path still runs a local reconciliation regardless of
session state, same as it already bypasses the cicd mode check — it's an
explicit "reconcile this directory" request, independent of cluster mode.`,
	Run: func(cmd *cobra.Command, args []string) {
		repoPath, _ := cmd.Flags().GetString("path")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if repoPath == "" {
			if _, ok := shared.UseClusterMode(); ok {
				fmt.Println("Logged in to a cluster-mode API server — reconciliation is continuous, handled by the deployed controller's own Kubernetes-native reconcile loop.")
				fmt.Println("Nothing to trigger here. Check status with: kubectl get clusterdefinitions")
				return
			}
		}
		shared.RunReconciliation(repoPath, dryRun)
	},
}

func init() {
	reconcileCmd.Flags().StringP("path", "p", "", "Path to a local repository checkout; bypasses cicd mode check and runs reconciliation directly")
	reconcileCmd.Flags().Bool("dry-run", false, "Preview what reconcile would do without changing anything — cluster create/delete/scale/workflows and resource apply/delete are all skipped and logged instead of run")
}
