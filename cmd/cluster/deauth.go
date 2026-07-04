package cluster

import (
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/kubeconfig"
)

var deauthCmd = &cobra.Command{
	Use:   "deauth [cluster-name]",
	Short: "Remove a cluster's context from ~/.kube/config",
	Long:  "Remove the context, cluster, and user entries a previous `hyve cluster auth` added to ~/.kube/config.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeFromKubeConfig(args[0])
	},
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
