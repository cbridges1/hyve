package cluster

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	mod "github.com/cbridges1/hyve/internal/module"
)

var authSyncDryRun bool

var authSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Remove stale local kubeconfig entries for clusters that no longer exist",
	Long: "Diffs context names in ~/.kube/config against known clusters — fetched from " +
		"the API in cluster mode (a 'hyve login' session is active), or from local " +
		"clusters/*.yaml files otherwise — and removes any context, plus its cluster " +
		"and user entries, that no longer corresponds to a known cluster.",
	Run: func(cmd *cobra.Command, args []string) {
		runClusterAuthSync(authSyncDryRun)
	},
}

func init() {
	authSyncCmd.Flags().BoolVar(&authSyncDryRun, "dry-run", false,
		"report what would be removed without modifying the kubeconfig")
	authCmd.AddCommand(authSyncCmd)
}

func runClusterAuthSync(dryRun bool) {
	known, err := knownClusterNames()
	if err != nil {
		log.Fatalf("Failed to load known clusters: %v", err)
	}

	kcPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		log.Fatalf("Failed to resolve kubeconfig path: %v", err)
	}
	data, err := os.ReadFile(kcPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No kubeconfig found — nothing to sync")
			return
		}
		log.Fatalf("Failed to read kubeconfig: %v", err)
	}

	contextNames, err := kubeconfig.ContextNames(string(data))
	if err != nil {
		log.Fatalf("Failed to parse kubeconfig: %v", err)
	}

	var orphans []string
	for _, name := range contextNames {
		if !known[name] {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) == 0 {
		fmt.Println("No stale kubeconfig entries found")
		return
	}

	for _, name := range orphans {
		if dryRun {
			fmt.Printf("would remove stale context '%s'\n", name)
			continue
		}
		if err := kubeconfig.RemoveKubeconfigContext(string(data), name, kcPath); err != nil {
			log.Printf("Warning: failed to remove context '%s': %v", name, err)
			continue
		}
		fmt.Printf("Removed stale context '%s'\n", name)

		// RemoveKubeconfigContext parses the *string* passed to it — not the
		// live file — and writes its result to kcPath. Re-read the file
		// after each removal so the next iteration's parse reflects the
		// previous removal; reusing the original `data` across iterations
		// would re-write the file from stale content and silently resurrect
		// contexts already removed earlier in the loop.
		data, err = os.ReadFile(kcPath)
		if err != nil {
			log.Fatalf("Failed to re-read kubeconfig after removal: %v", err)
		}
	}
}

// knownClusterNames returns the set of cluster names `sync` should treat as
// known. In cluster mode this asks the API — the single gateway for
// cluster-mode state — rather than a local git checkout, which wouldn't
// reflect server-side reality and would hard-fail anyway with no repo
// configured. Otherwise it reads local clusters/*.yaml files, unchanged.
func knownClusterNames() (map[string]bool, error) {
	if sess, ok := shared.UseClusterMode(); ok {
		clusters, err := shared.NewAPIClient(sess).ListClusters()
		if err != nil {
			return nil, err
		}
		known := make(map[string]bool, len(clusters))
		for _, c := range clusters {
			known[c.Name] = true
		}
		return known, nil
	}

	ctx := context.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)
	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(clusterDefs))
	for _, c := range clusterDefs {
		known[c.Metadata.Name] = true
	}
	return known, nil
}
