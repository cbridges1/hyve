package cluster

import (
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/types"
)

// Cmd is the cluster command.
var Cmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage clusters",
	Long:  "Commands to add, modify, or delete cluster configurations",
}

var addCmd = &cobra.Command{
	Use:   "add [cluster-name]",
	Short: "Add a new cluster",
	Long: `Create a new cluster configuration YAML file.

Supported cloud providers:
  - civo    Civo Cloud (K3s/Talos clusters)
  - aws     Amazon Web Services (EKS)
  - gcp     Google Cloud Platform (GKE)
  - azure   Microsoft Azure (AKS)

Use --account-name, --project-name, --subscription-name, or --org-name to specify the account.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]

		region, _ := cmd.Flags().GetString("region")
		providerName, _ := cmd.Flags().GetString("provider")
		nodes, _ := cmd.Flags().GetStringSlice("nodes")
		clusterType, _ := cmd.Flags().GetString("cluster-type")

		if strings.ToLower(providerName) != "civo" {
			if cmd.Flags().Changed("cluster-type") {
				log.Printf("⚠️  --cluster-type is only supported for the Civo provider and will be ignored for '%s'", providerName)
			}
			clusterType = ""
		}

		accountName, _ := cmd.Flags().GetString("account-name")
		projectName, _ := cmd.Flags().GetString("project-name")
		subscriptionName, _ := cmd.Flags().GetString("subscription-name")
		orgName, _ := cmd.Flags().GetString("org-name")

		vpcName, _ := cmd.Flags().GetString("vpc-name")
		eksRoleName, _ := cmd.Flags().GetString("eks-role-name")
		nodeRoleName, _ := cmd.Flags().GetString("node-role-name")

		if !shared.IsValidProvider(providerName) {
			log.Fatalf("Invalid provider '%s'. Valid providers are: %s", providerName, shared.ValidProvidersString())
		}

		providerName = strings.ToLower(providerName)

		switch providerName {
		case "aws":
			if accountName == "" {
				log.Fatalf("AWS provider requires --account-name flag. Use 'hyve config aws account list' to see available accounts.")
			}
			if vpcName == "" {
				log.Fatalf("AWS provider requires --vpc-name flag. Use 'hyve config aws vpc list --account %s' to see available VPCs.", accountName)
			}
			if eksRoleName == "" {
				log.Fatalf("AWS provider requires --eks-role-name flag. Use 'hyve config aws eks-role list --account %s' to see available roles.", accountName)
			}
			if nodeRoleName == "" {
				log.Fatalf("AWS provider requires --node-role-name flag. Use 'hyve config aws node-role list --account %s' to see available roles.", accountName)
			}
		case "gcp":
			if projectName == "" {
				log.Fatalf("GCP provider requires --project-name flag. Use 'hyve config gcp project list' to see available projects.")
			}
		case "azure":
			if subscriptionName == "" {
				log.Fatalf("Azure provider requires --subscription-name flag. Use 'hyve config azure subscription list' to see available subscriptions.")
			}
		case "civo":
			if orgName == "" {
				log.Fatalf("Civo provider requires --org-name flag. Use 'hyve config civo org list' to see available organizations.")
			}
		}

		nodeGroupStrs, _ := cmd.Flags().GetStringArray("node-group")
		var nodeGroups []types.NodeGroup
		for _, s := range nodeGroupStrs {
			ng, err := shared.ParseNodeGroup(s)
			if err != nil {
				log.Fatalf("Invalid --node-group value '%s': %v", s, err)
			}
			nodeGroups = append(nodeGroups, ng)
		}

		onCreated, _ := cmd.Flags().GetStringArray("on-created")
		onDestroy, _ := cmd.Flags().GetStringArray("on-destroy")

		addClusterFromCLI(clusterName, region, providerName, nodes, nodeGroups, clusterType, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName, onCreated, onDestroy)
	},
}

var modifyCmd = &cobra.Command{
	Use:   "modify [cluster-name]",
	Short: "Modify an existing cluster",
	Long: `Update an existing cluster configuration YAML file.

Supported cloud providers (if changing provider):
  - civo    Civo Cloud (K3s/Talos clusters)
  - aws     Amazon Web Services (EKS)
  - gcp     Google Cloud Platform (GKE)
  - azure   Microsoft Azure (AKS)`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]

		if cmd.Flags().Changed("provider") {
			providerName, _ := cmd.Flags().GetString("provider")
			if !shared.IsValidProvider(providerName) {
				log.Fatalf("Invalid provider '%s'. Valid providers are: %s", providerName, shared.ValidProvidersString())
			}
		}

		modifyClusterFromCLI(cmd, clusterName)
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete [cluster-name]",
	Short: "Delete a cluster",
	Long: `Delete a cluster by removing its YAML definition and reconciling.

Default behaviour:
  1. Remove the cluster configuration YAML file
  2. Commit and push the removal to the state repository
  3. Run reconciliation

Use --force to delete the cluster from the cloud provider immediately before
removing the configuration file. This is useful when you want to bypass CI/CD
and destroy the cluster right now.

Use --force-cloud together with --force to delete from cloud even if no
configuration file exists.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		forceCloud, _ := cmd.Flags().GetBool("force-cloud")
		force, _ := cmd.Flags().GetBool("force")
		deleteClusterFromCLI(clusterName, forceCloud, force)
	},
}

var forceDeleteCmd = &cobra.Command{
	Use:   "force-delete [cluster-name]",
	Short: "Force delete a cluster by name from cloud provider",
	Long: `Force delete a cluster by name directly from the cloud provider without requiring a configuration file.
This command will search all regions for the cluster and delete it if found.
Useful when configuration files are lost or corrupted.

Note: This command does not remove configuration files or run reconciliation.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		region, _ := cmd.Flags().GetString("region")
		providerName, _ := cmd.Flags().GetString("provider")
		projectName, _ := cmd.Flags().GetString("project-name")

		if providerName == "" {
			providerName = "civo"
		}

		if !shared.IsValidProvider(providerName) {
			log.Fatalf("Invalid provider '%s'. Valid providers are: %s", providerName, shared.ValidProvidersString())
		}

		if providerName == "gcp" && projectName == "" {
			log.Fatalf("GCP provider requires --project-name flag. Use 'hyve config gcp project list' to see available projects.")
		}

		forceDeleteClusterFromCloud(clusterName, region, providerName, projectName)
	},
}

var importCmd = &cobra.Command{
	Use:   "import <name>",
	Short: "Import an existing cloud cluster into hyve",
	Long:  "Record an already-running cluster in the hyve repository without provisioning it.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		region, _ := cmd.Flags().GetString("region")
		providerName, _ := cmd.Flags().GetString("provider")
		nodes, _ := cmd.Flags().GetStringSlice("nodes")
		accountName, _ := cmd.Flags().GetString("account-name")
		projectName, _ := cmd.Flags().GetString("project-name")
		subscriptionName, _ := cmd.Flags().GetString("subscription-name")
		orgName, _ := cmd.Flags().GetString("org-name")
		vpcName, _ := cmd.Flags().GetString("vpc-name")
		eksRoleName, _ := cmd.Flags().GetString("eks-role-name")
		nodeRoleName, _ := cmd.Flags().GetString("node-role-name")
		importClusterFromCLI(clusterName, region, providerName, nodes, []types.NodeGroup{}, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName)
	},
}

var releaseCmd = &cobra.Command{
	Use:   "release <name>",
	Short: "Release a cluster from hyve management without deleting it from the cloud",
	Long:  "Remove the cluster definition from the hyve repository. The cloud cluster is left running.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		releaseClusterFromCLI(args[0])
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cluster definitions",
	Long:  "Display all cluster definitions from the current repository",
	Run: func(cmd *cobra.Command, args []string) {
		listClusters()
	},
}

func init() {
	addCmd.Flags().StringP("region", "r", "PHX1", "Region for the cluster")
	addCmd.Flags().StringP("provider", "p", "", "Cloud provider (civo, aws, gcp, azure)")
	addCmd.MarkFlagRequired("provider")
	addCmd.Flags().StringSliceP("nodes", "n", []string{"g4s.kube.small"}, "Node sizes")
	addCmd.Flags().StringP("cluster-type", "t", "k3s", "Type of Kubernetes cluster")

	addCmd.Flags().StringP("account-name", "a", "", "AWS account name (required for AWS provider)")
	addCmd.Flags().String("project-name", "", "GCP project name (required for GCP provider)")
	addCmd.Flags().StringP("subscription-name", "s", "", "Azure subscription name (required for Azure provider)")
	addCmd.Flags().StringP("org-name", "o", "", "Civo organization name (required for Civo provider)")

	addCmd.Flags().StringP("vpc-name", "v", "", "AWS VPC name alias (required for AWS provider)")
	addCmd.Flags().StringP("eks-role-name", "e", "", "AWS EKS IAM role name alias (required for AWS provider)")
	addCmd.Flags().String("node-role-name", "", "AWS EKS node IAM role name alias (required for AWS provider)")

	addCmd.Flags().StringArrayP("node-group", "g", nil, `Node group spec (repeatable): name=workers,type=t3.medium,count=3[,min=1,max=5,disk=50,spot=true,mode=System]`)

	addCmd.Flags().StringArray("on-created", nil, "Workflow name(s) to run after cluster creation (repeatable)")
	addCmd.Flags().StringArray("on-destroy", nil, "Workflow name(s) to run before cluster destruction (repeatable)")

	modifyCmd.Flags().StringP("region", "r", "", "Region for the cluster")
	modifyCmd.Flags().StringP("provider", "p", "", "Cloud provider")
	modifyCmd.Flags().StringSliceP("nodes", "n", nil, "Node sizes")
	modifyCmd.Flags().StringP("cluster-type", "t", "", "Type of Kubernetes cluster")
	modifyCmd.Flags().StringArrayP("node-group", "g", nil, `Node group spec (repeatable): name=workers,type=t3.medium,count=3[,min=1,max=5,disk=50,spot=true,mode=System]`)

	deleteCmd.Flags().Bool("force-cloud", false, "With --force: delete from cloud even if no configuration file exists")
	deleteCmd.Flags().Bool("force", false, "Delete cluster from cloud immediately before removing configuration (bypasses CI/CD)")

	forceDeleteCmd.Flags().StringP("region", "r", "", "Specific region to search (optional, will search common regions if not provided)")
	forceDeleteCmd.Flags().StringP("provider", "p", "civo", "Cloud provider (civo, aws, gcp, azure)")
	forceDeleteCmd.Flags().String("project-name", "", "Project/account name alias (required for GCP provider)")

	importCmd.Flags().StringP("region", "r", "", "Region where the cluster is running")
	importCmd.Flags().StringP("provider", "p", "", "Cloud provider (civo, aws, gcp, azure)")
	importCmd.MarkFlagRequired("provider")
	importCmd.Flags().StringSliceP("nodes", "n", nil, "Node sizes")
	importCmd.Flags().StringP("account-name", "a", "", "AWS account name alias")
	importCmd.Flags().String("project-name", "", "GCP project name alias")
	importCmd.Flags().StringP("subscription-name", "s", "", "Azure subscription name alias")
	importCmd.Flags().StringP("org-name", "o", "", "Civo organization name alias")
	importCmd.Flags().StringP("vpc-name", "v", "", "AWS VPC name alias")
	importCmd.Flags().StringP("eks-role-name", "e", "", "AWS EKS IAM role name alias")
	importCmd.Flags().String("node-role-name", "", "AWS EKS node IAM role name alias")

	Cmd.AddCommand(addCmd)
	Cmd.AddCommand(importCmd)
	Cmd.AddCommand(releaseCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(modifyCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(forceDeleteCmd)
}
