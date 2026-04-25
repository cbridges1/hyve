package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	internalcluster "github.com/cbridges1/hyve/internal/cluster"
	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/types"
)

func deleteClusterFromCLI(clusterName string, allowNoConfig bool, deleteFromCloud bool) {
	ctx := gocontext.Background()
	stateMgr, stateDir := shared.CreateStateManager(ctx)
	filePath := filepath.Join(stateDir, clusterName+".yaml")

	var clusterDef types.ClusterDefinition
	configExists := false

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if deleteFromCloud && allowNoConfig {
			log.Printf("⚠️ Configuration file not found, but --force --force-cloud specified")
			clusterDef.Metadata.Name = clusterName
		} else {
			log.Fatalf("Cluster %s configuration does not exist. Use --force --force-cloud to delete from cloud provider anyway.", clusterName)
		}
	} else {
		configExists = true
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Fatalf("Failed to read cluster definition: %v", err)
		}
		if err := yaml.Unmarshal(data, &clusterDef); err != nil {
			log.Fatalf("Failed to parse cluster definition: %v", err)
		}
	}

	if deleteFromCloud {
		// --force-cloud: delete from the cloud provider immediately, then remove the YAML.
		log.Printf("🗑️ Deleting cluster '%s' from cloud provider...", clusterName)
		if err := deleteClusterExplicitly(ctx, clusterDef); err != nil {
			log.Fatalf("❌ Failed to delete cluster '%s' from cloud provider: %v\n\n"+
				"Configuration file was NOT removed to prevent orphaned cluster state.\n"+
				"Please resolve the issue and try again.", clusterName, err)
		}
		log.Printf("✅ Cloud cluster '%s' deleted", clusterName)

		if configExists {
			if err := os.Remove(filePath); err != nil {
				log.Fatalf("Failed to delete cluster definition file: %v", err)
			}
			shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Delete cluster %s", clusterName))
			log.Printf("Deleted cluster definition file: %s", filePath)
		}

		cleanupClusterKubeconfig(clusterName)
		shared.RunReconciliation("")
	} else {
		// Normal path: mark the cluster for deletion so the reconciler runs onDestroy
		// workflows before removing the cloud cluster and the YAML file.
		if !configExists {
			log.Fatalf("Cluster %s configuration does not exist. Use --force --force-cloud to delete from cloud provider anyway.", clusterName)
		}

		clusterDef.Spec.Delete = true
		data, err := yaml.Marshal(&clusterDef)
		if err != nil {
			log.Fatalf("Failed to marshal cluster definition: %v", err)
		}
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			log.Fatalf("Failed to write cluster definition: %v", err)
		}

		shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Mark cluster %s for deletion", clusterName))
		log.Printf("📝 Cluster '%s' marked for deletion", clusterName)
		log.Printf("   The reconciler will run onDestroy workflows, delete the cloud cluster, and remove this file.")

		shared.RunReconciliation("")
	}
}

func cleanupClusterKubeconfig(clusterName string) {
	kubeconfigMgr, _, err := shared.CreateKubeconfigManager()
	if err != nil {
		log.Printf("⚠️  Could not open kubeconfig database: %v", err)
	} else {
		defer kubeconfigMgr.Close()
		if err := kubeconfigMgr.DeleteKubeconfig(clusterName); err != nil {
			log.Printf("⚠️  Failed to remove kubeconfig from database: %v", err)
		} else {
			log.Printf("🗑️  Removed kubeconfig for '%s' from Hyve database", clusterName)
		}
	}

	shared.RemoveKubeconfig(clusterName)
}

func deleteClusterExplicitly(ctx gocontext.Context, clusterDef types.ClusterDefinition) error {
	clusterName := clusterDef.Metadata.Name
	region := clusterDef.Metadata.Region
	providerName := clusterDef.Spec.Provider
	if providerName == "" {
		providerName = "civo"
	}

	prov, err := createProviderForClusterDef(clusterDef)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	clusterMgr := internalcluster.NewManager(prov)

	log.Printf("🔍 Explicitly searching for cluster '%s' in region %s (provider: %s)...", clusterName, region, providerName)

	existingCluster, err := clusterMgr.FindByName(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("failed to search for cluster: %w", err)
	}

	if existingCluster == nil {
		log.Printf("✅ Cluster '%s' not found in cloud provider, may already be deleted", clusterName)
		return nil
	}

	log.Printf("🗑️ Found cluster '%s' with ID %s, explicitly deleting...", clusterName, existingCluster.ID)

	err = clusterMgr.Delete(ctx, existingCluster.ID)
	if err != nil {
		return fmt.Errorf("failed to delete cluster %s (ID: %s): %w", clusterName, existingCluster.ID, err)
	}

	log.Printf("✅ Successfully deleted cluster '%s' from cloud provider", clusterName)
	return nil
}

// CreateProviderForClusterDef creates a cloud provider instance for the given cluster definition.
func CreateProviderForClusterDef(clusterDef types.ClusterDefinition) (provider.Provider, error) {
	return createProviderForClusterDef(clusterDef)
}

func createProviderForClusterDef(clusterDef types.ClusterDefinition) (provider.Provider, error) {
	providerName := clusterDef.Spec.Provider
	if providerName == "" {
		providerName = "civo"
	}

	providerFactory := provider.NewFactory()

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
		apiKey := providerconfig.ReadCivoCLIToken()
		if apiKey == "" {
			apiKey = os.Getenv("CIVO_TOKEN")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("Civo API token not found. Log in with the Civo CLI ('civo apikey') or set the CIVO_TOKEN environment variable")
		}
		opts.APIKey = apiKey
	}

	if providerName == "gcp" {
		if clusterDef.Spec.GCPProjectID != "" {
			opts.ProjectID = clusterDef.Spec.GCPProjectID
			log.Printf("Using GCP project ID '%s'", clusterDef.Spec.GCPProjectID)
		} else if clusterDef.Spec.GCPProject != "" {
			gcpRepoMgr, err := repository.NewManager()
			if err == nil {
				defer gcpRepoMgr.Close()
				if currentRepo, err := gcpRepoMgr.GetCurrentRepository(); err == nil {
					pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
					projectID, err := pcMgr.GetGCPProjectID(clusterDef.Spec.GCPProject)
					if err != nil {
						return nil, fmt.Errorf("failed to resolve GCP project '%s': %w", clusterDef.Spec.GCPProject, err)
					}
					opts.ProjectID = projectID
					log.Printf("Using GCP project '%s' (ID: %s)", clusterDef.Spec.GCPProject, projectID)
				}
			}
		}
	}

	if providerName == "azure" {
		opts.AzureResourceGroup = clusterDef.Spec.AzureResourceGroup
		if clusterDef.Spec.AzureSubscriptionID != "" {
			opts.AzureSubscriptionID = clusterDef.Spec.AzureSubscriptionID
		} else if clusterDef.Spec.AzureSubscription != "" {
			azureRepoMgr, err := repository.NewManager()
			if err == nil {
				defer azureRepoMgr.Close()
				if currentRepo, err := azureRepoMgr.GetCurrentRepository(); err == nil {
					pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
					subscriptionID, err := pcMgr.GetAzureSubscriptionID(clusterDef.Spec.AzureSubscription)
					if err != nil {
						return nil, fmt.Errorf("failed to resolve Azure subscription '%s': %w", clusterDef.Spec.AzureSubscription, err)
					}
					opts.AzureSubscriptionID = subscriptionID
					log.Printf("Using Azure subscription '%s' (ID: %s)", clusterDef.Spec.AzureSubscription, subscriptionID)
				}
			}
		}
	}

	return providerFactory.CreateProviderWithOptions(providerName, opts)
}

func forceDeleteClusterFromCloud(clusterName, region, providerName, projectName string, accountAlias ...string) {
	alias := ""
	if len(accountAlias) > 0 {
		alias = accountAlias[0]
	}
	ctx := gocontext.Background()

	regions := []string{region}
	if region == "" {
		switch providerName {
		case "civo":
			regions = []string{"PHX1", "NYC1", "FRA1", "LON1"}
		case "gcp":
			regions = []string{"us-central1", "us-east1", "us-west1", "europe-west1"}
		case "aws":
			regions = []string{"us-east-1", "us-west-2", "eu-west-1", "ap-southeast-1"}
		case "azure":
			regions = []string{"eastus", "westus2", "westeurope", "southeastasia"}
		default:
			regions = []string{"PHX1"}
		}
		log.Printf("🔍 No region specified, searching common %s regions: %v", providerName, regions)
	}

	opts := provider.ProviderOptions{}

	if providerName == "civo" {
		apiKey := providerconfig.ReadCivoCLIToken()
		if apiKey == "" {
			apiKey = os.Getenv("CIVO_TOKEN")
		}
		if apiKey == "" {
			log.Fatalf("Civo API token not found. Log in with the Civo CLI ('civo apikey') or set the CIVO_TOKEN environment variable")
		}
		opts.APIKey = apiKey
	}

	var fdPcMgr *providerconfig.Manager
	{
		fdRepoMgr, err := repository.NewManager()
		if err == nil {
			defer fdRepoMgr.Close()
			if fdCurrentRepo, err := fdRepoMgr.GetCurrentRepository(); err == nil {
				fdPcMgr = providerconfig.NewManager(fdCurrentRepo.LocalPath)
			}
		}
	}

	if providerName == "gcp" && projectName != "" {
		if fdPcMgr == nil {
			log.Fatalf("Failed to load repository configuration for GCP project lookup")
		}
		projectID, err := fdPcMgr.GetGCPProjectID(projectName)
		if err != nil {
			log.Fatalf("GCP project alias '%s' not found in repository configuration.\n"+
				"Use 'hyve config gcp project add --name %s --id <project-id>' to add it.", projectName, projectName)
		}
		opts.ProjectID = projectID
		log.Printf("Using GCP project '%s' (ID: %s)", projectName, projectID)
		regions = []string{"-"}
	}

	if providerName == "aws" && alias != "" && fdPcMgr != nil {
		keyID, secret, tok, err := fdPcMgr.GetAWSCredentials(alias)
		if err == nil {
			opts.AccessKeyID = keyID
			opts.SecretAccessKey = secret
			opts.SessionToken = tok
		}
		opts.AccountName = alias
	}

	if providerName == "azure" && alias != "" && fdPcMgr != nil {
		subID, _ := fdPcMgr.GetAzureSubscriptionID(alias)
		tenantID, clientID, clientSecret, err := fdPcMgr.GetAzureCredentials(alias)
		if err == nil {
			opts.AzureTenantID = tenantID
			opts.AzureClientID = clientID
			opts.AzureClientSecret = clientSecret
		}
		opts.AzureSubscriptionID = subID
		opts.AccountName = alias
	}

	providerFactory := provider.NewFactory()
	found := false

	for _, r := range regions {
		log.Printf("🔍 Searching for cluster '%s' in region %s (provider: %s)...", clusterName, r, providerName)

		opts.Region = r
		prov, err := providerFactory.CreateProviderWithOptions(providerName, opts)
		if err != nil {
			log.Printf("Failed to create provider for region %s: %v", r, err)
			continue
		}

		clusterMgr := internalcluster.NewManager(prov)

		existingCluster, err := clusterMgr.FindByName(ctx, clusterName)
		if err != nil {
			log.Printf("Failed to search for cluster in region %s: %v", r, err)
			continue
		}

		if existingCluster == nil {
			log.Printf("❌ Cluster '%s' not found in region %s", clusterName, r)
			continue
		}

		found = true
		log.Printf("✅ Found cluster '%s' in region %s with ID %s", clusterName, r, existingCluster.ID)
		log.Printf("🗑️ Force deleting cluster '%s'...", clusterName)

		err = clusterMgr.Delete(ctx, existingCluster.ID)
		if err != nil {
			log.Fatalf("Failed to delete cluster %s (ID: %s): %v", clusterName, existingCluster.ID, err)
		}

		log.Printf("✅ Successfully force deleted cluster '%s' from region %s", clusterName, r)
		break
	}

	if !found {
		log.Printf("❌ Cluster '%s' not found in any searched regions", clusterName)
		log.Printf("💡 Try specifying a specific region with --region flag")
	}
}
