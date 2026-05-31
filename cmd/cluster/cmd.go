package cluster

import (
	"github.com/spf13/cobra"
)

// Cmd is the cluster command.
var Cmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage clusters",
	Long:  "Commands to list, inspect, delete, and configure clusters",
}

var showCmd = &cobra.Command{
	Use:   "show [cluster-name]",
	Short: "Show cluster definition",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		showCluster(args[0])
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete [cluster-name]",
	Short: "Mark a cluster for deletion (reconciler runs onDelete + module delete)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		markClusterForDeletion(args[0])
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cluster definitions",
	Run: func(cmd *cobra.Command, args []string) {
		listClusters()
	},
}

func init() {
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(showCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(authCmd)
}
