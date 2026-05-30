package cluster

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Discover and import unmanaged cloud clusters",
	Long: `Scan all configured cloud accounts for running clusters and compare against
repo definitions. Unmanaged clusters (present in the cloud, absent from the repo)
are listed and the user selects which to import.

Replaces the top-level 'hyve sync' command.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		providerFilter, _ := cmd.Flags().GetString("provider")
		accountFilter, _ := cmd.Flags().GetString("account")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return runClusterSync(providerFilter, accountFilter, dryRun)
	},
}

func init() {
	syncCmd.Flags().StringP("provider", "p", "", "Limit to a specific provider (civo, aws, gcp, azure)")
	syncCmd.Flags().StringP("account", "a", "", "Limit to a specific account/subscription/project alias")
	syncCmd.Flags().Bool("dry-run", false, "Print what would be imported without writing anything")
}

func runClusterSync(providerFilter, accountFilter string, dryRun bool) error {
	ctx := context.Background()

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
	}

	return nil
}

type discoveredCluster struct {
	name     string
	provider string
	account  string
	region   string
}

func discoverClusters(ctx context.Context, providerFilter, accountFilter string) ([]discoveredCluster, error) {
	var results []discoveredCluster

	providers := []string{"civo", "aws", "gcp", "azure"}
	if providerFilter != "" {
		providers = []string{providerFilter}
	}

	for _, prov := range providers {
		accounts := accountsForProvider(prov)
		for _, acct := range accounts {
			if accountFilter != "" && acct != accountFilter {
				continue
			}
			log.Printf("Scanning %s / %s ...", prov, acct)
			names := shared.FetchCloudClusterNames(ctx, prov, "", acct)
			for _, n := range names {
				results = append(results, discoveredCluster{
					name:     n,
					provider: prov,
					account:  acct,
				})
			}
		}
	}

	return results, nil
}

func accountsForProvider(providerName string) []string {
	switch providerName {
	case "civo":
		return shared.FetchCivoOrgNames()
	case "aws":
		return shared.FetchAWSAccountNames()
	case "gcp":
		return shared.FetchGCPProjectNames()
	case "azure":
		return shared.FetchAzureSubscriptionNames()
	}
	return nil
}

func importSelected(ctx context.Context, stateMgr *state.Manager, stateDir string, clusters []discoveredCluster, existing map[string]bool) error {
	if stateDir == "" {
		return fmt.Errorf("could not determine state directory")
	}

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	imported := 0
	for _, c := range clusters {
		if existing[c.name] {
			continue
		}

		def := buildSyncClusterDefinition(c)
		data, err := yaml.Marshal(&def)
		if err != nil {
			log.Printf("Warning: failed to marshal definition for '%s': %v", c.name, err)
			continue
		}

		filePath := filepath.Join(stateDir, c.name+".yaml")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			log.Printf("Warning: failed to write '%s': %v", filePath, err)
			continue
		}

		log.Printf("✅ Imported: %s", c.name)
		log.Printf("   Written: %s", filePath)
		imported++
		existing[c.name] = true
	}

	if imported == 0 {
		return nil
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("hyve cluster sync: import %d cluster(s)", imported))
	return nil
}

func buildSyncClusterDefinition(c discoveredCluster) types.ClusterDefinition {
	spec := types.ClusterSpec{
		Provider: c.provider,
	}

	switch c.provider {
	case "civo":
		spec.CivoOrganization = c.account
	case "aws":
		spec.AWSAccount = c.account
	case "gcp":
		spec.GCPProject = c.account
	case "azure":
		spec.AzureSubscription = c.account
	}

	return types.ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata: types.ClusterMetadata{
			Name:   c.name,
			Region: c.region,
		},
		Spec: spec,
	}
}
