package cmd

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/migrate"
	"github.com/cbridges1/hyve/internal/reconcile"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// crdStateProviderFor wraps a live cluster's client as a
// reconcile.StateProvider — needed because migrate.ClusterDefinitions/
// migrate.HyveConfig take a StateProvider (so `to-cluster`'s local-directory
// source and `cluster`'s live-cluster source share the identical copy
// logic), but a `migrate cluster` source is itself a cluster's CRDs, not a
// directory. Same type the controller/API use server-side
// (internal/controller.CRDStateProvider) — this is its first client-side
// use, against an arbitrary external kubeconfig instead of the in-cluster
// default, which is exactly what its own Client client.Client field
// already supports with no changes.
func crdStateProviderFor(c client.Client, namespace, configName string) reconcile.StateProvider {
	return &controller.CRDStateProvider{Client: c, Namespace: namespace, ConfigName: configName}
}

var (
	migrateClusterFrom               string
	migrateClusterTo                 string
	migrateClusterNamespace          string
	migrateClusterConfigName         string
	migrateClusterWrite              bool
	migrateClusterForce              bool
	migrateClusterAckSourceStopped   bool
	migrateClusterSkipAccessBindings bool
)

// migrateClusterCmd implements HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// Phase 7 `hyve migrate cluster` — moving which cluster hosts the
// controller + API (hardware replacement, cloud migration, whatever the
// reason). Same underlying mechanism as `to-cluster`: both ends are
// CRDStateProviders, so it's the identical copy loop plus HyveAccessBinding
// (and its paired credentials Secrets) so the same users keep the same
// access on the new primary without re-onboarding.
var migrateClusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Copy ClusterDefinitions, HyveConfig, and HyveAccessBindings from one primary cluster to another",
	Long: `Copies ClusterDefinitions (spec + status), the singleton HyveConfig, and every
HyveAccessBinding (+ its paired credentials Secret, for local users) from
--from's cluster to --to's — moving which cluster hosts hyve's controller
+ API. Both sides are read/written directly via their kubeconfigs, not
through either cluster's API — this has to work even before the target
has its own API reachable yet.

This command does NOT redeploy the controller/API onto the target, and
does NOT stop the source cluster's controller — both are deliberately
left as explicit operator steps (which image, which manifests, which DNS
record vary by environment; this command only copies data).

THE REAL RISK: if the source cluster's controller is still running when
the target's controller starts reconciling the copied ClusterDefinitions,
both will reconcile the SAME downstream clusters simultaneously. The
correct order is: (1) run this command, (2) deploy controller+API to the
target but don't point traffic/DNS at it yet, (3) stop the source
cluster's controller — verified, not assumed, (4) cut over DNS/whatever
'hyve login' sessions point at, (5) only then treat the new primary as
authoritative. This command refuses to run past a dry run without
--i-have-stopped-the-source-controller as an explicit acknowledgment that
step 3 already happened.

Defaults to a dry run. Pass --write (and the acknowledgment flag above)
to actually create resources. Refuses to overwrite an existing object on
the target unless --force is passed.`,
	Run: func(cmd *cobra.Command, args []string) {
		runMigrateCluster()
	},
}

func init() {
	migrateClusterCmd.Flags().StringVar(&migrateClusterFrom, "from", "", "Path to the source primary cluster's kubeconfig (required)")
	migrateClusterCmd.Flags().StringVar(&migrateClusterTo, "to", "", "Path to the target primary cluster's kubeconfig (required)")
	migrateClusterCmd.Flags().StringVar(&migrateClusterNamespace, "namespace", "hyve-system", "Namespace on both clusters holding ClusterDefinition/HyveConfig/HyveAccessBinding objects")
	migrateClusterCmd.Flags().StringVar(&migrateClusterConfigName, "config-name", "hyve-config", "Name of the singleton HyveConfig object")
	migrateClusterCmd.Flags().BoolVar(&migrateClusterWrite, "write", false, "Actually create resources on the target (default is a dry run)")
	migrateClusterCmd.Flags().BoolVar(&migrateClusterForce, "force", false, "Overwrite an existing object with the same name on the target instead of skipping it")
	migrateClusterCmd.Flags().BoolVar(&migrateClusterAckSourceStopped, "i-have-stopped-the-source-controller", false, "Required alongside --write: confirms the source cluster's controller is already stopped, avoiding dual-reconciliation (see this command's long help)")
	migrateClusterCmd.Flags().BoolVar(&migrateClusterSkipAccessBindings, "skip-access-bindings", false, "Skip copying HyveAccessBindings + their credentials Secrets — only ClusterDefinitions/HyveConfig")
	_ = migrateClusterCmd.MarkFlagRequired("from")
	_ = migrateClusterCmd.MarkFlagRequired("to")
	migrateCmd.AddCommand(migrateClusterCmd)
}

func runMigrateCluster() {
	ctx := context.Background()
	dryRun := !migrateClusterWrite
	if dryRun {
		log.Println("🔍 Dry run — nothing will be written. Pass --write to actually migrate.")
	} else if !migrateClusterAckSourceStopped {
		log.Fatal("Refusing to write: pass --i-have-stopped-the-source-controller once you've " +
			"actually stopped the source cluster's controller (see 'hyve migrate cluster --help' " +
			"for why — running both controllers at once means dual-reconciliation of the same " +
			"downstream clusters).")
	}

	source, err := migrate.BuildClient(migrateClusterFrom)
	if err != nil {
		log.Fatalf("Failed to build client for source cluster (--from): %v", err)
	}
	dest, err := migrate.BuildClient(migrateClusterTo)
	if err != nil {
		log.Fatalf("Failed to build client for target cluster (--to): %v", err)
	}

	sourceProvider := crdStateProviderFor(source, migrateClusterNamespace, migrateClusterConfigName)

	log.Printf("Migrating clusters into namespace %q on the target cluster...", migrateClusterNamespace)
	clusterSummary, err := migrate.ClusterDefinitions(ctx, sourceProvider, dest, migrateClusterNamespace, dryRun, migrateClusterForce)
	if err != nil {
		log.Fatalf("Failed to migrate cluster definitions: %v", err)
	}
	printMigrateSummary("cluster", clusterSummary, dryRun)

	skipped, err := migrate.HyveConfig(ctx, sourceProvider, dest, migrateClusterNamespace, migrateClusterConfigName, dryRun)
	if err != nil {
		log.Fatalf("Failed to migrate HyveConfig: %v", err)
	}
	switch {
	case skipped:
		log.Printf("[hyveconfig] %s — already exists, left untouched", migrateClusterConfigName)
	case dryRun:
		log.Printf("[hyveconfig] %s — would create", migrateClusterConfigName)
	default:
		log.Printf("[hyveconfig] %s — created", migrateClusterConfigName)
	}

	allOK := clusterSummary.OK()
	if !migrateClusterSkipAccessBindings {
		bindingSummary, err := migrate.AccessBindings(ctx, source, dest, migrateClusterNamespace, dryRun, migrateClusterForce)
		if err != nil {
			log.Fatalf("Failed to migrate HyveAccessBindings: %v", err)
		}
		printMigrateSummary("accessbinding", bindingSummary, dryRun)
		allOK = allOK && bindingSummary.OK()
	}

	if dryRun {
		log.Println("\nRe-run with --write --i-have-stopped-the-source-controller to actually migrate.")
		return
	}
	if !allOK {
		log.Fatal("Migration completed with failures — see above.")
	}

	log.Println("\n✅ Data copied. Remaining steps, still yours to do:")
	log.Println("   2. Deploy the controller + API onto the target — don't point real traffic/DNS at it yet.")
	log.Println("   3. (You've already confirmed the source controller is stopped.)")
	log.Println("   4. Cut over DNS / whatever 'hyve login' sessions point at.")
	log.Println("   5. Only then treat the target as the authoritative primary cluster.")
}
