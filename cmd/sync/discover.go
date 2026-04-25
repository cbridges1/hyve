package sync

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

type discoveredCluster struct {
	name     string
	provider string
	account  string
	region   string
}

// discoverClusters queries all configured cloud accounts for running clusters.
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

// importSelected writes ClusterDefinition YAMLs for the selected clusters.
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

		def := buildClusterDefinition(c)
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

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("hyve sync: import %d cluster(s)", imported))
	return nil
}

func buildClusterDefinition(c discoveredCluster) types.ClusterDefinition {
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

	region := c.region

	return types.ClusterDefinition{
		APIVersion: "v1",
		Kind:       "Cluster",
		Metadata: types.ClusterMetadata{
			Name:   c.name,
			Region: region,
		},
		Spec: spec,
	}
}
