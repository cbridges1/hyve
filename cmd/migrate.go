package cmd

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/workflow"
)

var (
	migrateDir          string
	migrateFile         string
	migrateWrite        bool
	migrateSkipExisting bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate [path]",
	Short: "Push local templates/workflows/clusters into the active environment's cluster",
	Long: `Reads CR-shaped YAML — a single file (--file, or a file given as [path]) or a
directory tree (--dir, or a directory given as [path], walking its
templates/, workflows/, then clusters/ subdirectories in that order so a
migrated cluster's lifecycle-hook refs resolve against the just-created
Workflow CRs instead of a stale fallback) — and creates each as a CR via
the API.

Requires an active environment with cluster-mode credentials (see 'hyve
login'). The destination is always whichever cluster that environment is
logged into — independent of the source path, which is never implicitly
tied to any environment's registered directory.

Defaults to the current working directory (directory mode) when no path,
--dir, or --file is given.

Defaults to a dry run (prints what would be created, writes nothing). Pass
--write to actually create resources. Safe to re-run: --skip-existing
(the default) treats "already exists" as success, not a failure, so a
partial migration can just be re-run.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := ""
		if len(args) > 0 {
			path = args[0]
		}
		runMigrate(path)
	},
}

func init() {
	migrateCmd.Flags().StringVar(&migrateDir, "dir", "", "Directory to migrate (walks templates/, workflows/, clusters/ subdirectories)")
	migrateCmd.Flags().StringVar(&migrateFile, "file", "", "Single CR-shaped YAML file to migrate")
	migrateCmd.Flags().BoolVar(&migrateWrite, "write", false, "Actually create resources (default is a dry run)")
	migrateCmd.Flags().BoolVar(&migrateSkipExisting, "skip-existing", true, "Treat an already-exists response as success, not a failure — safe to re-run")
	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(posPath string) {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("`hyve migrate` requires an active environment with cluster-mode credentials — run `hyve login` first")
	}
	client := shared.NewAPIClient(sess)

	dryRun := !migrateWrite
	if dryRun {
		log.Println("🔍 Dry run — nothing will be written. Pass --write to actually migrate.")
	}

	switch {
	case migrateFile != "" && migrateDir != "":
		log.Fatal("--file and --dir are mutually exclusive")
	case migrateFile != "":
		migrateSingleFile(client, migrateFile, dryRun)
	case migrateDir != "":
		migrateDirectory(client, migrateDir, dryRun)
	case posPath != "":
		info, err := os.Stat(posPath)
		if err != nil {
			log.Fatalf("Failed to stat %s: %v", posPath, err)
		}
		if info.IsDir() {
			migrateDirectory(client, posPath, dryRun)
		} else {
			migrateSingleFile(client, posPath, dryRun)
		}
	default:
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Failed to resolve current directory: %v", err)
		}
		migrateDirectory(client, cwd, dryRun)
	}
}

// migrateSingleFile pushes exactly one CR-shaped YAML file — the same
// parse/dispatch `hyve apply` uses, just routed through --skip-existing
// instead of hard-failing on 409, and respecting the dry-run default.
func migrateSingleFile(client *shared.APIClient, path string, dryRun bool) {
	kind, name, spec, err := parseCRFile(path)
	if err != nil {
		log.Fatal(err)
	}

	if dryRun {
		log.Printf("  [%s] %s", strings.ToLower(kind), name)
		log.Println("\nWould migrate: 1 resource")
		log.Println("Re-run with --write to actually create it.")
		return
	}

	desc, err := createByKind(client, kind, name, spec)
	if err != nil {
		if migrateSkipExisting && isAlreadyExists(err) {
			log.Printf("  %s '%s' — already exists, skipped", kind, name)
			return
		}
		log.Fatalf("%s: %v", path, err)
	}
	log.Printf("✅ %s '%s' created", desc, name)
}

func migrateDirectory(client *shared.APIClient, dirPath string, dryRun bool) {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		log.Fatalf("Invalid path %q: %v", dirPath, err)
	}

	workflowCount := migrateWorkflows(client, absPath, dryRun)
	templateCount := migrateTemplates(client, absPath, dryRun)
	clusterCount := migrateClusters(client, shared.CreateStateManagerFromPath(absPath), dryRun)

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
