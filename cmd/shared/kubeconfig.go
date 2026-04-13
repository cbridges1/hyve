package shared

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cbridges1/hyve/internal/config"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/types"
)

// CreateLocalKubeconfigManager creates a kubeconfig manager for locally-imported
// external clusters. No Git repository needs to be configured.
func CreateLocalKubeconfigManager() (*kubeconfig.Manager, error) {
	return kubeconfig.NewLocalManager()
}

// CreateKubeconfigManager creates a kubeconfig manager for the current repository
func CreateKubeconfigManager() (*kubeconfig.Manager, string, error) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create repository manager: %w", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return nil, "", fmt.Errorf("no Git repository configured. Use 'hyve git add' to configure a repository")
	}

	kubeconfigMgr, err := kubeconfig.NewManager(currentRepo.Name)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create kubeconfig manager: %w", err)
	}

	return kubeconfigMgr, currentRepo.Name, nil
}

// CreateProviderForCluster creates a provider with the appropriate options for a specific cluster
func CreateProviderForCluster(factory *provider.Factory, clusterDef types.ClusterDefinition) (provider.Provider, error) {
	providerName := clusterDef.Spec.Provider
	if providerName == "" {
		providerName = "civo" // default
	}

	opts := provider.ProviderOptions{
		Region: clusterDef.Metadata.Region,
	}

	// Populate AccountName so the factory can resolve named env vars.
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

	// Handle Civo-specific configuration
	if providerName == "civo" {
		configMgr := config.NewManager()
		apiKey := configMgr.GetCivoToken(clusterDef.Spec.CivoOrganization)
		if apiKey == "" {
			apiKey = os.Getenv("CIVO_TOKEN")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("Civo API token not found. Please run 'hyve config civo token set --org %s' or set CIVO_TOKEN environment variable", clusterDef.Spec.CivoOrganization)
		}
		opts.APIKey = apiKey
	}

	// Handle GCP-specific configuration
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

	// Handle AWS-specific configuration
	if providerName == "aws" && clusterDef.Spec.AWSAccount != "" {
		repoMgr, err := repository.NewManager()
		if err == nil {
			defer repoMgr.Close()
			if currentRepo, err := repoMgr.GetCurrentRepository(); err == nil {
				pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
				if keyID, secret, session, err := pcMgr.GetAWSCredentials(clusterDef.Spec.AWSAccount); err == nil {
					opts.AccessKeyID = keyID
					opts.SecretAccessKey = secret
					opts.SessionToken = session
				}
			}
		}
	}

	// Handle Azure-specific configuration
	if providerName == "azure" {
		opts.AzureResourceGroup = clusterDef.Spec.AzureResourceGroup
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

	return factory.CreateProviderWithOptions(providerName, opts)
}

// RemoveKubeconfig removes a cluster's kubeconfig from ~/.kube/config
func RemoveKubeconfig(clusterName string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}

	kubeConfigPath := filepath.Join(homeDir, ".kube", "config")

	if _, err := os.Stat(kubeConfigPath); os.IsNotExist(err) {
		log.Printf("❌ No kubeconfig found at %s", kubeConfigPath)
		return
	}

	existingData, err := os.ReadFile(kubeConfigPath)
	if err != nil {
		log.Fatalf("Failed to read kubeconfig: %v", err)
	}

	backupPath := kubeConfigPath + ".backup"
	if err := os.WriteFile(backupPath, existingData, 0600); err != nil {
		log.Printf("⚠️  Warning: Failed to create backup at %s", backupPath)
	} else {
		log.Printf("📦 Backup created at %s", backupPath)
	}

	log.Printf("🗑️  Removing cluster '%s' from %s", clusterName, kubeConfigPath)

	removed := false

	if err := kubeconfig.RemoveKubeconfigContext(string(existingData), clusterName, kubeConfigPath); err != nil {
		log.Printf("⚠️  Warning: Failed to remove context: %v", err)
	} else {
		removed = true
	}

	if removed {
		log.Printf("✅ Successfully removed cluster '%s' from %s", clusterName, kubeConfigPath)
		log.Println()
		log.Println("💡 View remaining contexts:")
		log.Println("   kubectl config get-contexts")
	} else {
		log.Printf("⚠️  Context '%s' not found in kubeconfig", clusterName)
	}
}
