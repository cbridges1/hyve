package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/types"
)

var resourcesCmd = &cobra.Command{
	Use:   "resources [cluster-name]",
	Short: "Show resources hyve currently tracks for a cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		showClusterResources(args[0])
	},
}

func init() { Cmd.AddCommand(resourcesCmd) }

// showClusterResources reads the cluster YAML directly (no live cluster
// calls, same read-only pattern as showCluster) and prints both declared
// (spec.resources) and reconciler-tracked (spec.appliedResources) state.
func showClusterResources(clusterName string) {
	ctx := gocontext.Background()
	_, clustersDir := shared.CreateStateManager(ctx)
	filePath := filepath.Join(clustersDir, clusterName+".yaml")

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Cluster '%s' not found. Use 'hyve cluster list' to see available clusters.", clusterName)
		}
		log.Fatalf("Failed to read cluster file: %v", err)
	}

	var clusterDef types.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		log.Fatalf("Failed to parse cluster definition: %v", err)
	}

	if len(clusterDef.Spec.Resources) == 0 && len(clusterDef.Spec.AppliedResources) == 0 {
		fmt.Printf("No resources declared or tracked for cluster '%s'\n", clusterName)
		return
	}

	if len(clusterDef.Spec.Resources) > 0 {
		fmt.Printf("Declared (spec.resources):\n")
		for _, res := range clusterDef.Spec.Resources {
			switch {
			case res.Delete:
				fmt.Printf("  %s  (marked delete: true)\n", res.Name)
			case res.Helm != nil:
				fmt.Printf("  %s  helm chart=%s version=%s namespace=%s\n", res.Name, res.Helm.Chart, res.Helm.Version, res.Helm.Namespace)
			default:
				ns := res.Namespace
				if ns == "" {
					ns = "(manifest default)"
				}
				fmt.Printf("  %s  source=%s namespace=%s\n", res.Name, res.Source, ns)
			}
		}
		fmt.Println()
	}

	if len(clusterDef.Spec.AppliedResources) > 0 {
		fmt.Printf("Tracked (spec.appliedResources):\n")
		names := make([]string, 0, len(clusterDef.Spec.AppliedResources))
		for n := range clusterDef.Spec.AppliedResources {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ar := clusterDef.Spec.AppliedResources[n]
			kind := "manifest"
			if ar.Helm {
				kind = "helm"
			}
			hashPrefix := ar.SourceSHA256
			if len(hashPrefix) > 12 {
				hashPrefix = hashPrefix[:12]
			}
			fmt.Printf("  %s  [%s]  appliedAt=%s  sha256=%s…  objects=%d\n", n, kind, ar.AppliedAt, hashPrefix, len(ar.Objects))
		}
	}
}
