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
	Long:  "Commands to create, modify, or delete cluster configurations",
}

var createCmd = &cobra.Command{
	Use:   "create [cluster-name]",
	Short: "Create a new cluster",
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

		beforeCreate, _ := cmd.Flags().GetStringArray("before-create")
		onCreated, _ := cmd.Flags().GetStringArray("on-created")
		onDestroy, _ := cmd.Flags().GetStringArray("on-destroy")
		afterDelete, _ := cmd.Flags().GetStringArray("after-delete")

		pause, _ := cmd.Flags().GetBool("pause")
		expiresAt, _ := cmd.Flags().GetString("expires-at")

		createClusterFromCLI(clusterName, region, providerName, nodes, nodeGroups, clusterType, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName, beforeCreate, onCreated, onDestroy, afterDelete, pause, expiresAt)
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

var showCmd = &cobra.Command{
	Use:   "show [cluster-name]",
	Short: "Show cluster definition and summary",
	Long:  "Print the full cluster definition YAML and a summary of live fields. Reads from the repo definition; does not make cloud API calls.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		showCluster(args[0])
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

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cluster definitions",
	Long:  "Display all cluster definitions from the current repository",
	Run: func(cmd *cobra.Command, args []string) {
		listClusters()
	},
}

func init() {
	createCmd.Flags().StringP("region", "r", "PHX1", "Region for the cluster")
	createCmd.Flags().StringP("provider", "p", "", "Cloud provider (civo, aws, gcp, azure)")
	createCmd.MarkFlagRequired("provider")
	createCmd.Flags().StringSliceP("nodes", "n", []string{"g4s.kube.small"}, "Node sizes")
	createCmd.Flags().StringP("cluster-type", "t", "k3s", "Type of Kubernetes cluster")

	createCmd.Flags().StringP("account-name", "a", "", "AWS account name (required for AWS provider)")
	createCmd.Flags().String("project-name", "", "GCP project name (required for GCP provider)")
	createCmd.Flags().StringP("subscription-name", "s", "", "Azure subscription name (required for Azure provider)")
	createCmd.Flags().StringP("org-name", "o", "", "Civo organization name (required for Civo provider)")

	createCmd.Flags().StringP("vpc-name", "v", "", "AWS VPC name alias")
	createCmd.Flags().StringP("eks-role-name", "e", "", "IAM role name for the EKS control plane")
	createCmd.Flags().String("node-role-name", "", "IAM role name for EKS node groups")

	createCmd.Flags().StringArrayP("node-group", "g", nil, `Node group spec (repeatable): name=workers,type=t3.medium,count=3[,min=1,max=5,disk=50,spot=true,mode=System]`)

	createCmd.Flags().StringArray("before-create", nil, "Workflow name(s) to run before cluster creation (repeatable)")
	createCmd.Flags().StringArray("on-created", nil, "Workflow name(s) to run after cluster creation (repeatable)")
	createCmd.Flags().StringArray("on-destroy", nil, "Workflow name(s) to run before cluster destruction (repeatable)")
	createCmd.Flags().StringArray("after-delete", nil, "Workflow name(s) to run after cluster deletion (repeatable)")
	createCmd.Flags().Bool("pause", false, "Create the cluster in a paused state (reconciliation will be skipped)")
	createCmd.Flags().String("expires-at", "", "RFC 3339 timestamp after which the cluster is auto-deleted (e.g. 2026-05-01T00:00:00Z)")

	modifyCmd.Flags().StringP("region", "r", "", "Region for the cluster")
	modifyCmd.Flags().StringP("provider", "p", "", "Cloud provider")
	modifyCmd.Flags().StringSliceP("nodes", "n", nil, "Node sizes")
	modifyCmd.Flags().StringP("cluster-type", "t", "", "Type of Kubernetes cluster")
	modifyCmd.Flags().StringArrayP("node-group", "g", nil, `Node group spec (repeatable): name=workers,type=t3.medium,count=3[,min=1,max=5,disk=50,spot=true,mode=System]`)
	modifyCmd.Flags().Bool("pause", false, "Pause reconciliation for this cluster")
	modifyCmd.Flags().Bool("unpause", false, "Resume reconciliation for this cluster (clear the pause flag)")
	modifyCmd.Flags().String("expires-at", "", "RFC 3339 expiry timestamp (e.g. 2026-05-01T00:00:00Z); set to 'none' to clear")
	modifyCmd.Flags().StringArray("before-create", nil, "Workflows to run before cluster creation (replaces existing list)")
	modifyCmd.Flags().StringArray("after-delete", nil, "Workflows to run after cluster deletion (replaces existing list)")
	modifyCmd.Flags().String("eks-role-name", "", "IAM role name for the EKS control plane")
	modifyCmd.Flags().String("node-role-name", "", "IAM role name for EKS node groups")

	deleteCmd.Flags().Bool("force-cloud", false, "With --force: delete from cloud even if no configuration file exists")
	deleteCmd.Flags().Bool("force", false, "Delete cluster from cloud immediately before removing configuration (bypasses CI/CD)")

	forceDeleteCmd.Flags().StringP("region", "r", "", "Specific region to search (optional, will search common regions if not provided)")
	forceDeleteCmd.Flags().StringP("provider", "p", "civo", "Cloud provider (civo, aws, gcp, azure)")
	forceDeleteCmd.Flags().String("project-name", "", "Project/account name alias (required for GCP provider)")

	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(showCmd)
	Cmd.AddCommand(modifyCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(forceDeleteCmd)
}
