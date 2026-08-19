package state

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/workflow"
)

var migrateWrite bool
var migrateSkipExisting bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate the active local state directory's templates/workflows/clusters into the cluster",
	Long: `Walks templates/, workflows/, then clusters/ in the active local state
directory (see 'hyve state') and creates each as a CR via the API —
requires an active 'hyve login' session (see 'hyve login'). Order matters:
templates and workflows are migrated first, so a migrated cluster's
lifecycle-hook refs resolve against the just-created Workflow CRs (see
internal/controller's CR-first WorkflowRef resolution) instead of a stale
fallback.

Defaults to a dry run (prints what would be created, writes nothing). Pass
--write to actually create resources. Safe to re-run: --skip-existing
(the default) treats "already exists" as success, not a failure, so a
partial migration can just be re-run.`,
	Run: func(cmd *cobra.Command, args []string) {
		runMigrate()
	},
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateWrite, "write", false, "Actually create resources (default is a dry run)")
	migrateCmd.Flags().BoolVar(&migrateSkipExisting, "skip-existing", true, "Treat an already-exists response as success, not a failure — safe to re-run")
	Cmd.AddCommand(migrateCmd)
}

func runMigrate() {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("`hyve state migrate` requires an active cluster-mode session — run `hyve login` first")
	}
	client := shared.NewAPIClient(sess)

	ctx := context.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)
	localPath := stateMgr.LocalPath()

	dryRun := !migrateWrite
	if dryRun {
		log.Println("🔍 Dry run — nothing will be written. Pass --write to actually migrate.")
	}

	workflowCount := migrateWorkflows(client, localPath, dryRun)
	templateCount := migrateTemplates(client, localPath, dryRun)
	clusterCount := migrateClusters(client, stateMgr, dryRun)

	verb := "Migrated"
	if dryRun {
		verb = "Would migrate"
	}
	log.Printf("\n%s: %d template(s), %d workflow(s), %d cluster(s)", verb, templateCount, workflowCount, clusterCount)
	if dryRun {
		log.Println("Re-run with --write to actually create these.")
	}
}

// isAlreadyExists reports whether err looks like the API's 409 Conflict
// "already exists" response — the only case --skip-existing treats as
// success rather than a failure.
func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

func migrateWorkflows(client *shared.APIClient, localPath string, dryRun bool) int {
	mgr, err := workflow.NewManager(localPath)
	if err != nil {
		log.Fatalf("Failed to load workflows: %v", err)
	}
	workflows, err := mgr.ListWorkflows()
	if err != nil {
		log.Fatalf("Failed to list workflows: %v", err)
	}

	count := 0
	for _, wf := range workflows {
		name := wf.Metadata.Name
		if dryRun {
			log.Printf("  [workflow] %s", name)
			count++
			continue
		}
		data, err := json.Marshal(workflowSpecWithDescription{WorkflowSpec: wf.Spec, Description: wf.Metadata.Description})
		if err != nil {
			log.Fatalf("Failed to marshal workflow %q: %v", name, err)
		}
		if _, err := client.CreateWorkflow(name, data); err != nil {
			if migrateSkipExisting && isAlreadyExists(err) {
				log.Printf("  [workflow] %s — already exists, skipped", name)
				continue
			}
			log.Fatalf("Failed to create workflow %q: %v", name, err)
		}
		log.Printf("  [workflow] %s — created", name)
		count++
	}
	return count
}

type workflowSpecWithDescription struct {
	workflow.WorkflowSpec
	Description string `json:"description,omitempty"`
}

func migrateTemplates(client *shared.APIClient, localPath string, dryRun bool) int {
	mgr := template.NewManager(localPath)
	templates, err := mgr.ListTemplates()
	if err != nil {
		log.Fatalf("Failed to list templates: %v", err)
	}

	count := 0
	for _, tpl := range templates {
		name := tpl.Metadata.Name
		if dryRun {
			log.Printf("  [template] %s", name)
			count++
			continue
		}
		data, err := json.Marshal(templateSpecWithDescription{TemplateSpec: tpl.Spec, Description: tpl.Metadata.Description})
		if err != nil {
			log.Fatalf("Failed to marshal template %q: %v", name, err)
		}
		if _, err := client.CreateTemplate(name, data); err != nil {
			if migrateSkipExisting && isAlreadyExists(err) {
				log.Printf("  [template] %s — already exists, skipped", name)
				continue
			}
			log.Fatalf("Failed to create template %q: %v", name, err)
		}
		log.Printf("  [template] %s — created", name)
		count++
	}
	return count
}

type templateSpecWithDescription struct {
	template.TemplateSpec
	Description string `json:"description,omitempty"`
}

func migrateClusters(client *shared.APIClient, stateMgr *state.Manager, dryRun bool) int {
	defs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to list clusters: %v", err)
	}

	count := 0
	for _, def := range defs {
		if def.Spec.Delete {
			continue
		}
		name := def.Metadata.Name
		if dryRun {
			log.Printf("  [cluster]  %s", name)
			count++
			continue
		}
		data, err := json.Marshal(crdconv.FromTypesClusterDefinitionSpec(&def))
		if err != nil {
			log.Fatalf("Failed to marshal cluster %q: %v", name, err)
		}
		if _, err := client.CreateCluster(name, data); err != nil {
			if migrateSkipExisting && isAlreadyExists(err) {
				log.Printf("  [cluster]  %s — already exists, skipped", name)
				continue
			}
			log.Fatalf("Failed to create cluster %q: %v", name, err)
		}
		log.Printf("  [cluster]  %s — created", name)
		count++
	}
	return count
}
