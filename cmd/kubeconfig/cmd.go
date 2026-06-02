package kubeconfig

import (
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/kubeconfig"
)

// Cmd is the root kubeconfig command exposed to the parent.
var Cmd = kubeconfigCmd

var kubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Manage kubeconfig contexts",
	Long: `Commands to manage kubectl contexts in ~/.kube/config.

Kubeconfigs are configured by running 'hyve cluster auth <name>', which
executes the module's auth operation (e.g. civo kubernetes config --save)
and merges the cluster context into ~/.kube/config automatically.`,
}

var kubeconfigRemoveCmd = &cobra.Command{
	Use:   "remove [cluster-name]",
	Short: "Remove a cluster context from ~/.kube/config",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeFromKubeConfig(args[0])
	},
}

func init() {
	kubeconfigCmd.AddCommand(kubeconfigRemoveCmd)
}

func removeFromKubeConfig(clusterName string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}
	kubeConfigPath := filepath.Join(homeDir, ".kube", "config")
	if _, err := os.Stat(kubeConfigPath); os.IsNotExist(err) {
		log.Printf("No kubeconfig found at %s", kubeConfigPath)
		return
	}
	existingData, err := os.ReadFile(kubeConfigPath)
	if err != nil {
		log.Fatalf("Failed to read kubeconfig: %v", err)
	}
	if err := kubeconfig.RemoveKubeconfigContext(string(existingData), clusterName, kubeConfigPath); err != nil {
		log.Fatalf("Failed to remove context: %v", err)
	}
	log.Printf("Removed cluster '%s' from %s", clusterName, kubeConfigPath)
}
