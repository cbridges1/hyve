package cmd

import (
	"context"
	"log"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/migrate"
	"github.com/cbridges1/hyve/internal/state"
)

var (
	migrateToClusterKubeconfig string
	migrateToClusterName       string
	migrateToClusterDir        string
	migrateToClusterNamespace  string
	migrateToClusterConfigName string
	migrateToClusterWrite      bool
	migrateToClusterForce      bool
)

// migrateToClusterCmd implements HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// Phase 7 `hyve migrate to-cluster` — local/git mode -> cluster mode,
// direct via kubeconfig + internal/migrate, not through the hyve API (that
// path already exists as the bare `hyve migrate` command above; this one
// exists specifically for bootstrapping a target cluster BEFORE its own
// API/HyveAccessBinding is set up to log into, since it only needs the
// target's kubeconfig).
var migrateToClusterCmd = &cobra.Command{
	Use:   "to-cluster",
	Short: "Copy the active local/git environment's clusters + config directly onto a target cluster's CRDs",
	Long: `Copies every ClusterDefinition (spec AND status — driverOutputs/appliedResources,
so the destination's controller can act on an already-provisioned cluster
without mistaking it for brand new) plus hyve.yaml's RepoConfig (as a
HyveConfig CR) from the active local/git environment directly onto a
target cluster, via that cluster's kubeconfig — no hyve API or login
required on the target, since this talks straight to its Kubernetes API
server, same as 'kubectl apply' would.

The target cluster must already exist and be reachable; deploying the
controller + API there (deploy/helm/hyve) is a separate, explicit step —
this command only copies data, matching hyve.lock's own
"image build is a separate concern" precedent.

Defaults to a dry run (lists what would be created, writes nothing). Pass
--write to actually create resources. Refuses to overwrite an existing
object with the same name on the target unless --force is passed.

Does NOT touch or modify the source local directory — nothing stops you
from continuing to run 'hyve reconcile' against it after migrating, which
would mean two reconcilers (your local one and the target's controller)
acting on the same downstream clusters. Stop doing that once migrated.`,
	Run: func(cmd *cobra.Command, args []string) {
		runMigrateToCluster()
	},
}

func init() {
	migrateToClusterCmd.Flags().StringVar(&migrateToClusterKubeconfig, "to", "", "Path to the target cluster's kubeconfig (fallback for ad hoc cases with no convenient ClusterDefinition — prefer --to-cluster)")
	migrateToClusterCmd.Flags().StringVar(&migrateToClusterName, "to-cluster", "", "Name of a ClusterDefinition hyve already knows about — resolved to a kubeconfig the same way `hyve cluster auth` would, local-mode or cluster-mode. Exactly one of --to/--to-cluster is required.")
	migrateToClusterCmd.Flags().StringVar(&migrateToClusterDir, "dir", "", "Local directory to migrate from (defaults to the active environment's registered directory)")
	migrateToClusterCmd.Flags().StringVar(&migrateToClusterNamespace, "namespace", "hyve-system", "Namespace on the target cluster to create ClusterDefinition/HyveConfig objects in")
	migrateToClusterCmd.Flags().StringVar(&migrateToClusterConfigName, "config-name", "hyve-config", "Name of the singleton HyveConfig object to create on the target")
	migrateToClusterCmd.Flags().BoolVar(&migrateToClusterWrite, "write", false, "Actually create resources on the target (default is a dry run)")
	migrateToClusterCmd.Flags().BoolVar(&migrateToClusterForce, "force", false, "Overwrite an existing object with the same name on the target instead of skipping it")
	migrateCmd.AddCommand(migrateToClusterCmd)
}

func runMigrateToCluster() {
	ctx := context.Background()
	dryRun := !migrateToClusterWrite
	if dryRun {
		log.Println("🔍 Dry run — nothing will be written. Pass --write to actually migrate.")
	}

	if migrateToClusterKubeconfig == "" && migrateToClusterName == "" {
		log.Fatal("Exactly one of --to or --to-cluster is required")
	}
	if migrateToClusterKubeconfig != "" && migrateToClusterName != "" {
		log.Fatal("--to and --to-cluster are mutually exclusive")
	}

	var source *state.Manager
	if migrateToClusterDir != "" {
		absPath, err := filepath.Abs(migrateToClusterDir)
		if err != nil {
			log.Fatalf("Invalid path %q: %v", migrateToClusterDir, err)
		}
		source = shared.CreateStateManagerFromPath(absPath)
	} else {
		source, _ = shared.CreateStateManagerFromRepository(ctx)
	}

	kcPath := migrateToClusterKubeconfig
	if migrateToClusterName != "" {
		resolved, cleanup, err := resolveClusterKubeconfigPath(migrateToClusterName)
		if err != nil {
			log.Fatalf("Failed to resolve kubeconfig for %q: %v", migrateToClusterName, err)
		}
		defer cleanup()
		kcPath = resolved
	}

	dest, err := migrate.BuildClient(kcPath)
	if err != nil {
		log.Fatalf("Failed to build client for target cluster: %v", err)
	}

	log.Printf("Migrating clusters into namespace %q on the target cluster...", migrateToClusterNamespace)
	clusterSummary, err := migrate.ClusterDefinitions(ctx, source, dest, migrateToClusterNamespace, dryRun, migrateToClusterForce)
	if err != nil {
		log.Fatalf("Failed to migrate cluster definitions: %v", err)
	}
	printMigrateSummary("cluster", clusterSummary, dryRun)

	skipped, err := migrate.HyveConfig(ctx, source, dest, migrateToClusterNamespace, migrateToClusterConfigName, dryRun)
	if err != nil {
		log.Fatalf("Failed to migrate HyveConfig: %v", err)
	}
	switch {
	case skipped:
		log.Printf("[hyveconfig] %s — already exists, left untouched", migrateToClusterConfigName)
	case dryRun:
		log.Printf("[hyveconfig] %s — would create", migrateToClusterConfigName)
	default:
		log.Printf("[hyveconfig] %s — created", migrateToClusterConfigName)
	}

	localPath := source.LocalPath()
	templateSummary, err := migrate.TemplatesFromDir(ctx, localPath, dest, migrateToClusterNamespace, dryRun, migrateToClusterForce)
	if err != nil {
		log.Fatalf("Failed to migrate templates: %v", err)
	}
	printMigrateSummary("template", templateSummary, dryRun)

	workflowSummary, err := migrate.WorkflowsFromDir(ctx, localPath, dest, migrateToClusterNamespace, dryRun, migrateToClusterForce)
	if err != nil {
		log.Fatalf("Failed to migrate workflows: %v", err)
	}
	printMigrateSummary("workflow", workflowSummary, dryRun)

	if dryRun {
		log.Println("\nRe-run with --write to actually create these on the target cluster.")
		return
	}
	if !clusterSummary.OK() || !templateSummary.OK() || !workflowSummary.OK() {
		log.Fatal("Migration completed with failures — see above.")
	}

	log.Println("\n⚠️  This command does NOT touch your local directory. If you continue running")
	log.Println("    'hyve reconcile' against it now that the target cluster's controller also")
	log.Println("    reconciles these same clusters, both will fight over the same downstream")
	log.Println("    infrastructure. Stop running 'hyve reconcile' locally against this directory.")

	log.Println("\n⚠️  This command does NOT copy hyve.lock. The target cluster's controller image")
	log.Println("    needs it baked in separately (see deploy/Dockerfile.controller) — module")
	log.Println("    resolution will fail for any cluster whose driver isn't already locked into")
	log.Println("    that image, even though the ClusterDefinition itself migrated successfully.")
}
