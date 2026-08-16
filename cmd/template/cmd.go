package template

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
)

// Cmd is the root template command exposed to the parent.
var Cmd = templateCmd

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage cluster templates",
	Long:  "Create, list, delete, and validate cluster templates with associated workflows. To create a cluster from a template, see `hyve cluster create`.",
}

var templateCreateCmd = &cobra.Command{
	Use:   "create [template-name]",
	Short: "Create a new cluster template",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		description, _ := cmd.Flags().GetString("description")
		driverSource, _ := cmd.Flags().GetString("driver")
		driverVersion, _ := cmd.Flags().GetString("driver-version")
		region, _ := cmd.Flags().GetString("region")
		setVals, _ := cmd.Flags().GetStringArray("set")
		beforeCreate, _ := cmd.Flags().GetString("before-create")
		onCreate, _ := cmd.Flags().GetString("on-create")
		onDelete, _ := cmd.Flags().GetString("on-delete")
		afterDelete, _ := cmd.Flags().GetString("after-delete")
		schedule, _ := cmd.Flags().GetString("schedule")
		lockParams, _ := cmd.Flags().GetBool("lock-params")

		if driverSource == "" {
			log.Fatal("--driver is required")
		}

		params := map[string]string{}
		for _, kv := range setVals {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				log.Fatalf("Invalid --set value %q (expected KEY=VALUE)", kv)
			}
			params[parts[0]] = parts[1]
		}

		createTemplate(templateName, description, driverSource, driverVersion, region, params,
			beforeCreate, onCreate, onDelete, afterDelete, schedule, lockParams)
	},
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cluster templates",
	Run: func(cmd *cobra.Command, args []string) {
		listTemplates()
	},
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete [template-name]",
	Short: "Delete a cluster template",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deleteTemplate(args[0])
	},
}

var templateShowCmd = &cobra.Command{
	Use:   "show [template-name]",
	Short: "Show template details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		showTemplate(args[0])
	},
}

var templateValidateCmd = &cobra.Command{
	Use:   "validate [template-name]",
	Short: "Validate a template",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		validateTemplate(args[0])
	},
}

func init() {
	templateCreateCmd.Flags().StringP("description", "d", "", "Template description")
	templateCreateCmd.Flags().String("driver", "", "Module source (e.g. github.com/hyve-modules/aws-eks)")
	templateCreateCmd.Flags().String("driver-version", "latest", "Module version (semver constraint, tag, or commit)")
	templateCreateCmd.Flags().StringP("region", "r", "", "Default region for clusters created from this template")
	templateCreateCmd.Flags().StringArray("set", nil, "Default driver params (repeatable): KEY=VALUE")
	templateCreateCmd.Flags().String("before-create", "", "Workflows to run before cluster creation (comma-separated)")
	templateCreateCmd.Flags().StringP("on-create", "c", "", "Workflows to run after cluster creation (comma-separated)")
	templateCreateCmd.Flags().String("on-delete", "", "Workflows to run before cluster destruction (comma-separated)")
	templateCreateCmd.Flags().String("after-delete", "", "Workflows to run after cluster deletion (comma-separated)")
	templateCreateCmd.Flags().String("schedule", "", "Cron expression for cluster expiry (e.g. '0 20 * * 5')")
	templateCreateCmd.Flags().Bool("lock-params", false, "Prevent users from overriding default params when creating a cluster from this template")

	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateDeleteCmd)
	templateCmd.AddCommand(templateShowCmd)
	templateCmd.AddCommand(templateValidateCmd)
}

func createTemplate(
	name, description, driverSource, driverVersion, region string,
	params map[string]string,
	beforeCreateStr, onCreateStr, onDeleteStr, afterDeleteStr string,
	schedule string,
	lockParams bool,
) {
	ctx := context.Background()

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

	templateMgr := template.NewManager(currentRepo.LocalPath)

	parseWorkflows := func(s string) []types.WorkflowRef {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		out := make([]types.WorkflowRef, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, types.WorkflowRef{Name: p})
			}
		}
		return out
	}

	tmpl := &template.Template{
		APIVersion: "v1",
		Kind:       "Template",
		Metadata: template.TemplateMetadata{
			Name:        name,
			Description: description,
		},
		Spec: template.TemplateSpec{
			Driver: template.TemplateDriverRef{Source: driverSource, Version: driverVersion},
			Region: region,
			Params: params,
			Workflows: template.TemplateWorkflowsSpec{
				BeforeCreate: parseWorkflows(beforeCreateStr),
				OnCreate:     parseWorkflows(onCreateStr),
				OnDelete:     parseWorkflows(onDeleteStr),
				AfterDelete:  parseWorkflows(afterDeleteStr),
			},
			Schedule:   schedule,
			LockParams: lockParams,
		},
	}

	if err := templateMgr.CreateTemplate(tmpl); err != nil {
		log.Fatalf("Failed to create template: %v", err)
	}

	log.Printf("✅ Template '%s' created successfully", name)

	stateMgr := state.NewManagerFromPath(filepath.Join(currentRepo.LocalPath, "clusters"))
	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Create template %s", name))
	log.Printf("Template path: %s", templateMgr.GetTemplatePath(name))
	log.Println("\n📋 Template Details:")
	log.Printf("  Driver: %s@%s", driverSource, driverVersion)
	if region != "" {
		log.Printf("  Region: %s", region)
	}
	if len(params) > 0 {
		log.Println("  Params:")
		for k, v := range params {
			log.Printf("    %s=%s", k, v)
		}
	}
	if wf := tmpl.Spec.Workflows.BeforeCreate; len(wf) > 0 {
		log.Printf("  BeforeCreate: %s", shared.JoinWorkflowRefs(wf))
	}
	if wf := tmpl.Spec.Workflows.OnCreate; len(wf) > 0 {
		log.Printf("  OnCreate: %s", shared.JoinWorkflowRefs(wf))
	}
	if wf := tmpl.Spec.Workflows.AfterCreate; len(wf) > 0 {
		log.Printf("  AfterCreate: %s", shared.JoinWorkflowRefs(wf))
	}
	if wf := tmpl.Spec.Workflows.OnDelete; len(wf) > 0 {
		log.Printf("  OnDelete: %s", shared.JoinWorkflowRefs(wf))
	}
	if wf := tmpl.Spec.Workflows.AfterDelete; len(wf) > 0 {
		log.Printf("  AfterDelete: %s", shared.JoinWorkflowRefs(wf))
	}
	if schedule != "" {
		log.Printf("  Expiry Schedule: %s", schedule)
	}

	log.Println("\n💡 Create a cluster from this template with:")
	log.Printf("  hyve cluster create <cluster-name> --template %s", name)
}

func listTemplates() {
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

	templateMgr := template.NewManager(currentRepo.LocalPath)
	templates, err := templateMgr.ListTemplates()
	if err != nil {
		log.Fatalf("Failed to list templates: %v", err)
	}

	if len(templates) == 0 {
		log.Println("No templates found")
		log.Println("\n💡 Create a template with:")
		log.Println("  hyve template create <name> --driver <source> --driver-version <ver>")
		return
	}

	log.Printf("📋 Available templates (%d):\n", len(templates))
	for _, tmpl := range templates {
		log.Printf("  %s  (file: %s)", tmpl.Metadata.Name, tmpl.Filename)
		if tmpl.Metadata.Description != "" {
			log.Printf("    Description: %s", tmpl.Metadata.Description)
		}
		log.Printf("    Driver: %s@%s", tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version)
		if tmpl.Spec.Region != "" {
			log.Printf("    Region: %s", tmpl.Spec.Region)
		}
		if len(tmpl.Spec.Workflows.OnCreate) > 0 {
			log.Printf("    OnCreate: %s", shared.JoinWorkflowRefs(tmpl.Spec.Workflows.OnCreate))
		}
		if len(tmpl.Spec.Workflows.AfterCreate) > 0 {
			log.Printf("    AfterCreate: %s", shared.JoinWorkflowRefs(tmpl.Spec.Workflows.AfterCreate))
		}
		if len(tmpl.Spec.Workflows.OnDelete) > 0 {
			log.Printf("    OnDelete: %s", shared.JoinWorkflowRefs(tmpl.Spec.Workflows.OnDelete))
		}
		if tmpl.Spec.Schedule != "" {
			log.Printf("    Expiry Schedule: %s", tmpl.Spec.Schedule)
		}
		log.Println()
	}

	log.Println("💡 Create a cluster from a template with:")
	log.Println("  hyve cluster create <cluster-name> --template <template-name>")
}

func deleteTemplate(name string) {
	ctx := context.Background()

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

	templateMgr := template.NewManager(currentRepo.LocalPath)

	if err := templateMgr.DeleteTemplate(name); err != nil {
		log.Fatalf("Failed to delete template: %v", err)
	}

	log.Printf("✅ Template '%s' deleted successfully", name)

	stateMgr := state.NewManagerFromPath(filepath.Join(currentRepo.LocalPath, "clusters"))
	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Delete template %s", name))
}

func showTemplate(name string) {
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

	templateMgr := template.NewManager(currentRepo.LocalPath)

	tmpl, err := templateMgr.GetTemplate(name)
	if err != nil {
		log.Fatalf("Failed to get template: %v", err)
	}

	data, err := yaml.Marshal(tmpl)
	if err != nil {
		log.Fatalf("Failed to marshal template: %v", err)
	}

	log.Printf("📋 Template: %s\n", name)
	log.Println(string(data))
}

func validateTemplate(name string) {
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

	templateMgr := template.NewManager(currentRepo.LocalPath)

	tmpl, err := templateMgr.GetTemplate(name)
	if err != nil {
		log.Fatalf("Failed to get template: %v", err)
	}

	log.Printf("🔍 Validating template '%s'...\n", name)

	errors, warnings := template.Validate(currentRepo.LocalPath, tmpl)

	if len(errors) > 0 {
		log.Println("\n❌ Validation Failed")
		log.Println("\nErrors:")
		for _, e := range errors {
			log.Printf("  • %s", e)
		}
	}
	if len(warnings) > 0 {
		log.Println("\n⚠️  Warnings:")
		for _, w := range warnings {
			log.Printf("  • %s", w)
		}
	}
	if len(errors) == 0 {
		log.Println("\n✅ Template is valid")
		log.Printf("📋 Driver: %s@%s", tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version)
		if tmpl.Spec.Region != "" {
			log.Printf("📋 Region: %s", tmpl.Spec.Region)
		}
		if len(warnings) == 0 {
			log.Println("✨ No warnings")
		}
	} else {
		os.Exit(1)
	}
}
