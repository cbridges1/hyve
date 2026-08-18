package cluster

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
)

var createCmd = &cobra.Command{
	Use:   "create [cluster-name]",
	Short: "Create a cluster from a template (local mode) or a file (cluster mode)",
	Long: `Local mode (no 'hyve login' session active): creates from a template via
--template, exactly as before.

Cluster mode (a valid 'hyve login' session exists): --template isn't
supported — the API has no server-side template rendering — use --file
pointing at an already-fully-specified cluster definition instead (the
same apiVersion/kind/metadata/spec YAML shape a local clusters/<name>.yaml
already uses).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		templateName, _ := cmd.Flags().GetString("template")
		region, _ := cmd.Flags().GetString("region")
		setVals, _ := cmd.Flags().GetStringArray("set")
		file, _ := cmd.Flags().GetString("file")

		if sess, ok := shared.UseClusterMode(); ok {
			if file == "" {
				log.Fatal("--file is required in cluster mode (a 'hyve login' session is active) — templates aren't supported over the API yet")
			}
			createClusterFromFileAPI(shared.NewAPIClient(sess), file)
			return
		}

		if templateName == "" {
			log.Fatal("--template is required")
		}

		overrides := map[string]string{}
		for _, kv := range setVals {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				log.Fatalf("Invalid --set value %q (expected KEY=VALUE)", kv)
			}
			overrides[parts[0]] = parts[1]
		}

		createClusterFromTemplate(templateName, clusterName, region, overrides)
	},
}

func init() {
	createCmd.Flags().StringP("template", "t", "", "Template to create the cluster from (local mode; required unless --file is given)")
	createCmd.Flags().StringP("region", "r", "", "Override the template's default region (local mode)")
	createCmd.Flags().StringArray("set", nil, "Override driver params (repeatable): KEY=VALUE (local mode)")
	createCmd.Flags().StringP("file", "f", "", "Path to a cluster definition YAML file (cluster mode; required instead of --template)")
}

func createClusterFromTemplate(templateName, clusterName, region string, overrides map[string]string) {
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

	stateMgr := state.NewManagerFromPath(filepath.Join(currentRepo.LocalPath, "clusters"))

	templateMgr := template.NewManager(currentRepo.LocalPath)

	log.Printf("🚀 Creating cluster '%s' from template '%s'...\n", clusterName, templateName)

	tmpl, clusterDef, err := templateMgr.ExecuteTemplate(ctx, templateName, clusterName, region, overrides)
	if err != nil {
		log.Fatalf("Failed to create cluster from template: %v", err)
	}

	if tmpl.Spec.Schedule != "" {
		next, err := template.CronNextOccurrence(tmpl.Spec.Schedule, time.Now())
		if err != nil {
			log.Fatalf("Failed to evaluate schedule %q: %v", tmpl.Spec.Schedule, err)
		}
		clusterDef.Spec.ExpiresAt = next.Format(time.RFC3339)
	}

	log.Println("📋 Template Details:")
	log.Printf("  Driver: %s@%s", tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version)
	log.Printf("  Region: %s", clusterDef.Metadata.Region)
	if len(clusterDef.Spec.Params) > 0 {
		log.Println("  Params:")
		for k, v := range clusterDef.Spec.Params {
			log.Printf("    %s=%s", k, v)
		}
	}
	if len(tmpl.Spec.Workflows.OnCreate) > 0 {
		log.Printf("  OnCreate Workflows: %s", shared.JoinWorkflowRefs(tmpl.Spec.Workflows.OnCreate))
	}
	if len(tmpl.Spec.Workflows.AfterCreate) > 0 {
		log.Printf("  AfterCreate Workflows: %s", shared.JoinWorkflowRefs(tmpl.Spec.Workflows.AfterCreate))
	}
	if len(tmpl.Spec.Workflows.OnDelete) > 0 {
		log.Printf("  OnDelete Workflows: %s", shared.JoinWorkflowRefs(tmpl.Spec.Workflows.OnDelete))
	}
	if tmpl.Spec.Schedule != "" {
		log.Printf("  Expiry Schedule: %s → %s", tmpl.Spec.Schedule, clusterDef.Spec.ExpiresAt)
	}

	if err := stateMgr.SaveClusterDefinition(clusterDef); err != nil {
		log.Fatalf("Failed to write cluster definition: %v", err)
	}

	log.Printf("\n✅ Cluster definition created: %s", filepath.Join(currentRepo.LocalPath, "clusters", clusterName+".yaml"))

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Create cluster %s from template %s", clusterName, templateName))

	log.Println("\n1️⃣ Reconciling cluster...")
	shared.RunReconciliation("", false)
	log.Printf("\n✅ Cluster creation completed for '%s'", clusterName)
}
