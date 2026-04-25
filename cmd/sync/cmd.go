package sync

import (
	gocontext "context"
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/reconcile"
)

// Cmd is the sync command exposed to the parent.
var Cmd = &cobra.Command{
	Use:   "sync",
	Short: "Discover unmanaged clusters and reconcile provider config resources",
	Long: `Scan all configured cloud accounts and perform two operations:

Clusters — compare running cloud clusters against repo definitions. Unmanaged
clusters (present in the cloud, absent from the repo) are listed and the user
selects which to import.

Provider config resources — query each account for VPCs, IAM roles, and
resource groups and reconcile the provider config YAML files immediately,
adding missing entries and removing stale ones.

Both steps commit and push if any changes were made.

Replaces 'hyve cluster import'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		providerFilter, _ := cmd.Flags().GetString("provider")
		accountFilter, _ := cmd.Flags().GetString("account")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return runSync(providerFilter, accountFilter, dryRun)
	},
}

func init() {
	Cmd.Flags().StringP("provider", "p", "", "Limit to a specific provider (civo, aws, gcp, azure)")
	Cmd.Flags().StringP("account", "a", "", "Limit to a specific account/subscription/project alias")
	Cmd.Flags().Bool("dry-run", false, "Print what would be imported without writing anything")
}

func runSync(providerFilter, accountFilter string, dryRun bool) error {
	ctx := gocontext.Background()

	stateMgr, stateDir := shared.CreateStateManager(ctx)
	if stateMgr == nil {
		log.Fatal("No Git repository configured. Use 'hyve git add' to configure a repository.")
	}

	existing, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to load cluster definitions: %v", err)
	}

	existingNames := make(map[string]bool, len(existing))
	for _, d := range existing {
		existingNames[d.Metadata.Name] = true
	}

	discoveries, err := discoverClusters(ctx, providerFilter, accountFilter)
	if err != nil {
		return err
	}

	var unmanaged []discoveredCluster
	for _, d := range discoveries {
		if !existingNames[d.name] {
			unmanaged = append(unmanaged, d)
		}
	}

	if len(unmanaged) == 0 {
		log.Println("✅ No unmanaged clusters found.")
	} else {
		log.Printf("Unmanaged clusters found (%d):\n", len(unmanaged))
		for _, d := range unmanaged {
			log.Printf("  [%s / %s]  %s", d.provider, d.region, d.name)
		}

		if !dryRun {
			if err := importSelected(ctx, stateMgr, stateDir, unmanaged, existingNames); err != nil {
				return err
			}
		}
	}

	if dryRun {
		log.Println("\n(dry-run — nothing written)")
		return nil
	}

	// Provider config resources sync: query each account for VPCs, IAM roles,
	// resource groups, and networks — reconcile the provider config YAML files.
	log.Println("\nProvider config sync: scanning cloud accounts...")
	added, removed := reconcile.SyncProviderConfigFields(ctx, stateMgr)
	if added+removed > 0 {
		log.Printf("Provider config sync: %d resource(s) added, %d resource(s) removed", added, removed)
		shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("hyve sync: provider config sync (%d added, %d removed)", added, removed))
	} else {
		log.Println("Provider config sync: no changes")
	}

	return nil
}
