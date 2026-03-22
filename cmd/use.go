package cmd

import (
	"github.com/spf13/cobra"

	"hyve/cmd/kubeconfig"
)

var useCmd = &cobra.Command{
	Use:   "use [cluster-name]",
	Short: "Merge cluster into ~/.kube/config and set as active context",
	Long:  "Convenience command that merges the cluster's kubeconfig into ~/.kube/config and sets it as the active kubectl context. Equivalent to 'hyve kubeconfig use'",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		kubeconfig.UseKubeconfig(clusterName)
	},
}
