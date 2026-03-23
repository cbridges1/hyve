package reconcile

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cbridges1/hyve/internal/cluster"
)

// exportClusterInfoToEnv exports cluster information to environment variables
func exportClusterInfoToEnv(ctx context.Context, clusterMgr *cluster.Manager, clusterName string) error {
	clusterInfo, err := clusterMgr.GetClusterInfo(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get cluster info: %w", err)
	}

	if clusterInfo.Status != "ACTIVE" {
		log.Printf("Cluster %s is not active (status: %s), skipping export", clusterInfo.Name, clusterInfo.Status)
		return nil
	}

	if githubEnv := os.Getenv("GITHUB_ENV"); githubEnv != "" {
		log.Printf("Exporting cluster information to GitHub Actions environment")
		file, err := os.OpenFile(githubEnv, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("Warning: Failed to open GITHUB_ENV file: %v", err)
		} else {
			defer file.Close()

			fmt.Fprintf(file, "HYVE_CLUSTER_NAME=%s\n", clusterInfo.Name)
			fmt.Fprintf(file, "HYVE_CLUSTER_IP_ADDRESS=%s\n", clusterInfo.IPAddress)
			fmt.Fprintf(file, "HYVE_CLUSTER_ACCESS_PORT=%s\n", clusterInfo.AccessPort)
			fmt.Fprintf(file, "HYVE_CLUSTER_ID=%s\n", clusterInfo.ID)
			fmt.Fprintf(file, "HYVE_CLUSTER_STATUS=%s\n", clusterInfo.Status)
			// Kubeconfig is multi-line YAML; use the heredoc syntax required by GITHUB_ENV
			fmt.Fprintf(file, "HYVE_CLUSTER_KUBECONFIG<<HYVE_EOF\n%s\nHYVE_EOF\n", clusterInfo.Kubeconfig)

			log.Printf("✅ Exported cluster information to GitHub Actions environment:")
			log.Printf("  HYVE_CLUSTER_NAME=%s", clusterInfo.Name)
			log.Printf("  HYVE_CLUSTER_IP_ADDRESS=%s", clusterInfo.IPAddress)
			log.Printf("  HYVE_CLUSTER_ACCESS_PORT=%s", clusterInfo.AccessPort)
			log.Printf("  HYVE_CLUSTER_ID=%s", clusterInfo.ID)
			log.Printf("  HYVE_CLUSTER_STATUS=%s", clusterInfo.Status)
			log.Printf("  HYVE_CLUSTER_KUBECONFIG=<kubeconfig content>")
		}
	}

	os.Setenv("HYVE_CLUSTER_NAME", clusterInfo.Name)
	os.Setenv("HYVE_CLUSTER_IP_ADDRESS", clusterInfo.IPAddress)
	os.Setenv("HYVE_CLUSTER_ACCESS_PORT", clusterInfo.AccessPort)
	os.Setenv("HYVE_CLUSTER_ID", clusterInfo.ID)
	os.Setenv("HYVE_CLUSTER_STATUS", clusterInfo.Status)
	os.Setenv("HYVE_CLUSTER_KUBECONFIG", clusterInfo.Kubeconfig)

	return nil
}
