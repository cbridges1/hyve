package shared

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"hyve/internal/cluster"
	"hyve/internal/provider"
	"hyve/internal/providerconfig"
	"hyve/internal/repository"
	"hyve/internal/types"
)

// ExportClusterInfo exports cluster information to environment variables and GitHub Actions.
func ExportClusterInfo(ctx context.Context, apiKey string, clusterDef types.ClusterDefinition) error {
	factory := provider.NewFactory()

	providerName := clusterDef.Spec.Provider
	if providerName == "" {
		providerName = "civo"
	}

	opts := provider.ProviderOptions{
		Region: clusterDef.Metadata.Region,
	}

	switch strings.ToLower(providerName) {
	case "civo":
		opts.AccountName = clusterDef.Spec.CivoOrganization
	case "aws":
		opts.AccountName = clusterDef.Spec.AWSAccount
	case "gcp":
		opts.AccountName = clusterDef.Spec.GCPProject
	case "azure":
		opts.AccountName = clusterDef.Spec.AzureSubscription
	}

	if providerName == "civo" {
		opts.APIKey = apiKey
	}

	if providerName == "gcp" {
		if clusterDef.Spec.GCPProjectID != "" {
			opts.ProjectID = clusterDef.Spec.GCPProjectID
		} else if clusterDef.Spec.GCPProject != "" {
			repoMgr, err := repository.NewManager()
			if err == nil {
				defer repoMgr.Close()
				if currentRepo, err := repoMgr.GetCurrentRepository(); err == nil {
					pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
					if projectID, err := pcMgr.GetGCPProjectID(clusterDef.Spec.GCPProject); err == nil {
						opts.ProjectID = projectID
					}
				}
			}
		}
	}

	if providerName == "azure" {
		if clusterDef.Spec.AzureSubscriptionID != "" {
			opts.AzureSubscriptionID = clusterDef.Spec.AzureSubscriptionID
		} else if clusterDef.Spec.AzureSubscription != "" {
			azRepoMgr, err := repository.NewManager()
			if err == nil {
				defer azRepoMgr.Close()
				if currentRepo, err := azRepoMgr.GetCurrentRepository(); err == nil {
					pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
					if subscriptionID, err := pcMgr.GetAzureSubscriptionID(clusterDef.Spec.AzureSubscription); err == nil {
						opts.AzureSubscriptionID = subscriptionID
					}
				}
			}
		}
	}

	prov, err := factory.CreateProviderWithOptions(providerName, opts)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	clusterMgr := cluster.NewManager(prov)

	clusterInfo, err := clusterMgr.GetClusterInfo(ctx, clusterDef.Metadata.Name)
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
