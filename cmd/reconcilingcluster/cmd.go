// Package reconcilingcluster implements `hyve reconciling-cluster` — CLI
// surfacing for internal/api's /reconciling-clusters endpoints (Milestone
// 6's per-organization reconciling cluster, see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md and
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs).
// Cluster-mode only, same reasoning as cmd/organization's own doc
// comment: a reconciling cluster is hyve-api's own multi-tenant concept,
// with no local-directory equivalent.
package reconcilingcluster

import (
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

// Cmd is the reconciling-cluster command.
var Cmd = &cobra.Command{
	Use:   "reconciling-cluster",
	Short: "Register and list reconciling clusters (cluster mode only)",
	Long: `Register a physical Kubernetes cluster hyve-controller can
reconcile organizations' infrastructure against, distinct from whichever
cluster hyve-api's own pods happen to run on. Requires cluster mode: run
'hyve env login' against a hyve-api server first.

Move an organization onto a registered reconciling cluster with 'hyve
organization migrate'.`,
}

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Register a new reconciling cluster",
	Long: `Reads a kubeconfig from --kubeconfig-file (or stdin, with
--kubeconfig-file -) and registers it under <name>. The kubeconfig content
itself is never echoed back by this API under any circumstance. Idempotent
by name: re-running this against an existing name rotates its stored
kubeconfig content — a real, expected operational need (kubeconfigs
expire/get regenerated).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		kubeconfigFile, _ := cmd.Flags().GetString("kubeconfig-file")
		if kubeconfigFile == "" {
			log.Fatal("--kubeconfig-file is required (use - for stdin)")
		}
		createReconcilingCluster(args[0], kubeconfigFile)
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered reconciling clusters",
	Run: func(cmd *cobra.Command, args []string) {
		listReconcilingClusters()
	},
}

func init() {
	createCmd.Flags().String("kubeconfig-file", "", "Path to the kubeconfig file to register (required) — pass - to read from stdin")

	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(listCmd)
}

func requireClusterMode() *shared.APIClient {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("This command requires cluster mode — run 'hyve env login' against a hyve-api server first.")
	}
	return shared.NewAPIClient(sess)
}

func createReconcilingCluster(name, kubeconfigFile string) {
	var (
		data []byte
		err  error
	)
	if kubeconfigFile == "-" {
		data, err = os.ReadFile("/dev/stdin")
	} else {
		data, err = os.ReadFile(kubeconfigFile)
	}
	if err != nil {
		log.Fatalf("Failed to read kubeconfig: %v", err)
	}

	rc, err := requireClusterMode().CreateReconcilingCluster(name, string(data))
	if err != nil {
		log.Fatalf("Failed to register reconciling cluster: %v", err)
	}
	log.Printf("✅ Reconciling cluster %q registered", rc.Name)
	log.Println("💡 Use 'hyve organization migrate <name> --reconciling-cluster " + rc.Name + "' to move an organization onto it.")
}

func listReconcilingClusters() {
	clusters, err := requireClusterMode().ListReconcilingClusters()
	if err != nil {
		log.Fatalf("Failed to list reconciling clusters: %v", err)
	}
	if len(clusters) == 0 {
		log.Println("❌ No reconciling clusters registered")
		log.Println("\n💡 Run 'hyve reconciling-cluster create <name> --kubeconfig-file <path>' to register one")
		return
	}
	log.Printf("📦 Reconciling clusters (%d):", len(clusters))
	for _, rc := range clusters {
		log.Printf("  %s", rc.Name)
		switch {
		case rc.Reachable == nil:
			log.Printf("    Reachable: unknown (no health check yet)")
		case *rc.Reachable:
			log.Printf("    Reachable: yes")
		default:
			log.Printf("    Reachable: no")
			if rc.LastError != nil {
				log.Printf("    Last error: %s", *rc.LastError)
			}
		}
		if rc.LastCheckedAt != nil {
			log.Printf("    Last checked: %s", *rc.LastCheckedAt)
		}
	}
}
