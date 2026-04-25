package config

import (
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/providerconfig"
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
  hyve cluster create my-cluster --provider gcp --gcp-project dev --region us-central1`,
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

	// Azure subcommands
	configAzureAddSubscriptionIDsCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the subscription (required)")
	configAzureAddSubscriptionIDsCmd.Flags().StringP("id", "i", "", "Azure subscription ID (required)")
	configAzureAddSubscriptionIDsCmd.MarkFlagRequired("name")
	configAzureAddSubscriptionIDsCmd.MarkFlagRequired("id")

	configAzureSubscriptionCmd.AddCommand(configAzureAddSubscriptionIDsCmd, configAzureRemoveSubscriptionIDsCmd, configAzureListSubscriptionIDsCmd)

	configAzureCmd.AddCommand(configAzureSubscriptionCmd)

	// Civo subcommands
	configCivoOrgAddCmd.Flags().StringP("name", "n", "", "Friendly name/alias for the organization (required)")
	configCivoOrgAddCmd.Flags().StringP("id", "i", "", "Civo organization ID (required)")
	configCivoOrgAddCmd.MarkFlagRequired("name")
	configCivoOrgAddCmd.MarkFlagRequired("id")

	configCivoOrgCmd.AddCommand(configCivoOrgAddCmd, configCivoOrgRemoveCmd, configCivoOrgListCmd, configCivoOrgGetCmd)
	configCivoCmd.AddCommand(configCivoOrgCmd)

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
	log.Printf("   hyve cluster create my-cluster --provider gcp --gcp-project %s --region us-central1", name)
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
	log.Println("   hyve cluster create my-cluster --provider gcp --gcp-project <name> --region us-central1")
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
