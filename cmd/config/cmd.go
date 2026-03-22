package config

import (
	gocontext "context"
	"fmt"
	"log"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"hyve/cmd/shared"
	"hyve/internal/credentials"
	"hyve/internal/provider/aws"
	azureprovider "hyve/internal/provider/azure"
	"hyve/internal/providerconfig"
)

// Cmd is the root config command exposed to the parent.
var Cmd = configCmd

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Hyve configuration",
	Long:  "Commands to manage API tokens and other configuration settings",
}

// GCP provider config commands
var configGCPCmd = &cobra.Command{
	Use:   "gcp",
	Short: "Manage GCP provider configuration",
	Long: `Manage GCP-specific configuration stored in the current repository.

These configurations are stored in the repository under provider-configs/gcp.yaml
and are committed to Git for team sharing.`,
}

var configGCPProjectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage GCP projects",
}

var configGCPAddProjectCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a GCP project with an alias to the repository configuration",
	Long: `Add a GCP project ID with a friendly name/alias to the repository's provider configuration.

The project is stored in provider-configs/gcp.yaml in the current repository.
The name can then be used as an alias when creating clusters.

Examples:
  hyve config gcp project add --name dev --id my-dev-project-123
  hyve config gcp project add --name prod --id my-prod-project-456

Then use with cluster create:
  hyve cluster add my-cluster --provider gcp --gcp-project dev --region us-central1`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		projectID, _ := cmd.Flags().GetString("id")
		addGCPProject(name, projectID)
	},
}

var configGCPRemoveProjectCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a GCP project from the repository configuration",
	Long: `Remove a GCP project by its alias/name from the repository's provider configuration.

Examples:
  hyve config gcp project remove dev
  hyve config gcp project remove prod`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeGCPProject(args[0])
	},
}

var configGCPListProjectsCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured GCP projects",
	Long:  "Display all GCP projects configured in the current repository with their aliases.",
	Run: func(cmd *cobra.Command, args []string) {
		listGCPProjects()
	},
}

var configGCPGetProjectCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get the project ID for a GCP project alias",
	Long: `Display the GCP project ID associated with a given alias/name.

Examples:
  hyve config gcp project get dev`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		getGCPProject(args[0])
	},
}

// AWS provider config commands
var configAWSCmd = &cobra.Command{
	Use:   "aws",
	Short: "Manage AWS provider configuration",
	Long: `Manage AWS-specific configuration stored in the current repository.

These configurations are stored in the repository under provider-configs/aws.yaml
and are committed to Git for team sharing.`,
}

var configAWSAccountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage AWS accounts",
}

var configAWSAccountAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an AWS account to the repository configuration",
	Long: `Add an AWS account with a friendly name/alias to the repository's provider configuration.

The account is stored in provider-configs/aws.yaml in the current repository.
The name can then be used as an alias when creating EKS clusters.

Examples:
  hyve config aws account add --name prod --id 123456789012
  hyve config aws account add --name dev --id 987654321098`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		accountID, _ := cmd.Flags().GetString("id")
		addAWSAccount(name, accountID)
	},
}

var configAWSAccountRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove an AWS account from the repository configuration",
	Long: `Remove an AWS account by its alias/name from the repository's provider configuration.

Examples:
  hyve config aws account remove prod`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeAWSAccount(args[0])
	},
}

var configAWSAccountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured AWS accounts",
	Long:  "Display all AWS accounts configured in the current repository with their aliases.",
	Run: func(cmd *cobra.Command, args []string) {
		listAWSAccounts()
	},
}

var configAWSAccountGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get the account ID for an AWS account alias",
	Long: `Display the AWS account ID associated with a given alias/name.

Examples:
  hyve config aws account get prod`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		getAWSAccount(args[0])
	},
}

// AWS EKS Role group and leaf commands
var configAWSEKSRoleCmd = &cobra.Command{
	Use:   "eks-role",
	Short: "Manage AWS EKS IAM roles",
}

var configAWSEKSRoleAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an EKS IAM role to the repository configuration",
	Long: `Add an EKS IAM role with a friendly name/alias to the repository's provider configuration.

The role is stored in provider-configs/aws.yaml in the current repository.
The name can then be used as an alias when creating EKS clusters.

Examples:
  hyve config aws eks-role add --account prod --name default-role --role-arn arn:aws:iam::123456789012:role/my-eks-cluster-role
  hyve config aws eks-role add --name prod-role --role-arn arn:aws:iam::123456789012:role/prod-eks-role`,
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		name, _ := cmd.Flags().GetString("name")
		roleARN, _ := cmd.Flags().GetString("role-arn")
		addAWSEKSRole(account, name, roleARN)
	},
}

var configAWSEKSRoleRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove an EKS IAM role from the repository configuration",
	Long: `Remove an EKS IAM role by its alias/name from the repository's provider configuration.

Examples:
  hyve config aws eks-role remove --account prod default-role
  hyve config aws eks-role remove default-role`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		removeAWSEKSRole(account, args[0])
	},
}

var configAWSEKSRoleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured EKS IAM roles",
	Long:  "Display all EKS IAM roles configured in the current repository with their aliases.",
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		listAWSEKSRoles(account)
	},
}

var configAWSEKSRoleGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get the role ARN for an EKS IAM role alias",
	Long: `Display the EKS IAM role ARN associated with a given alias/name.

Examples:
  hyve config aws eks-role get --account prod default-role
  hyve config aws eks-role get default-role`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		getAWSEKSRole(account, args[0])
	},
}

// AWS EKS Role create/delete commands (actual AWS operations)
var configAWSEKSRoleCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an EKS IAM role in AWS",
	Long: `Create an IAM role for EKS clusters in AWS and store the alias in the repository configuration.

This command creates an actual IAM role in AWS with the EKS assume role policy and
attaches the AmazonEKSClusterPolicy. The role ARN is then stored with the given alias.

Requires AWS credentials configured via 'aws configure' or environment variables.

Examples:
  hyve config aws eks-role create --account prod --name default-role --role-name my-eks-cluster-role --region us-east-1
  hyve config aws eks-role create --name prod-role --role-name prod-eks-role --region us-west-2`,
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		name, _ := cmd.Flags().GetString("name")
		roleName, _ := cmd.Flags().GetString("role-name")
		region, _ := cmd.Flags().GetString("region")
		createAWSEKSRole(account, name, roleName, region)
	},
}

var configAWSEKSRoleDeleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete an EKS IAM role from AWS",
	Long: `Delete an EKS IAM role from AWS and remove it from the repository configuration.

This command deletes the actual IAM role from AWS (detaching all policies first),
then removes the alias from the repository configuration.

Use --config-only to remove only the configuration without deleting the AWS role.

Examples:
  hyve config aws eks-role delete --account prod default-role
  hyve config aws eks-role delete default-role --region us-east-1
  hyve config aws eks-role delete default-role --config-only`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		region, _ := cmd.Flags().GetString("region")
		configOnly, _ := cmd.Flags().GetBool("config-only")
		deleteAWSEKSRole(account, args[0], region, configOnly)
	},
}

// AWS Node Role group and leaf commands
var configAWSNodeRoleCmd = &cobra.Command{
	Use:   "node-role",
	Short: "Manage AWS EKS node IAM roles",
}

var configAWSNodeRoleAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an EKS node IAM role to the repository configuration",
	Long: `Add an EKS node IAM role with a friendly name/alias to the repository's provider configuration.

The role is stored in provider-configs/aws.yaml in the current repository.
The name can then be used as an alias when creating EKS clusters.

Examples:
  hyve config aws node-role add --account prod --name default-node-role --role-arn arn:aws:iam::123456789012:role/my-eks-node-role
  hyve config aws node-role add --name prod-node-role --role-arn arn:aws:iam::123456789012:role/prod-eks-node-role`,
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		name, _ := cmd.Flags().GetString("name")
		roleARN, _ := cmd.Flags().GetString("role-arn")
		addAWSNodeRole(account, name, roleARN)
	},
}

var configAWSNodeRoleRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove an EKS node IAM role from the repository configuration",
	Long: `Remove an EKS node IAM role by its alias/name from the repository's provider configuration.

Examples:
  hyve config aws node-role remove --account prod default-node-role
  hyve config aws node-role remove default-node-role`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		removeAWSNodeRole(account, args[0])
	},
}

var configAWSNodeRoleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured EKS node IAM roles",
	Long:  "Display all EKS node IAM roles configured in the current repository with their aliases.",
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		listAWSNodeRoles(account)
	},
}

var configAWSNodeRoleGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get the role ARN for an EKS node IAM role alias",
	Long: `Display the EKS node IAM role ARN associated with a given alias/name.

Examples:
  hyve config aws node-role get --account prod default-node-role
  hyve config aws node-role get default-node-role`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		getAWSNodeRole(account, args[0])
	},
}

// AWS Node Role create/delete commands (actual AWS operations)
var configAWSNodeRoleCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an EKS node IAM role in AWS",
	Long: `Create an IAM role for EKS worker nodes in AWS and store the alias in the repository configuration.

This command creates an actual IAM role in AWS with the EC2 assume role policy and
attaches the required EKS node policies (AmazonEKSWorkerNodePolicy, AmazonEKS_CNI_Policy,
AmazonEC2ContainerRegistryReadOnly). The role ARN is then stored with the given alias.

Requires AWS credentials configured via 'aws configure' or environment variables.

Examples:
  hyve config aws node-role create --account prod --name default-node-role --role-name my-eks-node-role --region us-east-1
  hyve config aws node-role create --name prod-node-role --role-name prod-eks-node-role --region us-west-2`,
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		name, _ := cmd.Flags().GetString("name")
		roleName, _ := cmd.Flags().GetString("role-name")
		region, _ := cmd.Flags().GetString("region")
		createAWSNodeRole(account, name, roleName, region)
	},
}

var configAWSNodeRoleDeleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete an EKS node IAM role from AWS",
	Long: `Delete an EKS node IAM role from AWS and remove it from the repository configuration.

This command deletes the actual IAM role from AWS (detaching all policies first),
then removes the alias from the repository configuration.

Use --config-only to remove only the configuration without deleting the AWS role.

Examples:
  hyve config aws node-role delete --account prod default-node-role
  hyve config aws node-role delete default-node-role --region us-east-1
  hyve config aws node-role delete default-node-role --config-only`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		region, _ := cmd.Flags().GetString("region")
		configOnly, _ := cmd.Flags().GetBool("config-only")
		deleteAWSNodeRole(account, args[0], region, configOnly)
	},
}

// AWS VPC group and leaf commands
var configAWSVPCCmd = &cobra.Command{
	Use:   "vpc",
	Short: "Manage AWS VPCs",
}

var configAWSVPCAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a VPC to the repository configuration",
	Long: `Add a VPC with a friendly name/alias to the repository's provider configuration.

The VPC is stored in provider-configs/aws.yaml in the current repository.
The name can then be used as an alias when creating EKS clusters.

Examples:
  hyve config aws vpc add --account prod --name default-vpc --id vpc-0123456789abcdef0
  hyve config aws vpc add --name prod-vpc --id vpc-abcdef0123456789`,
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		name, _ := cmd.Flags().GetString("name")
		vpcID, _ := cmd.Flags().GetString("id")
		addAWSVPC(account, name, vpcID)
	},
}

var configAWSVPCRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a VPC from the repository configuration",
	Long: `Remove a VPC by its alias/name from the repository's provider configuration.

Examples:
  hyve config aws vpc remove --account prod default-vpc
  hyve config aws vpc remove default-vpc`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		removeAWSVPC(account, args[0])
	},
}

var configAWSVPCListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured VPCs",
	Long:  "Display all VPCs configured in the current repository with their aliases.",
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		listAWSVPCs(account)
	},
}

var configAWSVPCGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get the VPC ID for a VPC alias",
	Long: `Display the VPC ID associated with a given alias/name.

Examples:
  hyve config aws vpc get --account prod default-vpc
  hyve config aws vpc get default-vpc`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		getAWSVPC(account, args[0])
	},
}

// AWS VPC create/delete commands (actual AWS operations)
var configAWSVPCCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a VPC in AWS",
	Long: `Create a VPC in AWS and store the alias in the repository configuration.

This command creates an actual VPC in AWS with optional subnets and DNS settings.
The VPC ID is then stored with the given alias.

Requires AWS credentials configured via 'aws configure' or environment variables.

Examples:
  hyve config aws vpc create --account prod --name default-vpc --region us-east-1
  hyve config aws vpc create --name prod-vpc --region us-west-2 --cidr 10.1.0.0/16
  hyve config aws vpc create --name dev-vpc --region us-east-1 --cidr 10.0.0.0/16 --subnets 10.0.1.0/24,10.0.2.0/24`,
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		name, _ := cmd.Flags().GetString("name")
		region, _ := cmd.Flags().GetString("region")
		cidr, _ := cmd.Flags().GetString("cidr")
		subnets, _ := cmd.Flags().GetString("subnets")
		enableDNS, _ := cmd.Flags().GetBool("enable-dns")
		createAWSVPC(account, name, region, cidr, subnets, enableDNS)
	},
}

var configAWSVPCDeleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete a VPC from AWS",
	Long: `Delete a VPC from AWS and remove it from the repository configuration.

This command deletes the actual VPC from AWS (including subnets and internet gateways),
then removes the alias from the repository configuration.

Use --config-only to remove only the configuration without deleting the AWS VPC.

Examples:
  hyve config aws vpc delete --account prod default-vpc
  hyve config aws vpc delete default-vpc --region us-east-1
  hyve config aws vpc delete default-vpc --config-only`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		account, _ := cmd.Flags().GetString("account")
		region, _ := cmd.Flags().GetString("region")
		configOnly, _ := cmd.Flags().GetBool("config-only")
		deleteAWSVPC(account, args[0], region, configOnly)
	},
}

// Azure provider config commands
var configAzureCmd = &cobra.Command{
	Use:   "azure",
	Short: "Manage Azure provider configuration",
	Long: `Manage Azure-specific configuration stored in the current repository.

These configurations are stored in the repository under provider-configs/azure.yaml
and are committed to Git for team sharing.`,
}

var configAzureSubscriptionCmd = &cobra.Command{
	Use:   "subscription",
	Short: "Manage Azure subscriptions",
}

var configAzureAddSubscriptionIDsCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an Azure subscription to the repository configuration",
	Long: `Add an Azure subscription with a friendly name to the repository's provider configuration.

The subscription is stored in provider-configs/azure.yaml in the current repository.
The name can then be used as an alias when creating clusters.

Examples:
  hyve config azure subscription add --name prod --id 12345678-1234-1234-1234-123456789012
  hyve config azure subscription add --name dev --id 87654321-4321-4321-4321-210987654321`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		id, _ := cmd.Flags().GetString("id")
		addAzureSubscriptionIDs(name, id)
	},
}

var configAzureRemoveSubscriptionIDsCmd = &cobra.Command{
	Use:   "remove [subscription-id,...]",
	Short: "Remove Azure subscription IDs from the repository configuration",
	Long: `Remove one or more Azure subscription IDs from the repository's provider configuration.

Examples:
  hyve config azure subscription remove 12345678-1234-1234-1234-123456789012`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeAzureSubscriptionIDs(args)
	},
}

var configAzureListSubscriptionIDsCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured Azure subscription IDs",
	Long:  "Display all Azure subscription IDs configured in the current repository.",
	Run: func(cmd *cobra.Command, args []string) {
		listAzureSubscriptionIDs()
	},
}

var configAzureResourceGroupCmd = &cobra.Command{
	Use:   "resource-group",
	Short: "Manage Azure resource groups",
}

var configAzureAddResourceGroupCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a resource group to an Azure subscription",
	Long: `Add a resource group to an Azure subscription in the repository's provider configuration.

The resource group is stored under the subscription in provider-configs/azure.yaml.

Examples:
  hyve config azure resource-group add --subscription prod --name my-rg --location eastus
  hyve config azure resource-group add --subscription dev --name dev-rg --location westus2`,
	Run: func(cmd *cobra.Command, args []string) {
		subscription, _ := cmd.Flags().GetString("subscription")
		name, _ := cmd.Flags().GetString("name")
		location, _ := cmd.Flags().GetString("location")
		addAzureResourceGroup(subscription, name, location)
	},
}

var configAzureListResourceGroupsCmd = &cobra.Command{
	Use:   "list",
	Short: "List resource groups for an Azure subscription",
	Long: `Display all resource groups configured under an Azure subscription.

Examples:
  hyve config azure resource-group list --subscription prod`,
	Run: func(cmd *cobra.Command, args []string) {
		subscription, _ := cmd.Flags().GetString("subscription")
		listAzureResourceGroups(subscription)
	},
}

var configAzureDeleteResourceGroupCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove a resource group from an Azure subscription",
	Long: `Remove a resource group from an Azure subscription in the repository's provider configuration.

Examples:
  hyve config azure resource-group delete --subscription prod --name my-rg`,
	Run: func(cmd *cobra.Command, args []string) {
		subscription, _ := cmd.Flags().GetString("subscription")
		name, _ := cmd.Flags().GetString("name")
		deleteAzureResourceGroup(subscription, name)
	},
}

// Civo provider config commands
var configCivoCmd = &cobra.Command{
	Use:   "civo",
	Short: "Manage Civo provider configuration",
	Long: `Manage Civo-specific configuration stored in the current repository.

These configurations are stored in the repository under provider-configs/civo.yaml
and are committed to Git for team sharing.`,
}

var configCivoOrgCmd = &cobra.Command{
	Use:   "org",
	Short: "Manage Civo organizations",
}

var configCivoOrgAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a Civo organization to the repository configuration",
	Long: `Add a Civo organization with a friendly name/alias to the repository's provider configuration.

The organization is stored in provider-configs/civo.yaml in the current repository.
The name can then be used as an alias when creating clusters.

Examples:
  hyve config civo org add --name prod --id org-abc123
  hyve config civo org add --name dev --id org-def456`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		orgID, _ := cmd.Flags().GetString("id")
		addCivoOrganization(name, orgID)
	},
}

var configCivoOrgRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a Civo organization from the repository configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeCivoOrganization(args[0])
	},
}

var configCivoOrgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured Civo organizations",
	Run: func(cmd *cobra.Command, args []string) {
		listCivoOrganizations()
	},
}

var configCivoOrgGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get the organization ID for a Civo organization alias",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		getCivoOrganization(args[0])
	},
}

var configCivoTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage Civo API tokens",
}

var configCivoSetTokenCmd = &cobra.Command{
	Use:   "set",
	Short: "Store a Civo API token for an organization",
	Long: `Store an encrypted Civo API token in the local database.

Examples:
  hyve config civo token set --org my-org
  hyve config civo token set --org my-org --token YOUR_TOKEN_HERE`,
	Run: func(cmd *cobra.Command, args []string) {
		org, _ := cmd.Flags().GetString("org")
		tokenFlag, _ := cmd.Flags().GetString("token")
		setCivoToken(org, tokenFlag)
	},
}

var configCivoGetTokenCmd = &cobra.Command{
	Use:   "get",
	Short: "Retrieve the stored Civo API token for an organization",
	Long: `Display the decrypted Civo API token for an organization.

Examples:
  hyve config civo token get --org my-org`,
	Run: func(cmd *cobra.Command, args []string) {
		org, _ := cmd.Flags().GetString("org")
		getCivoToken(org)
	},
}

var configCivoClearTokenCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove the stored Civo API token for an organization",
	Long: `Delete the Civo API token for an organization.

Examples:
  hyve config civo token clear --org my-org`,
	Run: func(cmd *cobra.Command, args []string) {
		org, _ := cmd.Flags().GetString("org")
		clearCivoToken(org)
	},
}

func init() {

	// GCP subcommands
	configGCPAddProjectCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the project (required)")
	configGCPAddProjectCmd.Flags().StringP("id", "i", "", "GCP project ID (required)")
	configGCPAddProjectCmd.MarkFlagRequired("name")
	configGCPAddProjectCmd.MarkFlagRequired("id")

	configGCPProjectCmd.AddCommand(configGCPAddProjectCmd, configGCPRemoveProjectCmd, configGCPListProjectsCmd, configGCPGetProjectCmd)
	configGCPCmd.AddCommand(configGCPProjectCmd)

	// AWS Account subcommands
	configAWSAccountAddCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the account (required)")
	configAWSAccountAddCmd.Flags().StringP("id", "i", "", "AWS account ID (required)")
	configAWSAccountAddCmd.MarkFlagRequired("name")
	configAWSAccountAddCmd.MarkFlagRequired("id")

	configAWSAccountCmd.AddCommand(configAWSAccountAddCmd, configAWSAccountRemoveCmd, configAWSAccountListCmd, configAWSAccountGetCmd)
	configAWSCmd.AddCommand(configAWSAccountCmd)

	// AWS EKS Role subcommands
	configAWSEKSRoleAddCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSEKSRoleAddCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the EKS role (required)")
	configAWSEKSRoleAddCmd.Flags().StringP("role-arn", "r", "", "IAM role ARN for EKS (required)")
	configAWSEKSRoleAddCmd.MarkFlagRequired("account")
	configAWSEKSRoleAddCmd.MarkFlagRequired("name")
	configAWSEKSRoleAddCmd.MarkFlagRequired("role-arn")

	configAWSEKSRoleRemoveCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSEKSRoleRemoveCmd.MarkFlagRequired("account")

	configAWSEKSRoleListCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSEKSRoleListCmd.MarkFlagRequired("account")

	configAWSEKSRoleGetCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSEKSRoleGetCmd.MarkFlagRequired("account")

	configAWSEKSRoleCreateCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSEKSRoleCreateCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the EKS role (required)")
	configAWSEKSRoleCreateCmd.Flags().String("role-name", "", "IAM role name to create in AWS (required)")
	configAWSEKSRoleCreateCmd.Flags().StringP("region", "r", "us-east-1", "AWS region")
	configAWSEKSRoleCreateCmd.MarkFlagRequired("account")
	configAWSEKSRoleCreateCmd.MarkFlagRequired("name")
	configAWSEKSRoleCreateCmd.MarkFlagRequired("role-name")

	configAWSEKSRoleDeleteCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSEKSRoleDeleteCmd.MarkFlagRequired("account")
	configAWSEKSRoleDeleteCmd.Flags().StringP("region", "r", "us-east-1", "AWS region")
	configAWSEKSRoleDeleteCmd.Flags().Bool("config-only", false, "Only remove from configuration, don't delete from AWS")

	configAWSEKSRoleCmd.AddCommand(configAWSEKSRoleAddCmd, configAWSEKSRoleRemoveCmd, configAWSEKSRoleListCmd, configAWSEKSRoleGetCmd, configAWSEKSRoleCreateCmd, configAWSEKSRoleDeleteCmd)
	configAWSCmd.AddCommand(configAWSEKSRoleCmd)

	// AWS Node Role subcommands
	configAWSNodeRoleAddCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSNodeRoleAddCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the node role (required)")
	configAWSNodeRoleAddCmd.Flags().StringP("role-arn", "r", "", "IAM role ARN for EKS nodes (required)")
	configAWSNodeRoleAddCmd.MarkFlagRequired("account")
	configAWSNodeRoleAddCmd.MarkFlagRequired("name")
	configAWSNodeRoleAddCmd.MarkFlagRequired("role-arn")

	configAWSNodeRoleRemoveCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSNodeRoleRemoveCmd.MarkFlagRequired("account")

	configAWSNodeRoleListCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSNodeRoleListCmd.MarkFlagRequired("account")

	configAWSNodeRoleGetCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSNodeRoleGetCmd.MarkFlagRequired("account")

	configAWSNodeRoleCreateCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSNodeRoleCreateCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the node role (required)")
	configAWSNodeRoleCreateCmd.Flags().String("role-name", "", "IAM role name to create in AWS (required)")
	configAWSNodeRoleCreateCmd.Flags().StringP("region", "r", "us-east-1", "AWS region")
	configAWSNodeRoleCreateCmd.MarkFlagRequired("account")
	configAWSNodeRoleCreateCmd.MarkFlagRequired("name")
	configAWSNodeRoleCreateCmd.MarkFlagRequired("role-name")

	configAWSNodeRoleDeleteCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSNodeRoleDeleteCmd.MarkFlagRequired("account")
	configAWSNodeRoleDeleteCmd.Flags().StringP("region", "r", "us-east-1", "AWS region")
	configAWSNodeRoleDeleteCmd.Flags().Bool("config-only", false, "Only remove from configuration, don't delete from AWS")

	configAWSNodeRoleCmd.AddCommand(configAWSNodeRoleAddCmd, configAWSNodeRoleRemoveCmd, configAWSNodeRoleListCmd, configAWSNodeRoleGetCmd, configAWSNodeRoleCreateCmd, configAWSNodeRoleDeleteCmd)
	configAWSCmd.AddCommand(configAWSNodeRoleCmd)

	// AWS VPC subcommands
	configAWSVPCAddCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSVPCAddCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the VPC (required)")
	configAWSVPCAddCmd.Flags().StringP("id", "i", "", "VPC ID (required)")
	configAWSVPCAddCmd.MarkFlagRequired("account")
	configAWSVPCAddCmd.MarkFlagRequired("name")
	configAWSVPCAddCmd.MarkFlagRequired("id")

	configAWSVPCRemoveCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSVPCRemoveCmd.MarkFlagRequired("account")

	configAWSVPCListCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSVPCListCmd.MarkFlagRequired("account")

	configAWSVPCGetCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSVPCGetCmd.MarkFlagRequired("account")

	configAWSVPCCreateCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSVPCCreateCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the VPC (required)")
	configAWSVPCCreateCmd.Flags().StringP("region", "r", "us-east-1", "AWS region")
	configAWSVPCCreateCmd.Flags().StringP("cidr", "c", "10.0.0.0/16", "CIDR block for the VPC")
	configAWSVPCCreateCmd.Flags().StringP("subnets", "s", "", "Comma-separated subnet CIDRs to create (e.g., 10.0.1.0/24,10.0.2.0/24)")
	configAWSVPCCreateCmd.Flags().Bool("enable-dns", true, "Enable DNS support and hostnames")
	configAWSVPCCreateCmd.MarkFlagRequired("account")
	configAWSVPCCreateCmd.MarkFlagRequired("name")

	configAWSVPCDeleteCmd.Flags().StringP("account", "a", "", "AWS account name (required)")
	configAWSVPCDeleteCmd.MarkFlagRequired("account")
	configAWSVPCDeleteCmd.Flags().StringP("region", "r", "us-east-1", "AWS region")
	configAWSVPCDeleteCmd.Flags().Bool("config-only", false, "Only remove from configuration, don't delete from AWS")

	configAWSVPCCmd.AddCommand(configAWSVPCAddCmd, configAWSVPCRemoveCmd, configAWSVPCListCmd, configAWSVPCGetCmd, configAWSVPCCreateCmd, configAWSVPCDeleteCmd)
	configAWSCmd.AddCommand(configAWSVPCCmd)

	// Azure subcommands
	configAzureAddSubscriptionIDsCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the subscription (required)")
	configAzureAddSubscriptionIDsCmd.Flags().StringP("id", "i", "", "Azure subscription ID (required)")
	configAzureAddSubscriptionIDsCmd.MarkFlagRequired("name")
	configAzureAddSubscriptionIDsCmd.MarkFlagRequired("id")

	configAzureSubscriptionCmd.AddCommand(configAzureAddSubscriptionIDsCmd, configAzureRemoveSubscriptionIDsCmd, configAzureListSubscriptionIDsCmd)

	configAzureAddResourceGroupCmd.Flags().StringP("subscription", "s", "", "Subscription name to add the resource group to (required)")
	configAzureAddResourceGroupCmd.Flags().StringP("name", "n", "", "Resource group name (required)")
	configAzureAddResourceGroupCmd.Flags().StringP("location", "l", "", "Azure region/location for the resource group (required)")
	configAzureAddResourceGroupCmd.MarkFlagRequired("subscription")
	configAzureAddResourceGroupCmd.MarkFlagRequired("name")
	configAzureAddResourceGroupCmd.MarkFlagRequired("location")

	configAzureListResourceGroupsCmd.Flags().StringP("subscription", "s", "", "Subscription name to list resource groups for (required)")
	configAzureListResourceGroupsCmd.MarkFlagRequired("subscription")

	configAzureDeleteResourceGroupCmd.Flags().StringP("subscription", "s", "", "Subscription name to remove the resource group from (required)")
	configAzureDeleteResourceGroupCmd.Flags().StringP("name", "n", "", "Resource group name to remove (required)")
	configAzureDeleteResourceGroupCmd.MarkFlagRequired("subscription")
	configAzureDeleteResourceGroupCmd.MarkFlagRequired("name")

	configAzureResourceGroupCmd.AddCommand(configAzureAddResourceGroupCmd, configAzureListResourceGroupsCmd, configAzureDeleteResourceGroupCmd)

	configAzureCmd.AddCommand(configAzureSubscriptionCmd, configAzureResourceGroupCmd)

	// Civo subcommands
	configCivoOrgAddCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the organization (required)")
	configCivoOrgAddCmd.Flags().StringP("id", "i", "", "Civo organization ID (required)")
	configCivoOrgAddCmd.MarkFlagRequired("name")
	configCivoOrgAddCmd.MarkFlagRequired("id")

	configCivoOrgCmd.AddCommand(configCivoOrgAddCmd, configCivoOrgRemoveCmd, configCivoOrgListCmd, configCivoOrgGetCmd)
	configCivoCmd.AddCommand(configCivoOrgCmd)

	configCivoSetTokenCmd.Flags().StringP("org", "o", "", "Civo organization name (required)")
	configCivoSetTokenCmd.Flags().StringP("token", "t", "", "API token (if not provided, will prompt securely)")
	configCivoSetTokenCmd.MarkFlagRequired("org")

	configCivoGetTokenCmd.Flags().StringP("org", "o", "", "Civo organization name (required)")
	configCivoGetTokenCmd.MarkFlagRequired("org")

	configCivoClearTokenCmd.Flags().StringP("org", "o", "", "Civo organization name (required)")
	configCivoClearTokenCmd.MarkFlagRequired("org")

	configCivoTokenCmd.AddCommand(configCivoSetTokenCmd, configCivoGetTokenCmd, configCivoClearTokenCmd)
	configCivoCmd.AddCommand(configCivoTokenCmd)

	configCmd.AddCommand(configGCPCmd, configAWSCmd, configAzureCmd, configCivoCmd)
}

// getRepoPath returns the current repository's local path (with sync)
func getRepoPath() string {
	return shared.GetLocalPath()
}

// parseProjectIDs parses project IDs from arguments (supports comma-separated and space-separated)
func parseProjectIDs(args []string) []string {
	var projectIDs []string
	for _, arg := range args {
		parts := strings.Split(arg, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				projectIDs = append(projectIDs, trimmed)
			}
		}
	}
	return projectIDs
}

func setCivoToken(orgName, token string) {
	credsMgr, err := credentials.NewManager()
	if err != nil {
		log.Fatalf("Failed to create credentials manager: %v", err)
	}
	defer credsMgr.Close()

	if token == "" {
		fmt.Print("Enter Civo API token (input will be hidden): ")
		tokenBytes, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			log.Fatalf("Failed to read token: %v", err)
		}
		token = string(tokenBytes)
	}

	if token == "" {
		log.Fatal("Token cannot be empty")
	}

	if err := credsMgr.StoreCivoToken(orgName, token); err != nil {
		log.Fatalf("Failed to store token: %v", err)
	}

	log.Printf("✅ Civo API token stored for organization '%s'", orgName)
	log.Println()
	log.Println("💡 The token is encrypted and stored in ~/.hyve/hyve.db")
	log.Println("💡 Hyve will now use this token automatically for Civo operations")
}

func getCivoToken(orgName string) {
	credsMgr, err := credentials.NewManager()
	if err != nil {
		log.Fatalf("Failed to create credentials manager: %v", err)
	}
	defer credsMgr.Close()

	token, err := credsMgr.GetCivoToken(orgName)
	if err != nil {
		log.Fatalf("Failed to get token: %v", err)
	}

	if token == "" {
		log.Printf("❌ No Civo token stored for organization '%s'", orgName)
		log.Println()
		log.Println("💡 Store a token with: hyve config civo token set")
		return
	}

	fmt.Println("🔑 Civo API token:")
	fmt.Println(token)
}

func clearCivoToken(orgName string) {
	credsMgr, err := credentials.NewManager()
	if err != nil {
		log.Fatalf("Failed to create credentials manager: %v", err)
	}
	defer credsMgr.Close()

	hasToken, err := credsMgr.HasCivoToken(orgName)
	if err != nil {
		log.Fatalf("Failed to check for token: %v", err)
	}

	if !hasToken {
		log.Printf("ℹ️  No Civo token stored for organization '%s'", orgName)
		return
	}

	if err := credsMgr.ClearCivoToken(orgName); err != nil {
		log.Fatalf("Failed to clear token: %v", err)
	}

	log.Printf("✅ Civo API token removed for organization '%s'", orgName)
}

// GCP helper functions

func addGCPProject(name, projectID string) {
	if name == "" {
		log.Fatal("Project name is required (--name)")
	}
	if projectID == "" {
		log.Fatal("Project ID is required (--id)")
	}

	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasGCPProject(name)
	if err != nil {
		log.Fatalf("Failed to check GCP config: %v", err)
	}

	if err := mgr.AddGCPProject(name, projectID); err != nil {
		log.Fatalf("Failed to add GCP project: %v", err)
	}

	if exists {
		log.Printf("✅ Updated GCP project '%s':\n", name)
	} else {
		log.Printf("✅ Added GCP project '%s':\n", name)
	}
	log.Printf("   Name:       %s", name)
	log.Printf("   Project ID: %s", projectID)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/gcp.yaml")
	log.Println("💡 Use this project when creating clusters:")
	log.Printf("   hyve cluster add my-cluster --provider gcp --gcp-project %s --region us-central1", name)
}

func removeGCPProject(name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	projectID, err := mgr.GetGCPProjectID(name)
	if err != nil {
		log.Fatalf("❌ GCP project '%s' not found", name)
	}

	if err := mgr.RemoveGCPProject(name); err != nil {
		log.Fatalf("Failed to remove GCP project: %v", err)
	}

	log.Printf("✅ Removed GCP project '%s' (project ID: %s)", name, projectID)
}

func listGCPProjects() {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	projects, err := mgr.ListGCPProjects()
	if err != nil {
		log.Fatalf("Failed to list GCP projects: %v", err)
	}

	if len(projects) == 0 {
		log.Println("❌ No GCP projects configured")
		log.Println()
		log.Println("💡 Add a project with:")
		log.Println("   hyve config gcp project add --name dev --id my-project-id")
		return
	}

	log.Printf("🌐 GCP Projects (%d):\n", len(projects))
	log.Println()
	for _, p := range projects {
		log.Printf("   %s", p.Name)
		log.Printf("      Project ID: %s", p.ProjectID)
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config gcp project add --name <name> --id <id>  # Add/update project")
	log.Println("   hyve config gcp project remove <name>                 # Remove project")
	log.Println("   hyve config gcp project get <name>                    # Get project ID")
	log.Println()
	log.Println("💡 Use with cluster create:")
	log.Println("   hyve cluster add my-cluster --provider gcp --gcp-project <name> --region us-central1")
}

func getGCPProject(name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	projectID, err := mgr.GetGCPProjectID(name)
	if err != nil {
		log.Fatalf("❌ GCP project '%s' not found", name)
	}

	log.Printf("%s\n", projectID)
}

// AWS Account helper functions

func addAWSAccount(name, accountID string) {
	if name == "" {
		log.Fatal("Account name is required (--name)")
	}
	if accountID == "" {
		log.Fatal("Account ID is required (--id)")
	}

	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasAWSAccount(name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}

	if err := mgr.AddAWSAccount(name, accountID); err != nil {
		log.Fatalf("Failed to add AWS account: %v", err)
	}

	if exists {
		log.Printf("✅ Updated AWS account '%s':\n", name)
	} else {
		log.Printf("✅ Added AWS account '%s':\n", name)
	}
	log.Printf("   Name:       %s", name)
	log.Printf("   Account ID: %s", accountID)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
}

func removeAWSAccount(name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	accountID, err := mgr.GetAWSAccountID(name)
	if err != nil {
		log.Fatalf("❌ AWS account '%s' not found", name)
	}

	if err := mgr.RemoveAWSAccount(name); err != nil {
		log.Fatalf("Failed to remove AWS account: %v", err)
	}

	log.Printf("✅ Removed AWS account '%s' (ID: %s)", name, accountID)
}

func listAWSAccounts() {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	accounts, err := mgr.ListAWSAccounts()
	if err != nil {
		log.Fatalf("Failed to list AWS accounts: %v", err)
	}

	if len(accounts) == 0 {
		log.Println("❌ No AWS accounts configured")
		log.Println()
		log.Println("💡 Add an account with:")
		log.Println("   hyve config aws account add --name prod --id 123456789012")
		return
	}

	log.Printf("☁️  AWS Accounts (%d):\n", len(accounts))
	log.Println()
	for _, a := range accounts {
		log.Printf("   %s", a.Name)
		log.Printf("      Account ID: %s", a.AccountID)
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config aws account add --name <name> --id <id>  # Add/update account")
	log.Println("   hyve config aws account remove <name>                # Remove account")
	log.Println("   hyve config aws account get <name>                   # Get account ID")
}

func getAWSAccount(name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	accountID, err := mgr.GetAWSAccountID(name)
	if err != nil {
		log.Fatalf("❌ AWS account '%s' not found", name)
	}

	log.Printf("%s\n", accountID)
}

// AWS EKS Role helper functions

func addAWSEKSRole(accountName, name, roleARN string) {
	if name == "" {
		log.Fatal("Role name is required (--name)")
	}
	if roleARN == "" {
		log.Fatal("Role ARN is required (--role-arn)")
	}
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasAWSEKSRole(accountName, name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}

	if err := mgr.AddAWSEKSRole(accountName, name, roleARN); err != nil {
		log.Fatalf("Failed to add EKS role: %v", err)
	}

	if exists {
		log.Printf("✅ Updated EKS role '%s' in account '%s':\n", name, accountName)
	} else {
		log.Printf("✅ Added EKS role '%s' to account '%s':\n", name, accountName)
	}
	log.Printf("   Name:     %s", name)
	log.Printf("   Role ARN: %s", roleARN)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
}

func removeAWSEKSRole(accountName, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	roleARN, err := mgr.GetAWSEKSRoleARN(accountName, name)
	if err != nil {
		log.Fatalf("❌ EKS role '%s' not found in account '%s'", name, accountName)
	}

	if err := mgr.RemoveAWSEKSRole(accountName, name); err != nil {
		log.Fatalf("Failed to remove EKS role: %v", err)
	}

	log.Printf("✅ Removed EKS role '%s' from account '%s' (ARN: %s)", name, accountName, roleARN)
}

func listAWSEKSRoles(accountName string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	roles, err := mgr.ListAWSEKSRoles(accountName)
	if err != nil {
		log.Fatalf("Failed to list EKS roles: %v", err)
	}

	if len(roles) == 0 {
		log.Printf("❌ No EKS roles configured for account '%s'", accountName)
		log.Println()
		log.Println("💡 Add an EKS role with:")
		log.Println("   hyve config aws eks-role add --name default-role --role-arn arn:aws:iam::123456789012:role/my-role")
		return
	}

	log.Printf("🔐 EKS IAM Roles for account '%s' (%d):\n", accountName, len(roles))
	log.Println()
	for _, r := range roles {
		log.Printf("   %s", r.Name)
		log.Printf("      Role ARN: %s", r.RoleARN)
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config aws eks-role add --name <name> --role-arn <arn>  # Add/update role")
	log.Println("   hyve config aws eks-role remove <name>                       # Remove role")
	log.Println("   hyve config aws eks-role get <name>                          # Get role ARN")
}

func getAWSEKSRole(accountName, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	roleARN, err := mgr.GetAWSEKSRoleARN(accountName, name)
	if err != nil {
		log.Fatalf("❌ EKS role '%s' not found in account '%s'", name, accountName)
	}

	log.Printf("%s\n", roleARN)
}

// AWS Node Role helper functions

func addAWSNodeRole(accountName, name, roleARN string) {
	if name == "" {
		log.Fatal("Role name is required (--name)")
	}
	if roleARN == "" {
		log.Fatal("Role ARN is required (--role-arn)")
	}
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasAWSNodeRole(accountName, name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}

	if err := mgr.AddAWSNodeRole(accountName, name, roleARN); err != nil {
		log.Fatalf("Failed to add node role: %v", err)
	}

	if exists {
		log.Printf("✅ Updated node role '%s' in account '%s':\n", name, accountName)
	} else {
		log.Printf("✅ Added node role '%s' to account '%s':\n", name, accountName)
	}
	log.Printf("   Name:     %s", name)
	log.Printf("   Role ARN: %s", roleARN)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
}

func removeAWSNodeRole(accountName, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	roleARN, err := mgr.GetAWSNodeRoleARN(accountName, name)
	if err != nil {
		log.Fatalf("❌ Node role '%s' not found in account '%s'", name, accountName)
	}

	if err := mgr.RemoveAWSNodeRole(accountName, name); err != nil {
		log.Fatalf("Failed to remove node role: %v", err)
	}

	log.Printf("✅ Removed node role '%s' from account '%s' (ARN: %s)", name, accountName, roleARN)
}

func listAWSNodeRoles(accountName string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	roles, err := mgr.ListAWSNodeRoles(accountName)
	if err != nil {
		log.Fatalf("Failed to list node roles: %v", err)
	}

	if len(roles) == 0 {
		log.Printf("❌ No node roles configured for account '%s'", accountName)
		log.Println()
		log.Println("💡 Add a node role with:")
		log.Println("   hyve config aws node-role add --name default-node-role --role-arn arn:aws:iam::123456789012:role/my-node-role")
		return
	}

	log.Printf("🔐 EKS Node IAM Roles for account '%s' (%d):\n", accountName, len(roles))
	log.Println()
	for _, r := range roles {
		log.Printf("   %s", r.Name)
		log.Printf("      Role ARN: %s", r.RoleARN)
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config aws node-role add --name <name> --role-arn <arn>  # Add/update role")
	log.Println("   hyve config aws node-role remove <name>                       # Remove role")
	log.Println("   hyve config aws node-role get <name>                          # Get role ARN")
}

func getAWSNodeRole(accountName, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	roleARN, err := mgr.GetAWSNodeRoleARN(accountName, name)
	if err != nil {
		log.Fatalf("❌ Node role '%s' not found in account '%s'", name, accountName)
	}

	log.Printf("%s\n", roleARN)
}

// AWS Node Role create/delete helper functions (actual AWS operations)

func createAWSNodeRole(accountName, name, roleName, region string) {
	if name == "" {
		log.Fatal("Role alias name is required (--name)")
	}
	if roleName == "" {
		log.Fatal("IAM role name is required (--role-name)")
	}
	if region == "" {
		region = "us-east-1"
	}
	repoPath := getRepoPath()
	configMgr := providerconfig.NewManager(repoPath)

	exists, err := configMgr.HasAWSNodeRole(accountName, name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}
	if exists {
		log.Fatalf("❌ Node role alias '%s' already exists in account '%s'. Use 'hyve config aws node-role remove' first or choose a different name.", name, accountName)
	}

	log.Printf("🔐 Creating EKS node IAM role '%s' in AWS region %s...", roleName, region)

	resourceMgr, err := aws.NewResourceManager(region)
	if err != nil {
		log.Fatalf("Failed to create AWS resource manager: %v", err)
	}

	ctx := gocontext.Background()
	roleInfo, err := resourceMgr.CreateNodeRole(ctx, roleName)
	if err != nil {
		log.Fatalf("Failed to create node IAM role in AWS: %v", err)
	}

	log.Printf("✅ Created IAM role '%s' in AWS", roleInfo.Name)
	log.Printf("   Role ARN: %s", roleInfo.ARN)

	if err := configMgr.AddAWSNodeRole(accountName, name, roleInfo.ARN); err != nil {
		log.Printf("⚠️  Warning: Role created in AWS but failed to save alias: %v", err)
		log.Printf("   You can manually add it with: hyve config aws node-role add --name %s --role-arn %s", name, roleInfo.ARN)
		return
	}

	log.Printf("✅ Stored alias '%s' in account '%s'", name, accountName)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
	log.Printf("💡 Use this role when creating EKS clusters with: --node-role-name %s", name)
}

func deleteAWSNodeRole(accountName, name, region string, configOnly bool) {
	if region == "" {
		region = "us-east-1"
	}
	repoPath := getRepoPath()
	configMgr := providerconfig.NewManager(repoPath)

	roleARN, err := configMgr.GetAWSNodeRoleARN(accountName, name)
	if err != nil {
		log.Fatalf("❌ Node role alias '%s' not found in account '%s'", name, accountName)
	}

	if configOnly {
		if err := configMgr.RemoveAWSNodeRole(accountName, name); err != nil {
			log.Fatalf("Failed to remove node role from configuration: %v", err)
		}
		log.Printf("✅ Removed node role alias '%s' from account '%s'", name, accountName)
		log.Printf("   Note: The IAM role still exists in AWS (ARN: %s)", roleARN)
		return
	}

	roleName := extractRoleNameFromARN(roleARN)
	if roleName == "" {
		log.Fatalf("❌ Could not extract role name from ARN: %s", roleARN)
	}

	log.Printf("🗑️  Deleting node IAM role '%s' from AWS...", roleName)

	resourceMgr, err := aws.NewResourceManager(region)
	if err != nil {
		log.Fatalf("Failed to create AWS resource manager: %v", err)
	}

	ctx := gocontext.Background()
	if err := resourceMgr.DeleteNodeRole(ctx, roleName); err != nil {
		log.Fatalf("Failed to delete node IAM role from AWS: %v\n\n"+
			"Configuration was NOT updated to prevent inconsistent state.\n"+
			"Use --config-only to remove only the configuration.", err)
	}

	log.Printf("✅ Deleted IAM role '%s' from AWS", roleName)

	if err := configMgr.RemoveAWSNodeRole(accountName, name); err != nil {
		log.Printf("⚠️  Warning: Role deleted from AWS but failed to remove alias: %v", err)
		return
	}

	log.Printf("✅ Removed alias '%s' from account '%s'", name, accountName)
}

// AWS VPC helper functions

func addAWSVPC(accountName, name, vpcID string) {
	if name == "" {
		log.Fatal("VPC name is required (--name)")
	}
	if vpcID == "" {
		log.Fatal("VPC ID is required (--id)")
	}
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasAWSVPC(accountName, name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}

	if err := mgr.AddAWSVPC(accountName, name, vpcID); err != nil {
		log.Fatalf("Failed to add VPC: %v", err)
	}

	if exists {
		log.Printf("✅ Updated VPC '%s' in account '%s':\n", name, accountName)
	} else {
		log.Printf("✅ Added VPC '%s' to account '%s':\n", name, accountName)
	}
	log.Printf("   Name:   %s", name)
	log.Printf("   VPC ID: %s", vpcID)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
}

func removeAWSVPC(accountName, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	vpcID, err := mgr.GetAWSVPCID(accountName, name)
	if err != nil {
		log.Fatalf("❌ VPC '%s' not found in account '%s'", name, accountName)
	}

	if err := mgr.RemoveAWSVPC(accountName, name); err != nil {
		log.Fatalf("Failed to remove VPC: %v", err)
	}

	log.Printf("✅ Removed VPC '%s' from account '%s' (ID: %s)", name, accountName, vpcID)
}

func listAWSVPCs(accountName string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	vpcs, err := mgr.ListAWSVPCs(accountName)
	if err != nil {
		log.Fatalf("Failed to list VPCs: %v", err)
	}

	if len(vpcs) == 0 {
		log.Printf("❌ No VPCs configured for account '%s'", accountName)
		log.Println()
		log.Println("💡 Add a VPC with:")
		log.Println("   hyve config aws vpc add --name default-vpc --id vpc-0123456789abcdef0")
		return
	}

	log.Printf("🌐 VPCs for account '%s' (%d):\n", accountName, len(vpcs))
	log.Println()
	for _, v := range vpcs {
		log.Printf("   %s", v.Name)
		log.Printf("      VPC ID: %s", v.VPCID)
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config aws vpc add --name <name> --id <vpc-id>  # Add/update VPC")
	log.Println("   hyve config aws vpc remove <name>                    # Remove VPC")
	log.Println("   hyve config aws vpc get <name>                       # Get VPC ID")
}

func getAWSVPC(accountName, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	vpcID, err := mgr.GetAWSVPCID(accountName, name)
	if err != nil {
		log.Fatalf("❌ VPC '%s' not found in account '%s'", name, accountName)
	}

	log.Printf("%s\n", vpcID)
}

// AWS EKS Role create/delete helper functions (actual AWS operations)

func createAWSEKSRole(accountName, name, roleName, region string) {
	if name == "" {
		log.Fatal("Role alias name is required (--name)")
	}
	if roleName == "" {
		log.Fatal("IAM role name is required (--role-name)")
	}
	if region == "" {
		region = "us-east-1"
	}
	repoPath := getRepoPath()
	configMgr := providerconfig.NewManager(repoPath)

	exists, err := configMgr.HasAWSEKSRole(accountName, name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}
	if exists {
		log.Fatalf("❌ EKS role alias '%s' already exists in account '%s'. Use 'hyve config aws eks-role remove' first or choose a different name.", name, accountName)
	}

	log.Printf("🔐 Creating EKS IAM role '%s' in AWS region %s...", roleName, region)

	resourceMgr, err := aws.NewResourceManager(region)
	if err != nil {
		log.Fatalf("Failed to create AWS resource manager: %v", err)
	}

	ctx := gocontext.Background()
	roleInfo, err := resourceMgr.CreateEKSRole(ctx, roleName)
	if err != nil {
		log.Fatalf("Failed to create EKS IAM role in AWS: %v", err)
	}

	log.Printf("✅ Created IAM role '%s' in AWS", roleInfo.Name)
	log.Printf("   Role ARN: %s", roleInfo.ARN)

	if err := configMgr.AddAWSEKSRole(accountName, name, roleInfo.ARN); err != nil {
		log.Printf("⚠️  Warning: Role created in AWS but failed to save alias: %v", err)
		log.Printf("   You can manually add it with: hyve config aws eks-role add --name %s --role-arn %s", name, roleInfo.ARN)
		return
	}

	log.Printf("✅ Stored alias '%s' in account '%s'", name, accountName)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
	log.Printf("💡 Use this role when creating EKS clusters with: --eks-role %s", name)
}

func deleteAWSEKSRole(accountName, name, region string, configOnly bool) {
	if region == "" {
		region = "us-east-1"
	}
	repoPath := getRepoPath()
	configMgr := providerconfig.NewManager(repoPath)

	roleARN, err := configMgr.GetAWSEKSRoleARN(accountName, name)
	if err != nil {
		log.Fatalf("❌ EKS role alias '%s' not found in account '%s'", name, accountName)
	}

	if configOnly {
		if err := configMgr.RemoveAWSEKSRole(accountName, name); err != nil {
			log.Fatalf("Failed to remove EKS role from configuration: %v", err)
		}
		log.Printf("✅ Removed EKS role alias '%s' from account '%s'", name, accountName)
		log.Printf("   Note: The IAM role still exists in AWS (ARN: %s)", roleARN)
		return
	}

	roleName := extractRoleNameFromARN(roleARN)
	if roleName == "" {
		log.Fatalf("❌ Could not extract role name from ARN: %s", roleARN)
	}

	log.Printf("🗑️  Deleting EKS IAM role '%s' from AWS...", roleName)

	resourceMgr, err := aws.NewResourceManager(region)
	if err != nil {
		log.Fatalf("Failed to create AWS resource manager: %v", err)
	}

	ctx := gocontext.Background()
	if err := resourceMgr.DeleteEKSRole(ctx, roleName); err != nil {
		log.Fatalf("Failed to delete EKS IAM role from AWS: %v\n\n"+
			"Configuration was NOT updated to prevent inconsistent state.\n"+
			"Use --config-only to remove only the configuration.", err)
	}

	log.Printf("✅ Deleted IAM role '%s' from AWS", roleName)

	if err := configMgr.RemoveAWSEKSRole(accountName, name); err != nil {
		log.Printf("⚠️  Warning: Role deleted from AWS but failed to remove alias: %v", err)
		return
	}

	log.Printf("✅ Removed alias '%s' from account '%s'", name, accountName)
}

func extractRoleNameFromARN(arn string) string {
	parts := strings.Split(arn, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return ""
}

// AWS VPC create/delete helper functions (actual AWS operations)

func createAWSVPC(accountName, name, region, cidr, subnets string, enableDNS bool) {
	if name == "" {
		log.Fatal("VPC alias name is required (--name)")
	}
	if region == "" {
		region = "us-east-1"
	}
	if cidr == "" {
		cidr = "10.0.0.0/16"
	}
	repoPath := getRepoPath()
	configMgr := providerconfig.NewManager(repoPath)

	exists, err := configMgr.HasAWSVPC(accountName, name)
	if err != nil {
		log.Fatalf("Failed to check AWS config: %v", err)
	}
	if exists {
		log.Fatalf("❌ VPC alias '%s' already exists in account '%s'. Use 'hyve config aws vpc remove' first or choose a different name.", name, accountName)
	}

	log.Printf("🌐 Creating VPC '%s' in AWS region %s...", name, region)
	log.Printf("   CIDR: %s", cidr)

	resourceMgr, err := aws.NewResourceManager(region)
	if err != nil {
		log.Fatalf("Failed to create AWS resource manager: %v", err)
	}

	var subnetCIDRs []string
	if subnets != "" {
		parts := strings.Split(subnets, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				subnetCIDRs = append(subnetCIDRs, trimmed)
			}
		}
	}

	ctx := gocontext.Background()
	vpcInput := &aws.CreateVPCInput{
		Name:              name,
		CIDR:              cidr,
		EnableDNSSupport:  enableDNS,
		EnableDNSHostname: enableDNS,
		CreateSubnets:     len(subnetCIDRs) > 0,
		SubnetCIDRs:       subnetCIDRs,
	}

	vpcInfo, err := resourceMgr.CreateVPC(ctx, vpcInput)
	if err != nil {
		log.Fatalf("Failed to create VPC in AWS: %v", err)
	}

	log.Printf("✅ Created VPC '%s' in AWS", vpcInfo.Name)
	log.Printf("   VPC ID: %s", vpcInfo.ID)
	log.Printf("   CIDR:   %s", vpcInfo.CIDR)
	log.Printf("   State:  %s", vpcInfo.State)

	if len(vpcInfo.Subnets) > 0 {
		log.Printf("   Subnets:")
		for _, subnet := range vpcInfo.Subnets {
			log.Printf("      %s (%s) - %s", subnet.ID, subnet.CIDR, subnet.AvailabilityZone)
		}
	}

	if err := configMgr.AddAWSVPC(accountName, name, vpcInfo.ID); err != nil {
		log.Printf("⚠️  Warning: VPC created in AWS but failed to save alias: %v", err)
		log.Printf("   You can manually add it with: hyve config aws vpc add --name %s --id %s", name, vpcInfo.ID)
		return
	}

	log.Printf("✅ Stored alias '%s' in account '%s'", name, accountName)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/aws.yaml")
	log.Printf("💡 Use this VPC when creating EKS clusters with: --vpc %s", name)
}

func deleteAWSVPC(accountName, name, region string, configOnly bool) {
	if region == "" {
		region = "us-east-1"
	}
	repoPath := getRepoPath()
	configMgr := providerconfig.NewManager(repoPath)

	vpcID, err := configMgr.GetAWSVPCID(accountName, name)
	if err != nil {
		log.Fatalf("❌ VPC alias '%s' not found in account '%s'", name, accountName)
	}

	if configOnly {
		if err := configMgr.RemoveAWSVPC(accountName, name); err != nil {
			log.Fatalf("Failed to remove VPC from configuration: %v", err)
		}
		log.Printf("✅ Removed VPC alias '%s' from account '%s'", name, accountName)
		log.Printf("   Note: The VPC still exists in AWS (ID: %s)", vpcID)
		return
	}

	log.Printf("🗑️  Deleting VPC '%s' from AWS...", vpcID)

	resourceMgr, err := aws.NewResourceManager(region)
	if err != nil {
		log.Fatalf("Failed to create AWS resource manager: %v", err)
	}

	ctx := gocontext.Background()
	if err := resourceMgr.DeleteVPC(ctx, vpcID); err != nil {
		log.Fatalf("Failed to delete VPC from AWS: %v\n\n"+
			"Configuration was NOT updated to prevent inconsistent state.\n"+
			"Use --config-only to remove only the configuration.", err)
	}

	log.Printf("✅ Deleted VPC '%s' from AWS", vpcID)

	if err := configMgr.RemoveAWSVPC(accountName, name); err != nil {
		log.Printf("⚠️  Warning: VPC deleted from AWS but failed to remove alias: %v", err)
		return
	}

	log.Printf("✅ Removed alias '%s' from account '%s'", name, accountName)
}

// Azure helper functions

func addAzureSubscriptionIDs(name, subscriptionID string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasAzureSubscription(name)
	if err != nil {
		log.Fatalf("Failed to check Azure config: %v", err)
	}

	if err := mgr.AddAzureSubscription(name, subscriptionID); err != nil {
		log.Fatalf("Failed to add Azure subscription: %v", err)
	}

	if exists {
		log.Printf("✅ Updated Azure subscription '%s'", name)
	} else {
		log.Printf("✅ Added Azure subscription '%s' (ID: %s)", name, subscriptionID)
	}

	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/azure.yaml")
}

func removeAzureSubscriptionIDs(args []string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	for _, name := range args {
		if err := mgr.RemoveAzureSubscription(name); err != nil {
			log.Printf("❌ Failed to remove subscription '%s': %v", name, err)
			continue
		}
		log.Printf("✅ Removed Azure subscription '%s'", name)
	}
}

func listAzureSubscriptionIDs() {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	subscriptions, err := mgr.ListAzureSubscriptions()
	if err != nil {
		log.Fatalf("Failed to load Azure config: %v", err)
	}

	if len(subscriptions) == 0 {
		log.Println("❌ No Azure subscriptions configured")
		log.Println()
		log.Println("💡 Add subscriptions with:")
		log.Println("   hyve config azure subscription add --name <name> --id <subscription-id>")
		return
	}

	log.Printf("🔷 Azure Subscriptions (%d):\n", len(subscriptions))
	log.Println()
	for _, s := range subscriptions {
		log.Printf("   %s", s.Name)
		log.Printf("      Subscription ID: %s", s.SubscriptionID)
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config azure subscription add --name <name> --id <id>  # Add subscription")
	log.Println("   hyve config azure subscription remove <name>                # Remove subscription")
}

func listAzureResourceGroups(subscription string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	rgs, err := mgr.ListAzureResourceGroups(subscription)
	if err != nil {
		log.Fatalf("Failed to list resource groups: %v", err)
	}

	if len(rgs) == 0 {
		log.Printf("❌ No resource groups configured for subscription '%s'", subscription)
		log.Println()
		log.Printf("💡 Add one with: hyve config azure resource-group add --subscription %s --name <name> --location <location>", subscription)
		return
	}

	log.Printf("🔷 Resource Groups for subscription '%s' (%d):\n", subscription, len(rgs))
	log.Println()
	for _, rg := range rgs {
		log.Printf("   %s", rg.Name)
		log.Printf("      Location: %s", rg.Location)
		log.Println()
	}
}

func addAzureResourceGroup(subscription, name, location string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	subscriptionID, err := mgr.GetAzureSubscriptionID(subscription)
	if err != nil {
		log.Fatalf("Failed to resolve subscription '%s': %v", subscription, err)
	}

	tenantID, clientID, clientSecret, err := mgr.GetAzureCredentials(subscription)
	if err != nil {
		log.Fatalf("Failed to load Azure credentials: %v", err)
	}

	if err := azureprovider.CreateResourceGroup(gocontext.Background(), subscriptionID, name, location, tenantID, clientID, clientSecret); err != nil {
		log.Fatalf("Failed to create resource group in Azure: %v", err)
	}

	if err := mgr.AddAzureResourceGroup(subscription, name, location); err != nil {
		log.Fatalf("Failed to save resource group to config: %v", err)
	}

	log.Printf("✅ Created resource group '%s' (location: %s) in subscription '%s'", name, location, subscription)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/azure.yaml")
}

func deleteAzureResourceGroup(subscription, name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	subscriptionID, err := mgr.GetAzureSubscriptionID(subscription)
	if err != nil {
		log.Fatalf("Failed to resolve subscription '%s': %v", subscription, err)
	}

	tenantID, clientID, clientSecret, err := mgr.GetAzureCredentials(subscription)
	if err != nil {
		log.Fatalf("Failed to load Azure credentials: %v", err)
	}

	if err := azureprovider.DeleteResourceGroup(gocontext.Background(), subscriptionID, name, tenantID, clientID, clientSecret); err != nil {
		log.Fatalf("Failed to delete resource group in Azure: %v", err)
	}

	if err := mgr.RemoveAzureResourceGroup(subscription, name); err != nil {
		log.Fatalf("Failed to remove resource group from config: %v", err)
	}

	log.Printf("✅ Deleted resource group '%s' from subscription '%s'", name, subscription)
}

// Civo helper functions

func addCivoOrganization(name, orgID string) {
	if name == "" {
		log.Fatal("Organization name is required (--name)")
	}
	if orgID == "" {
		log.Fatal("Organization ID is required (--id)")
	}

	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	exists, err := mgr.HasCivoOrganization(name)
	if err != nil {
		log.Fatalf("Failed to check Civo config: %v", err)
	}

	if err := mgr.AddCivoOrganization(name, orgID); err != nil {
		log.Fatalf("Failed to add Civo organization: %v", err)
	}

	if exists {
		log.Printf("✅ Updated Civo organization '%s':\n", name)
	} else {
		log.Printf("✅ Added Civo organization '%s':\n", name)
	}
	log.Printf("   Name:  %s", name)
	log.Printf("   Org ID: %s", orgID)
	log.Println()
	log.Println("💡 The configuration is stored in provider-configs/civo.yaml")
	log.Println("💡 Set this as the current organization with:")
	log.Printf("   hyve config use civo %s", name)
}

func removeCivoOrganization(name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	orgID, err := mgr.GetCivoOrgID(name)
	if err != nil {
		log.Fatalf("❌ Civo organization '%s' not found", name)
	}

	if err := mgr.RemoveCivoOrganization(name); err != nil {
		log.Fatalf("Failed to remove Civo organization: %v", err)
	}

	log.Printf("✅ Removed Civo organization '%s' (ID: %s)", name, orgID)
}

func listCivoOrganizations() {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	orgs, err := mgr.ListCivoOrganizations()
	if err != nil {
		log.Fatalf("Failed to list Civo organizations: %v", err)
	}

	if len(orgs) == 0 {
		log.Println("❌ No Civo organizations configured")
		log.Println()
		log.Println("💡 Add an organization with:")
		log.Println("   hyve config civo org add --name prod --id org-abc123")
		return
	}

	log.Printf("🟢 Civo Organizations (%d):\n", len(orgs))
	log.Println()
	for _, o := range orgs {
		log.Printf("   %s", o.Name)
		log.Printf("      Org ID: %s", o.OrgID)
		if len(o.Regions) > 0 {
			log.Printf("      Regions: %v", o.Regions)
		}
		log.Println()
	}
	log.Println("💡 Commands:")
	log.Println("   hyve config civo org add --name <name> --id <org-id>  # Add/update organization")
	log.Println("   hyve config civo org remove <name>                     # Remove organization")
	log.Println("   hyve config civo org get <name>                        # Get organization ID")
	log.Println()
	log.Println("💡 Set the current organization with:")
	log.Println("   hyve config use civo <name>")
}

func getCivoOrganization(name string) {
	repoPath := getRepoPath()
	mgr := providerconfig.NewManager(repoPath)

	orgID, err := mgr.GetCivoOrgID(name)
	if err != nil {
		log.Fatalf("❌ Civo organization '%s' not found", name)
	}

	log.Printf("%s\n", orgID)
}
