package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/config"
	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/types"
)

func createClusterFromCLI(clusterName, region, providerName string, nodes []string, nodeGroups []types.NodeGroup, clusterType, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName string, beforeCreate, onCreated, onDestroy, afterDelete []string, pause bool, expiresAt string) {
	ctx := gocontext.Background()
	stateMgr, stateDir := shared.CreateStateManager(ctx)

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		log.Fatalf("Failed to create state directory: %v", err)
	}

	filePath := filepath.Join(stateDir, clusterName+".yaml")

	if _, err := os.Stat(filePath); err == nil {
		log.Fatalf("Cluster %s already exists. Use 'modify' action to update it.", clusterName)
	}

	pcMgr := providerconfig.NewManager(filepath.Dir(stateDir))
	var err error

	var gcpProjectID string
	if providerName == "gcp" && projectName != "" {
		gcpProjectID, err = pcMgr.GetGCPProjectID(projectName)
		if err != nil {
			log.Fatalf("GCP project alias '%s' not found in repository configuration.\n"+
				"Use 'hyve config gcp project add --name %s --id <project-id>' to add it.", projectName, projectName)
		}
		log.Printf("Using GCP project '%s' (ID: %s)", projectName, gcpProjectID)
	}

	var awsAccountID string
	var awsVPCID string
	if providerName == "aws" {
		if accountName != "" {
			awsAccountID, err = pcMgr.GetAWSAccountID(accountName)
			if err != nil {
				log.Fatalf("AWS account alias '%s' not found in repository configuration.\n"+
					"Use 'hyve config aws account add --name %s --id <account-id>' to add it.", accountName, accountName)
			}
			log.Printf("Using AWS account '%s' (ID: %s)", accountName, awsAccountID)
		}

		// VPC name is resolved to ID via provider config alias if provided.
		if vpcName != "" {
			awsVPCID, err = pcMgr.GetAWSVPCID(accountName, vpcName)
			if err != nil {
				log.Printf("Warning: AWS VPC alias '%s' not found in account '%s' — storing name only.", vpcName, accountName)
			} else {
				log.Printf("Using AWS VPC '%s' (ID: %s)", vpcName, awsVPCID)
			}
		}

		// EKS role and node role are stored as direct names; ARNs are resolved at reconcile time.
		if eksRoleName != "" {
			log.Printf("EKS role name: %s (ARN resolved at reconcile time)", eksRoleName)
		}
		if nodeRoleName != "" {
			log.Printf("Node role name: %s (ARN resolved at reconcile time)", nodeRoleName)
		}
	}

	if providerName != "civo" {
		clusterType = ""
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
			ClusterType:       clusterType,
			GCPProject:        projectName,
			GCPProjectID:      gcpProjectID,
			AWSAccount:        accountName,
			AWSAccountID:      awsAccountID,
			AWSVPCName:        vpcName,
			AWSVPCID:          awsVPCID,
			AWSEKSRoleName:    eksRoleName,
			AWSNodeRoleName:   nodeRoleName,
			AzureSubscription: subscriptionName,
			CivoOrganization:  orgName,
			Pause:             pause,
			ExpiresAt:         expiresAt,
			Workflows: types.WorkflowsSpec{
				BeforeCreate: beforeCreate,
				OnCreated:    onCreated,
				OnDestroy:    onDestroy,
				AfterDelete:  afterDelete,
			},
			Ingress: types.IngressSpec{
				Enabled:      true,
				LoadBalancer: true,
			},
		},
	}

	data, err := yaml.Marshal(&clusterDef)
	if err != nil {
		log.Fatalf("Failed to marshal cluster definition: %v", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		log.Fatalf("Failed to write cluster definition file: %v", err)
	}

	log.Printf("Created cluster definition file: %s", filePath)
	log.Printf("Cluster %s configuration:", clusterName)
	log.Printf("  Region: %s", region)
	log.Printf("  Provider: %s", providerName)
	log.Printf("  Nodes: %v", nodes)
	log.Printf("  Cluster Type: %s", clusterType)
	if projectName != "" {
		log.Printf("  GCP Project: %s (ID: %s)", projectName, gcpProjectID)
	}
	if awsVPCID != "" {
		log.Printf("  AWS VPC: %s (ID: %s)", vpcName, awsVPCID)
	}
	if eksRoleName != "" {
		log.Printf("  AWS EKS Role: %s", eksRoleName)
	}
	if nodeRoleName != "" {
		log.Printf("  AWS Node Role: %s", nodeRoleName)
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Add cluster %s", clusterName))

	log.Printf("Exporting cluster information...")
	configMgr := config.NewManager()
	if apiKey := configMgr.GetCivoToken(clusterDef.Spec.CivoOrganization); apiKey != "" {
		err := shared.ExportClusterInfo(ctx, apiKey, clusterDef)
		if err != nil {
			log.Printf("Warning: Failed to export cluster info: %v", err)
		}
	}

	shared.RunReconciliation("")
}

func modifyClusterFromCLI(cmd *cobra.Command, clusterName string) {
	ctx := gocontext.Background()
	stateMgr, stateDir := shared.CreateStateManager(ctx)
	filePath := filepath.Join(stateDir, clusterName+".yaml")

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Fatalf("Cluster %s does not exist. Use 'create' action to create it.", clusterName)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read existing cluster file: %v", err)
	}

	var clusterDef types.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		log.Fatalf("Failed to parse existing cluster definition: %v", err)
	}

	if cmd.Flags().Changed("region") {
		region, _ := cmd.Flags().GetString("region")
		clusterDef.Metadata.Region = region
	}
	if cmd.Flags().Changed("provider") {
		provider, _ := cmd.Flags().GetString("provider")
		clusterDef.Spec.Provider = strings.ToLower(provider)
	}
	if cmd.Flags().Changed("nodes") {
		nodes, _ := cmd.Flags().GetStringSlice("nodes")
		clusterDef.Spec.Nodes = nodes
	}
	if cmd.Flags().Changed("cluster-type") {
		if clusterDef.Spec.Provider != "civo" {
			log.Printf("⚠️  --cluster-type is only supported for the Civo provider and will be ignored for '%s'", clusterDef.Spec.Provider)
		} else {
			clusterType, _ := cmd.Flags().GetString("cluster-type")
			clusterDef.Spec.ClusterType = clusterType
		}
	}
	if cmd.Flags().Changed("node-group") {
		nodeGroupStrs, _ := cmd.Flags().GetStringArray("node-group")
		var nodeGroups []types.NodeGroup
		for _, s := range nodeGroupStrs {
			ng, err := shared.ParseNodeGroup(s)
			if err != nil {
				log.Fatalf("Invalid --node-group value '%s': %v", s, err)
			}
			nodeGroups = append(nodeGroups, ng)
		}
		clusterDef.Spec.NodeGroups = nodeGroups
	}
	if cmd.Flags().Changed("pause") {
		clusterDef.Spec.Pause = true
	}
	if cmd.Flags().Changed("unpause") {
		clusterDef.Spec.Pause = false
	}
	if cmd.Flags().Changed("expires-at") {
		val, _ := cmd.Flags().GetString("expires-at")
		if strings.ToLower(val) == "none" {
			clusterDef.Spec.ExpiresAt = ""
		} else {
			clusterDef.Spec.ExpiresAt = val
		}
	}
	if cmd.Flags().Changed("before-create") {
		vals, _ := cmd.Flags().GetStringArray("before-create")
		clusterDef.Spec.Workflows.BeforeCreate = vals
	}
	if cmd.Flags().Changed("after-delete") {
		vals, _ := cmd.Flags().GetStringArray("after-delete")
		clusterDef.Spec.Workflows.AfterDelete = vals
	}
	if cmd.Flags().Changed("eks-role-name") {
		val, _ := cmd.Flags().GetString("eks-role-name")
		clusterDef.Spec.AWSEKSRoleName = val
	}
	if cmd.Flags().Changed("node-role-name") {
		val, _ := cmd.Flags().GetString("node-role-name")
		clusterDef.Spec.AWSNodeRoleName = val
	}

	updatedData, err := yaml.Marshal(&clusterDef)
	if err != nil {
		log.Fatalf("Failed to marshal updated cluster definition: %v", err)
	}

	if err := os.WriteFile(filePath, updatedData, 0644); err != nil {
		log.Fatalf("Failed to write updated cluster definition file: %v", err)
	}

	log.Printf("Updated cluster definition file: %s", filePath)
	log.Printf("Cluster %s updated configuration:", clusterName)
	log.Printf("  Region: %s", clusterDef.Metadata.Region)
	log.Printf("  Provider: %s", clusterDef.Spec.Provider)
	log.Printf("  Nodes: %v", clusterDef.Spec.Nodes)
	log.Printf("  Cluster Type: %s", clusterDef.Spec.ClusterType)
	if clusterDef.Spec.Pause {
		log.Printf("  Pause: true (reconciliation skipped)")
	}
	if clusterDef.Spec.ExpiresAt != "" {
		log.Printf("  Expires At: %s", clusterDef.Spec.ExpiresAt)
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Modify cluster %s", clusterName))

	log.Printf("Exporting cluster information...")
	configMgr := config.NewManager()
	if apiKey := configMgr.GetCivoToken(clusterDef.Spec.CivoOrganization); apiKey != "" {
		err := shared.ExportClusterInfo(ctx, apiKey, clusterDef)
		if err != nil {
			log.Printf("Warning: Failed to export cluster info: %v", err)
		}
	}
}

func showCluster(clusterName string) {
	ctx := gocontext.Background()
	_, clustersDir := shared.CreateStateManager(ctx)
	filePath := filepath.Join(clustersDir, clusterName+".yaml")

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Cluster '%s' not found. Use 'hyve cluster list' to see available clusters.", clusterName)
		}
		log.Fatalf("Failed to read cluster file: %v", err)
	}

	var clusterDef types.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		log.Fatalf("Failed to parse cluster definition: %v", err)
	}

	fmt.Printf("---\n%s", string(data))
	fmt.Println()
	fmt.Printf("Summary:\n")
	fmt.Printf("  Name:     %s\n", clusterDef.Metadata.Name)
	fmt.Printf("  Provider: %s\n", clusterDef.Spec.Provider)
	fmt.Printf("  Region:   %s\n", clusterDef.Metadata.Region)
	if len(clusterDef.Spec.NodeGroups) > 0 {
		fmt.Printf("  NodeGroups:\n")
		for _, ng := range clusterDef.Spec.NodeGroups {
			fmt.Printf("    - %s: %s x%d\n", ng.Name, ng.InstanceType, ng.Count)
		}
	}
	if clusterDef.Spec.Pause {
		fmt.Printf("  Pause:    true\n")
	}
	if clusterDef.Spec.ExpiresAt != "" {
		fmt.Printf("  ExpiresAt: %s\n", clusterDef.Spec.ExpiresAt)
	}
	if len(clusterDef.Spec.Workflows.BeforeCreate) > 0 {
		fmt.Printf("  BeforeCreate: %v\n", clusterDef.Spec.Workflows.BeforeCreate)
	}
	if len(clusterDef.Spec.Workflows.OnCreated) > 0 {
		fmt.Printf("  OnCreated: %v\n", clusterDef.Spec.Workflows.OnCreated)
	}
	if len(clusterDef.Spec.Workflows.OnDestroy) > 0 {
		fmt.Printf("  OnDestroy: %v\n", clusterDef.Spec.Workflows.OnDestroy)
	}
	if len(clusterDef.Spec.Workflows.AfterDelete) > 0 {
		fmt.Printf("  AfterDelete: %v\n", clusterDef.Spec.Workflows.AfterDelete)
	}
	if len(clusterDef.Spec.PendingWorkflows) > 0 {
		fmt.Printf("  PendingWorkflows: %d queued\n", len(clusterDef.Spec.PendingWorkflows))
	}
}

func listClusters() {
	ctx := gocontext.Background()
	_, clustersDir := shared.CreateStateManager(ctx)

	if _, err := os.Stat(clustersDir); os.IsNotExist(err) {
		log.Println("❌ No clusters found")
		log.Println("\n💡 Run 'hyve cluster create <name>' to create a cluster")
		return
	}

	entries, err := os.ReadDir(clustersDir)
	if err != nil {
		log.Fatalf("Failed to read clusters directory: %v", err)
	}

	var clusters []types.ClusterDefinition
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		filePath := filepath.Join(clustersDir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("Warning: Failed to read %s: %v", name, err)
			continue
		}

		var clusterDef types.ClusterDefinition
		if err := yaml.Unmarshal(data, &clusterDef); err != nil {
			log.Printf("Warning: Failed to parse %s: %v", name, err)
			continue
		}

		if clusterDef.Kind == "Cluster" && !clusterDef.Spec.Delete {
			clusters = append(clusters, clusterDef)
		}
	}

	if len(clusters) == 0 {
		log.Println("❌ No clusters found")
		log.Println("\n💡 Run 'hyve cluster create <name>' to create a cluster")
		return
	}

	log.Printf("📦 Clusters (%d):\n", len(clusters))

	for _, cluster := range clusters {
		nameLabel := cluster.Metadata.Name
		if cluster.Spec.Pause {
			nameLabel += " [paused]"
		}
		log.Printf("  %s", nameLabel)
		log.Printf("    Provider: %s", cluster.Spec.Provider)
		log.Printf("    Region: %s", cluster.Metadata.Region)
		if len(cluster.Spec.NodeGroups) > 0 {
			log.Printf("    NodeGroups: %d", len(cluster.Spec.NodeGroups))
			for _, ng := range cluster.Spec.NodeGroups {
				log.Printf("      - %s: %s x%d", ng.Name, ng.InstanceType, ng.Count)
			}
		} else {
			log.Printf("    Nodes: %d (%s)", len(cluster.Spec.Nodes), strings.Join(cluster.Spec.Nodes, ", "))
		}
		if cluster.Spec.Ingress.Enabled {
			log.Printf("    Ingress: enabled")
		}
		if cluster.Spec.ExpiresAt != "" {
			log.Printf("    Expires At: %s", cluster.Spec.ExpiresAt)
		}
		log.Println()
	}

	log.Println("💡 Commands:")
	log.Println("  hyve cluster create <name>    # Create a new cluster")
	log.Println("  hyve cluster show <name>      # Show cluster definition")
	log.Println("  hyve cluster modify <name>    # Modify an existing cluster")
	log.Println("  hyve cluster delete <name>    # Delete a cluster")
	log.Println("  hyve reconcile                # Apply cluster changes to cloud")
}
