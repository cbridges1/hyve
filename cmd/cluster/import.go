package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"hyve/cmd/shared"
	"hyve/internal/providerconfig"
	"hyve/internal/types"
)

func importClusterFromCLI(clusterName, region, providerName string, nodes []string, nodeGroups []types.NodeGroup, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName string) {
	ctx := gocontext.Background()
	stateMgr, stateDir := shared.CreateStateManager(ctx)

	if repoCfg, err := stateMgr.LoadRepoConfig(); err == nil && repoCfg.Reconcile.StrictDelete {
		log.Fatalf("❌ Import is disabled: this repository has strictDelete enabled. " +
			"In strict-delete mode hyve owns the full desired-state; importing an unmanaged cluster would cause it to be deleted on the next reconciliation.")
	}

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		log.Fatalf("Failed to create state directory: %v", err)
	}

	filePath := filepath.Join(stateDir, clusterName+".yaml")
	if _, err := os.Stat(filePath); err == nil {
		log.Fatalf("Cluster '%s' already exists in the repository. Use 'modify' to update it.", clusterName)
	}

	pcMgr := providerconfig.NewManager(filepath.Dir(stateDir))
	var err error

	var gcpProjectID string
	if providerName == "gcp" && projectName != "" {
		gcpProjectID, err = pcMgr.GetGCPProjectID(projectName)
		if err != nil {
			log.Fatalf("GCP project alias '%s' not found.", projectName)
		}
	}

	var awsAccountID, awsVPCID, awsEKSRoleARN, awsNodeRoleARN string
	if providerName == "aws" && accountName != "" {
		awsAccountID, _ = pcMgr.GetAWSAccountID(accountName)
		if vpcName != "" {
			awsVPCID, _ = pcMgr.GetAWSVPCID(accountName, vpcName)
		}
		if eksRoleName != "" {
			awsEKSRoleARN, _ = pcMgr.GetAWSEKSRoleARN(accountName, eksRoleName)
		}
		if nodeRoleName != "" {
			awsNodeRoleARN, _ = pcMgr.GetAWSNodeRoleARN(accountName, nodeRoleName)
		}
	}

	if len(nodeGroups) == 0 && len(nodes) == 0 {
		tempDef := types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Name: clusterName, Region: region},
			Spec: types.ClusterSpec{
				Provider:           providerName,
				CivoOrganization:   orgName,
				AWSAccount:         accountName,
				AWSAccountID:       awsAccountID,
				GCPProject:         projectName,
				GCPProjectID:       gcpProjectID,
				AzureSubscription:  subscriptionName,
				AzureResourceGroup: resolveAzureResourceGroup(pcMgr, subscriptionName),
			},
		}
		if prov, provErr := createProviderForClusterDef(tempDef); provErr == nil {
			if info, infoErr := prov.GetClusterInfo(ctx, clusterName); infoErr == nil && len(info.NodeGroups) > 0 {
				nodeGroups = info.NodeGroups
				log.Printf("📋 Detected %d node group(s) from cloud provider", len(nodeGroups))
			}
		}
	}

	clusterDef := types.ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata: types.ClusterMetadata{
			Name:   clusterName,
			Region: region,
		},
		Spec: types.ClusterSpec{
			Provider:          providerName,
			Nodes:             nodes,
			NodeGroups:        nodeGroups,
			GCPProject:        projectName,
			GCPProjectID:      gcpProjectID,
			AWSAccount:        accountName,
			AWSAccountID:      awsAccountID,
			AWSVPCName:        vpcName,
			AWSVPCID:          awsVPCID,
			AWSEKSRole:        eksRoleName,
			AWSEKSRoleARN:     awsEKSRoleARN,
			AWSNodeRole:       nodeRoleName,
			AWSNodeRoleARN:    awsNodeRoleARN,
			AzureSubscription: subscriptionName,
			CivoOrganization:  orgName,
		},
	}

	data, err := yaml.Marshal(&clusterDef)
	if err != nil {
		log.Fatalf("Failed to marshal cluster definition: %v", err)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		log.Fatalf("Failed to write cluster definition: %v", err)
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Import cluster %s", clusterName))
	log.Printf("✅ Cluster '%s' imported into hyve repository (cloud cluster untouched)", clusterName)
}

func resolveAzureResourceGroup(pcMgr *providerconfig.Manager, subscriptionName string) string {
	if subscriptionName == "" || pcMgr == nil {
		return ""
	}
	rgs, err := pcMgr.ListAzureResourceGroups(subscriptionName)
	if err != nil || len(rgs) == 0 {
		return ""
	}
	return rgs[0].Name
}

func releaseClusterFromCLI(clusterName string) {
	ctx := gocontext.Background()
	stateMgr, stateDir := shared.CreateStateManager(ctx)

	if repoCfg, err := stateMgr.LoadRepoConfig(); err == nil && repoCfg.Reconcile.StrictDelete {
		log.Fatalf("❌ Release is disabled: this repository has strictDelete enabled. " +
			"In strict-delete mode removing a cluster definition would cause the cloud cluster to be deleted on the next reconciliation. " +
			"Use 'hyve cluster delete' instead.")
	}

	filePath := filepath.Join(stateDir, clusterName+".yaml")

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Fatalf("Cluster '%s' not found in repository.", clusterName)
	}

	if err := os.Remove(filePath); err != nil {
		log.Fatalf("Failed to remove cluster definition: %v", err)
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Release cluster %s", clusterName))
	log.Printf("✅ Cluster '%s' released from hyve management. The cloud cluster continues to run.", clusterName)
}
