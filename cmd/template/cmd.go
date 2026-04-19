package template

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/cluster"
	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// Cmd is the root template command exposed to the parent.
var Cmd = templateCmd

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage cluster templates",
	Long:  "Create, list, delete, and execute cluster templates with associated workflows",
}

var templateCreateCmd = &cobra.Command{
	Use:   "create [template-name]",
	Short: "Create a new cluster template",
	Long: `Create a new cluster template with cluster specifications and workflows.

The template will be created interactively, prompting for cluster details
and workflows to execute upon cluster creation or destruction.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		description, _ := cmd.Flags().GetString("description")
		provider, _ := cmd.Flags().GetString("provider")
		region, _ := cmd.Flags().GetString("region")
		nodes, _ := cmd.Flags().GetString("nodes")
		clusterType, _ := cmd.Flags().GetString("cluster-type")
		onCreatedWorkflows, _ := cmd.Flags().GetString("on-created")
		onDestroyWorkflows, _ := cmd.Flags().GetString("on-destroy")
		schedule, _ := cmd.Flags().GetString("schedule")

		var nodeGroups []types.NodeGroup
		if ngStrs, _ := cmd.Flags().GetStringArray("node-group"); len(ngStrs) > 0 {
			for _, s := range ngStrs {
				ng, err := shared.ParseNodeGroup(s)
				if err != nil {
					log.Fatalf("Invalid --node-group value '%s': %v", s, err)
				}
				nodeGroups = append(nodeGroups, ng)
			}
		}

		createTemplate(templateName, description, provider, region, nodes, clusterType, nodeGroups, onCreatedWorkflows, onDestroyWorkflows, schedule)
	},
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cluster templates",
	Long:  "Display all available cluster templates in the current repository",
	Run: func(cmd *cobra.Command, args []string) {
		listTemplates()
	},
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete [template-name]",
	Short: "Delete a cluster template",
	Long:  "Remove a cluster template from the repository",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		deleteTemplate(templateName)
	},
}

var templateExecuteCmd = &cobra.Command{
	Use:   "execute [template-name] [cluster-name]",
	Short: "Create a cluster from a template",
	Long: `Execute a cluster template to create a new cluster.

The provider type in the template determines which account flag is required:
  --org         Civo organization name    (required for civo)
  --account     AWS account alias         (required for aws)
  --vpc-name    AWS VPC alias             (required for aws)
  --eks-role    AWS EKS cluster role alias (required for aws)
  --node-role   AWS node role alias        (required for aws)
  --subscription  Azure subscription alias  (required for azure)
  --resource-group Azure resource group    (required for azure)
  --project     GCP project alias          (required for gcp)

This command:
  1. Creates a cluster based on the template specifications
  2. Waits for the cluster to become ready
  3. Executes all workflows defined in the template`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		clusterName := args[1]
		org, _ := cmd.Flags().GetString("org")
		account, _ := cmd.Flags().GetString("account")
		vpcName, _ := cmd.Flags().GetString("vpc-name")
		eksRole, _ := cmd.Flags().GetString("eks-role")
		nodeRole, _ := cmd.Flags().GetString("node-role")
		subscription, _ := cmd.Flags().GetString("subscription")
		resourceGroup, _ := cmd.Flags().GetString("resource-group")
		project, _ := cmd.Flags().GetString("project")
		executeTemplate(templateName, clusterName, org, account, vpcName, eksRole, nodeRole, subscription, resourceGroup, project)
	},
}

var templateShowCmd = &cobra.Command{
	Use:   "show [template-name]",
	Short: "Show template details",
	Long:  "Display the full details of a cluster template",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		showTemplate(templateName)
	},
}

var templateValidateCmd = &cobra.Command{
	Use:   "validate [template-name]",
	Short: "Validate a template",
	Long:  "Validate the syntax and structure of a cluster template definition",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		validateTemplate(templateName)
	},
}

func init() {
	templateCreateCmd.Flags().StringP("description", "d", "", "Template description")
	templateCreateCmd.Flags().StringP("provider", "p", "civo", "Cloud provider")
	templateCreateCmd.Flags().StringP("region", "r", "PHX1", "Region")
	templateCreateCmd.Flags().StringP("nodes", "n", "g4s.kube.small", "Node sizes (comma-separated, Civo only)")
	templateCreateCmd.Flags().StringArrayP("node-group", "g", nil, `Node group spec (repeatable): name=workers,type=t3.medium,count=3[,min=1,max=5,disk=50,spot=true,mode=System]`)
	templateCreateCmd.Flags().StringP("cluster-type", "t", "k3s", "Kubernetes cluster type")
	templateCreateCmd.Flags().StringP("on-created", "c", "", "Workflows to run after cluster creation (comma-separated)")
	templateCreateCmd.Flags().String("on-destroy", "", "Workflows to run before cluster destruction (comma-separated)")
	templateCreateCmd.Flags().String("schedule", "", "Cron expression for cluster expiry (e.g. '0 20 * * 5' — every Friday at 8pm); evaluated at execute time")

	templateExecuteCmd.Flags().StringP("org", "o", "", "Civo organization name (required for civo provider)")
	templateExecuteCmd.Flags().StringP("account", "a", "", "AWS account alias (required for aws provider)")
	templateExecuteCmd.Flags().StringP("vpc-name", "v", "", "AWS VPC alias (required for aws provider)")
	templateExecuteCmd.Flags().StringP("eks-role", "e", "", "AWS EKS cluster role alias (required for aws provider)")
	templateExecuteCmd.Flags().StringP("node-role", "n", "", "AWS node role alias (required for aws provider)")
	templateExecuteCmd.Flags().StringP("subscription", "s", "", "Azure subscription alias (required for azure provider)")
	templateExecuteCmd.Flags().StringP("resource-group", "g", "", "Azure resource group name (required for azure provider)")
	templateExecuteCmd.Flags().StringP("project", "p", "", "GCP project alias (required for gcp provider)")

	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateDeleteCmd)
	templateCmd.AddCommand(templateExecuteCmd)
	templateCmd.AddCommand(templateShowCmd)
	templateCmd.AddCommand(templateValidateCmd)
}

func createTemplate(name, description, provider, region, nodesSizes, clusterType string, nodeGroups []types.NodeGroup, onCreatedStr, onDestroyStr, schedule string) {
	// cluster-type is only meaningful for Civo
	if strings.ToLower(provider) != "civo" {
		if clusterType != "" && clusterType != "k3s" {
			log.Printf("⚠️  cluster-type is only supported for the Civo provider and will be ignored for '%s'", provider)
		}
		clusterType = ""
	}

	ctx := context.Background()
	shared.SyncRepoState(ctx)

	// Get repository path
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	// Create template manager
	templateMgr := template.NewManager(currentRepo.LocalPath)

	// Parse nodes
	nodes := strings.Split(nodesSizes, ",")
	for i, node := range nodes {
		nodes[i] = strings.TrimSpace(node)
	}

	// Parse onCreated workflows
	var onCreatedWorkflows []string
	if onCreatedStr != "" {
		onCreatedWorkflows = strings.Split(onCreatedStr, ",")
		for i, wf := range onCreatedWorkflows {
			onCreatedWorkflows[i] = strings.TrimSpace(wf)
		}
	}

	// Parse onDestroy workflows
	var onDestroyWorkflows []string
	if onDestroyStr != "" {
		onDestroyWorkflows = strings.Split(onDestroyStr, ",")
		for i, wf := range onDestroyWorkflows {
			onDestroyWorkflows[i] = strings.TrimSpace(wf)
		}
	}

	// Create template
	tmpl := &template.Template{
		APIVersion: "v1",
		Kind:       "Template",
		Metadata: template.TemplateMetadata{
			Name:        name,
			Description: description,
		},
		Spec: template.TemplateSpec{
			Provider:    provider,
			Region:      region,
			Nodes:       nodes,
			NodeGroups:  nodeGroups,
			ClusterType: clusterType,
			Workflows: template.TemplateWorkflowsSpec{
				OnCreated: onCreatedWorkflows,
				OnDestroy: onDestroyWorkflows,
			},
			Schedule: schedule,
		},
	}

	// Save template
	if err := templateMgr.CreateTemplate(tmpl); err != nil {
		log.Fatalf("Failed to create template: %v", err)
	}

	log.Printf("✅ Template '%s' created successfully", name)

	// Commit and push template to Git
	authUsername, authToken := shared.GetAuthCredentials(currentRepo)
	stateMgr, err := state.NewManager(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Printf("⚠️  Warning: Failed to create state manager: %v", err)
		log.Println("💡 Template saved locally but not pushed to git")
	} else {
		shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Create template %s", name))
	}
	log.Printf("Template path: %s", templateMgr.GetTemplatePath(name))
	log.Println("\n📋 Template Details:")
	log.Printf("  Provider: %s", provider)
	log.Printf("  Region: %s", region)
	log.Printf("  Nodes: %s", strings.Join(nodes, ", "))
	log.Printf("  Cluster Type: %s", clusterType)
	if len(onCreatedWorkflows) > 0 {
		log.Printf("  OnCreated Workflows: %s", strings.Join(onCreatedWorkflows, ", "))
	}
	if len(onDestroyWorkflows) > 0 {
		log.Printf("  OnDestroy Workflows: %s", strings.Join(onDestroyWorkflows, ", "))
	}
	if schedule != "" {
		log.Printf("  Expiry Schedule: %s", schedule)
	}

	log.Println("\n💡 Execute this template with:")
	log.Printf("  hyve template execute %s <cluster-name>", name)
}

func listTemplates() {
	shared.SyncRepoState(context.Background())

	// Get repository path
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	// Create template manager
	templateMgr := template.NewManager(currentRepo.LocalPath)

	templates, err := templateMgr.ListTemplates()
	if err != nil {
		log.Fatalf("Failed to list templates: %v", err)
	}

	if len(templates) == 0 {
		log.Println("No templates found")
		log.Println("\n💡 Create a template with:")
		log.Println("  hyve template create <name> --region PHX1 --nodes g4s.kube.medium")
		return
	}

	log.Printf("📋 Available templates (%d):\n", len(templates))
	for _, tmpl := range templates {
		log.Printf("  %s  (file: %s)", tmpl.Metadata.Name, tmpl.Filename)
		if tmpl.Metadata.Description != "" {
			log.Printf("    Description: %s", tmpl.Metadata.Description)
		}
		log.Printf("    Region: %s | Nodes: %s | Type: %s",
			tmpl.Spec.Region,
			strings.Join(tmpl.Spec.Nodes, ", "),
			tmpl.Spec.ClusterType)
		if len(tmpl.Spec.Workflows.OnCreated) > 0 {
			log.Printf("    OnCreated Workflows: %s", strings.Join(tmpl.Spec.Workflows.OnCreated, ", "))
		}
		if len(tmpl.Spec.Workflows.OnDestroy) > 0 {
			log.Printf("    OnDestroy Workflows: %s", strings.Join(tmpl.Spec.Workflows.OnDestroy, ", "))
		}
		if tmpl.Spec.Schedule != "" {
			log.Printf("    Expiry Schedule: %s", tmpl.Spec.Schedule)
		}
		log.Println()
	}

	log.Println("💡 Execute a template with:")
	log.Println("  hyve template execute <template-name> <cluster-name>")
}

func deleteTemplate(name string) {
	ctx := context.Background()
	shared.SyncRepoState(ctx)

	// Get repository path
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	// Create template manager
	templateMgr := template.NewManager(currentRepo.LocalPath)

	// Delete template
	if err := templateMgr.DeleteTemplate(name); err != nil {
		log.Fatalf("Failed to delete template: %v", err)
	}

	log.Printf("✅ Template '%s' deleted successfully", name)

	// Commit and push deletion to Git
	authUsername, authToken := shared.GetAuthCredentials(currentRepo)
	stateMgr, err := state.NewManager(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Printf("⚠️  Warning: Failed to create state manager: %v", err)
		log.Println("💡 Template deleted locally but not pushed to git")
	} else {
		shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Delete template %s", name))
	}
}

func showTemplate(name string) {
	shared.SyncRepoState(context.Background())

	// Get repository path
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	// Create template manager
	templateMgr := template.NewManager(currentRepo.LocalPath)

	// Get template
	tmpl, err := templateMgr.GetTemplate(name)
	if err != nil {
		log.Fatalf("Failed to get template: %v", err)
	}

	// Marshal to YAML for display
	data, err := yaml.Marshal(tmpl)
	if err != nil {
		log.Fatalf("Failed to marshal template: %v", err)
	}

	log.Printf("📋 Template: %s\n", name)
	log.Println(string(data))
}

func executeTemplate(templateName, clusterName, org, account, vpcName, eksRole, nodeRole, subscription, resourceGroup, project string) {
	ctx := context.Background()
	shared.SyncRepoState(ctx)

	// Get repository path
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	// Get authentication
	authUsername, authToken := shared.GetAuthCredentials(currentRepo)

	// Create template manager
	templateMgr := template.NewManager(currentRepo.LocalPath)

	log.Printf("🚀 Executing template '%s' to create cluster '%s'...\n", templateName, clusterName)

	// Execute template (get cluster definition)
	tmpl, clusterDef, err := templateMgr.ExecuteTemplate(ctx, templateName, clusterName)
	if err != nil {
		log.Fatalf("Failed to execute template: %v", err)
	}

	// Apply provider-specific account flags: a flag overrides the template value;
	// if neither the template nor the flag supplies a value, execution fails.
	resolve := func(flagVal, templateVal, flag, hint string) string {
		if flagVal != "" {
			return flagVal
		}
		if templateVal != "" {
			return templateVal
		}
		log.Fatalf("Missing required value: set %s in the template or pass --%s. %s", flag, flag, hint)
		return ""
	}

	switch strings.ToLower(tmpl.Spec.Provider) {
	case "civo":
		clusterDef.Spec.CivoOrganization = resolve(org, clusterDef.Spec.CivoOrganization,
			"org", "Use 'hyve config civo org list' to see available organizations.")
	case "aws":
		clusterDef.Spec.AWSAccount = resolve(account, clusterDef.Spec.AWSAccount,
			"account", "Use 'hyve config aws account list' to see available accounts.")
		clusterDef.Spec.AWSVPCName = resolve(vpcName, clusterDef.Spec.AWSVPCName,
			"vpc-name", fmt.Sprintf("Use 'hyve config aws vpc list --account %s'.", clusterDef.Spec.AWSAccount))
		clusterDef.Spec.AWSEKSRole = resolve(eksRole, clusterDef.Spec.AWSEKSRole,
			"eks-role", fmt.Sprintf("Use 'hyve config aws eks-role list --account %s'.", clusterDef.Spec.AWSAccount))
		clusterDef.Spec.AWSNodeRole = resolve(nodeRole, clusterDef.Spec.AWSNodeRole,
			"node-role", fmt.Sprintf("Use 'hyve config aws node-role list --account %s'.", clusterDef.Spec.AWSAccount))
	case "azure":
		clusterDef.Spec.AzureSubscription = resolve(subscription, clusterDef.Spec.AzureSubscription,
			"subscription", "Use 'hyve config azure subscription list' to see available subscriptions.")
		clusterDef.Spec.AzureResourceGroup = resolve(resourceGroup, clusterDef.Spec.AzureResourceGroup,
			"resource-group", "")
	case "gcp":
		clusterDef.Spec.GCPProject = resolve(project, clusterDef.Spec.GCPProject,
			"project", "Use 'hyve config gcp project list' to see available projects.")
	}

	// Derive expiresAt from the template's cron schedule, if set.
	if tmpl.Spec.Schedule != "" {
		next, err := shared.CronNextOccurrence(tmpl.Spec.Schedule, time.Now())
		if err != nil {
			log.Fatalf("Failed to evaluate schedule %q: %v", tmpl.Spec.Schedule, err)
		}
		clusterDef.Spec.ExpiresAt = next.Format(time.RFC3339)
	}

	log.Println("📋 Template Details:")
	log.Printf("  Provider: %s", tmpl.Spec.Provider)
	log.Printf("  Region: %s", tmpl.Spec.Region)
	log.Printf("  Nodes: %s", strings.Join(tmpl.Spec.Nodes, ", "))
	log.Printf("  Cluster Type: %s", tmpl.Spec.ClusterType)
	if len(tmpl.Spec.Workflows.OnCreated) > 0 {
		log.Printf("  OnCreated Workflows: %s", strings.Join(tmpl.Spec.Workflows.OnCreated, ", "))
	}
	if len(tmpl.Spec.Workflows.OnDestroy) > 0 {
		log.Printf("  OnDestroy Workflows: %s", strings.Join(tmpl.Spec.Workflows.OnDestroy, ", "))
	}
	if tmpl.Spec.Schedule != "" {
		log.Printf("  Expiry Schedule: %s → %s", tmpl.Spec.Schedule, clusterDef.Spec.ExpiresAt)
	}

	// Resolve AWS aliases to actual IDs/ARNs before writing the cluster definition.
	if strings.ToLower(clusterDef.Spec.Provider) == "aws" && clusterDef.Spec.AWSAccount != "" {
		pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
		accountName := clusterDef.Spec.AWSAccount

		if clusterDef.Spec.AWSVPCName != "" && clusterDef.Spec.AWSVPCID == "" {
			vpcID, err := pcMgr.GetAWSVPCID(accountName, clusterDef.Spec.AWSVPCName)
			if err != nil {
				log.Fatalf("AWS VPC '%s' not found in account '%s': %v", clusterDef.Spec.AWSVPCName, accountName, err)
			}
			clusterDef.Spec.AWSVPCID = vpcID
			log.Printf("  Resolved VPC '%s' → %s", clusterDef.Spec.AWSVPCName, vpcID)
		}

		if clusterDef.Spec.AWSEKSRole != "" && clusterDef.Spec.AWSEKSRoleARN == "" {
			roleARN, err := pcMgr.GetAWSEKSRoleARN(accountName, clusterDef.Spec.AWSEKSRole)
			if err != nil {
				log.Fatalf("AWS EKS role '%s' not found in account '%s': %v", clusterDef.Spec.AWSEKSRole, accountName, err)
			}
			clusterDef.Spec.AWSEKSRoleARN = roleARN
			log.Printf("  Resolved EKS role '%s' → %s", clusterDef.Spec.AWSEKSRole, roleARN)
		}

		if clusterDef.Spec.AWSNodeRole != "" && clusterDef.Spec.AWSNodeRoleARN == "" {
			roleARN, err := pcMgr.GetAWSNodeRoleARN(accountName, clusterDef.Spec.AWSNodeRole)
			if err != nil {
				log.Fatalf("AWS node role '%s' not found in account '%s': %v", clusterDef.Spec.AWSNodeRole, accountName, err)
			}
			clusterDef.Spec.AWSNodeRoleARN = roleARN
			log.Printf("  Resolved node role '%s' → %s", clusterDef.Spec.AWSNodeRole, roleARN)
		}
	}

	// Save cluster definition to clusters directory
	clustersDir := filepath.Join(currentRepo.LocalPath, "clusters")
	if err := os.MkdirAll(clustersDir, 0755); err != nil {
		log.Fatalf("Failed to create clusters directory: %v", err)
	}

	clusterPath := filepath.Join(clustersDir, clusterName+".yaml")

	// Marshal cluster definition
	clusterData, err := yaml.Marshal(clusterDef)
	if err != nil {
		log.Fatalf("Failed to marshal cluster definition: %v", err)
	}

	if err := os.WriteFile(clusterPath, clusterData, 0644); err != nil {
		log.Fatalf("Failed to write cluster file: %v", err)
	}

	log.Printf("\n✅ Cluster definition created: %s", clusterPath)

	// Commit and push cluster definition to Git
	stateMgr, err := state.NewManager(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Printf("⚠️  Warning: Failed to create state manager: %v", err)
		log.Println("💡 Cluster definition saved locally but not pushed to git")
	} else {
		shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Create cluster %s from template %s", clusterName, templateName))
	}

	// Reconcile — same path as `hyve cluster add`, supports CI/CD mode.
	log.Println("\n1️⃣ Reconciling cluster (create + wait for ready)...")
	shared.RunReconciliation("")
	log.Printf("✅ Cluster '%s' is ready", clusterName)

	// Sync kubeconfig before running workflows
	log.Println("\n2️⃣ Syncing kubeconfig...")
	prov, provErr := cluster.CreateProviderForClusterDef(*clusterDef)
	if provErr != nil {
		log.Printf("⚠️  Warning: Failed to create provider: %v", provErr)
		log.Println("Workflows may fail without valid kubeconfig")
	} else {
		clusterInfo, ciErr := prov.GetClusterInfo(ctx, clusterName)
		if ciErr != nil {
			log.Printf("⚠️  Warning: Failed to get cluster info: %v", ciErr)
			log.Println("Workflows may fail without valid kubeconfig")
		} else if clusterInfo.Kubeconfig == "" {
			log.Printf("⚠️  Warning: Kubeconfig not yet available for cluster '%s', skipping storage", clusterName)
		} else {
			kubeconfigMgr, kcErr := kubeconfig.NewManager(currentRepo.Name)
			if kcErr != nil {
				log.Printf("⚠️  Warning: Failed to create kubeconfig manager: %v", kcErr)
			} else {
				defer kubeconfigMgr.Close()
				if _, err := kubeconfigMgr.StoreKubeconfig(clusterName, clusterInfo.Kubeconfig); err != nil {
					log.Printf("⚠️  Warning: Failed to store kubeconfig: %v", err)
				} else {
					log.Printf("✅ Kubeconfig synced and stored for cluster '%s'", clusterName)
				}
			}
		}
	}

	log.Printf("\n✅ Template execution completed!")
	log.Printf("\n💡 Cluster '%s' is now available", clusterName)
	log.Println("💡 Use 'hyve kubeconfig sync' to get the kubeconfig")
}

func validateTemplate(name string) {
	// Get repository path
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	// Create template manager
	templateMgr := template.NewManager(currentRepo.LocalPath)

	// Get template
	tmpl, err := templateMgr.GetTemplate(name)
	if err != nil {
		log.Fatalf("Failed to get template: %v", err)
	}

	log.Printf("🔍 Validating template '%s'...\n", name)

	errors := []string{}
	warnings := []string{}

	// Validate required fields
	if tmpl.APIVersion == "" {
		errors = append(errors, "Missing apiVersion")
	} else if tmpl.APIVersion != "v1" {
		warnings = append(warnings, fmt.Sprintf("Unexpected apiVersion '%s', expected 'v1'", tmpl.APIVersion))
	}

	if tmpl.Kind == "" {
		errors = append(errors, "Missing kind")
	} else if tmpl.Kind != "Template" {
		errors = append(errors, fmt.Sprintf("Invalid kind '%s', expected 'Template'", tmpl.Kind))
	}

	if tmpl.Metadata.Name == "" {
		errors = append(errors, "Missing metadata.name")
	}

	// Validate spec fields
	if tmpl.Spec.Provider == "" {
		errors = append(errors, "Missing spec.provider")
	} else {
		validProviders := []string{"civo", "aws", "gcp", "azure"}
		isValid := false
		for _, p := range validProviders {
			if tmpl.Spec.Provider == p {
				isValid = true
				break
			}
		}
		if !isValid {
			warnings = append(warnings, fmt.Sprintf("Provider '%s' may not be supported. Valid providers: %s", tmpl.Spec.Provider, strings.Join(validProviders, ", ")))
		}
	}

	if tmpl.Spec.Region == "" {
		errors = append(errors, "Missing spec.region")
	} else if tmpl.Spec.Provider == "civo" {
		// Validate Civo regions
		validRegions := []string{"PHX1", "NYC1", "FRA1", "LON1"}
		isValid := false
		for _, r := range validRegions {
			if tmpl.Spec.Region == r {
				isValid = true
				break
			}
		}
		if !isValid {
			warnings = append(warnings, fmt.Sprintf("Region '%s' may not be valid for Civo. Valid regions: %s", tmpl.Spec.Region, strings.Join(validRegions, ", ")))
		}
	}

	if len(tmpl.Spec.Nodes) == 0 {
		errors = append(errors, "Missing spec.nodes (at least one node required)")
	} else {
		// Validate node sizes for Civo
		if tmpl.Spec.Provider == "civo" {
			validNodeSizes := []string{
				"g4s.kube.xsmall",
				"g4s.kube.small",
				"g4s.kube.medium",
				"g4s.kube.large",
				"g4s.kube.xlarge",
			}
			for _, node := range tmpl.Spec.Nodes {
				isValid := false
				for _, size := range validNodeSizes {
					if node == size {
						isValid = true
						break
					}
				}
				if !isValid {
					warnings = append(warnings, fmt.Sprintf("Node size '%s' may not be valid for Civo", node))
				}
			}
		}
	}

	if tmpl.Spec.ClusterType == "" {
		warnings = append(warnings, "Missing spec.clusterType, defaulting to 'k3s'")
	} else {
		validTypes := []string{"k3s", "talos"}
		isValid := false
		for _, t := range validTypes {
			if tmpl.Spec.ClusterType == t {
				isValid = true
				break
			}
		}
		if !isValid {
			warnings = append(warnings, fmt.Sprintf("Cluster type '%s' may not be supported. Valid types: %s", tmpl.Spec.ClusterType, strings.Join(validTypes, ", ")))
		}
	}

	// Validate workflows exist
	allWorkflows := append(tmpl.Spec.Workflows.OnCreated, tmpl.Spec.Workflows.OnDestroy...)
	if len(allWorkflows) > 0 {
		workflowMgr, err := workflow.NewManager(shared.GetLocalPath())
		if err == nil {
			availableWorkflows, err := workflowMgr.ListWorkflows()
			if err == nil {
				workflowMap := make(map[string]bool)
				for _, wf := range availableWorkflows {
					workflowMap[wf.Metadata.Name] = true
				}

				for _, wfName := range tmpl.Spec.Workflows.OnCreated {
					if !workflowMap[wfName] {
						warnings = append(warnings, fmt.Sprintf("OnCreated workflow '%s' not found in repository", wfName))
					}
				}
				for _, wfName := range tmpl.Spec.Workflows.OnDestroy {
					if !workflowMap[wfName] {
						warnings = append(warnings, fmt.Sprintf("OnDestroy workflow '%s' not found in repository", wfName))
					}
				}
			}
		}
	}

	// Print results
	if len(errors) > 0 {
		log.Println("\n❌ Validation Failed")
		log.Println("\nErrors:")
		for _, err := range errors {
			log.Printf("  • %s", err)
		}
	}

	if len(warnings) > 0 {
		log.Println("\n⚠️  Warnings:")
		for _, warn := range warnings {
			log.Printf("  • %s", warn)
		}
	}

	if len(errors) == 0 {
		log.Println("\n✅ Template is valid")
		log.Printf("📋 Provider: %s", tmpl.Spec.Provider)
		log.Printf("📋 Region: %s", tmpl.Spec.Region)
		log.Printf("📋 Nodes: %d (%s)", len(tmpl.Spec.Nodes), strings.Join(tmpl.Spec.Nodes, ", "))
		log.Printf("📋 Cluster Type: %s", tmpl.Spec.ClusterType)
		log.Printf("📋 Ingress: %v", tmpl.Spec.Ingress.Enabled)
		if len(tmpl.Spec.Workflows.OnCreated) > 0 {
			log.Printf("📋 OnCreated Workflows: %d (%s)", len(tmpl.Spec.Workflows.OnCreated), strings.Join(tmpl.Spec.Workflows.OnCreated, ", "))
		} else {
			log.Printf("📋 OnCreated Workflows: none")
		}
		if len(tmpl.Spec.Workflows.OnDestroy) > 0 {
			log.Printf("📋 OnDestroy Workflows: %d (%s)", len(tmpl.Spec.Workflows.OnDestroy), strings.Join(tmpl.Spec.Workflows.OnDestroy, ", "))
		} else {
			log.Printf("📋 OnDestroy Workflows: none")
		}

		if len(warnings) == 0 {
			log.Println("✨ No warnings")
		}
	} else {
		os.Exit(1)
	}
}
