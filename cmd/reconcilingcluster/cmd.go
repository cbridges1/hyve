// Package reconcilingcluster implements `hyve reconciling-cluster` — CLI
// surfacing for internal/api's reconciling-cluster endpoints (Milestone 6's
// per-organization reconciling cluster, see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md and
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs).
//
// Two audiences, two command sets:
//   - add/list/use/remove/current: an organization admin managing their own
//     organization's clusters. An organization can keep several stored and
//     switch between them by name.
//   - pool ...: a superadmin managing the install-wide pool, whose clusters
//     are assigned to an organization with 'hyve organization migrate'.
//
// Cluster-mode only, same reasoning as cmd/organization's own doc comment:
// a reconciling cluster is hyve-api's own multi-tenant concept, with no
// local-directory equivalent.
package reconcilingcluster

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

var orgFlag string

// Cmd is the reconciling-cluster command.
var Cmd = &cobra.Command{
	Use:   "reconciling-cluster",
	Short: "Manage the clusters your organization's resources reconcile on (cluster mode only)",
	Long: `A reconciling cluster is the Kubernetes cluster hyve-controller reconciles an
organization's infrastructure on, distinct from whichever cluster hyve-api's
own pods happen to run on. Requires cluster mode: run 'hyve context login'
against a hyve-api server first.

An organization can store several of its own and switch between them by
name, without re-entering a kubeconfig:

  hyve reconciling-cluster add k3s --kubeconfig-file ~/k3s.yaml
  hyve reconciling-cluster add civo --kubeconfig-file ~/civo.yaml
  hyve reconciling-cluster use civo

Switching copies every resource onto the target cluster first (requests
against the organization get 423 Locked meanwhile); a failed copy leaves it
where it was.

These commands act on your own organization; a superadmin can pass --org
to act on another. The install-wide pool is under 'pool'.`,
}

var addCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Store one of your organization's own reconciling clusters",
	Long: `Reads a kubeconfig from --kubeconfig-file (or stdin, with --kubeconfig-file -)
and stores it under <name> for your organization, without switching to it —
see 'use'. The kubeconfig is never echoed back by the API. Re-running this
for an existing name rotates its kubeconfig (kubeconfigs expire/get
regenerated), taking effect immediately if it's the active cluster.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		kubeconfigFile, _ := cmd.Flags().GetString("kubeconfig-file")
		addCluster(args[0], readKubeconfig(kubeconfigFile))
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the reconciling clusters your organization can use",
	Run: func(cmd *cobra.Command, args []string) {
		listClusters()
	},
}

var useCmd = &cobra.Command{
	Use:   "use [name]",
	Short: "Switch your organization onto a reconciling cluster",
	Long: `Moves your organization onto one of its own stored clusters, or, with --home,
back onto the control plane's own home cluster (not allowed on an install
started with --require-reconciling-cluster).

Every resource is copied onto the target before the switch takes effect,
so this waits for that copy to finish.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		home, _ := cmd.Flags().GetBool("home")
		if (len(args) == 1) == home {
			log.Fatal("give exactly one of a cluster name or --home")
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		useCluster(name)
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Delete one of your organization's stored reconciling clusters",
	Long: `Permanently deletes the stored cluster and its kubeconfig. The active cluster
can't be removed — 'use' another one first.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeCluster(args[0])
	},
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show which reconciling cluster your organization is on",
	Run: func(cmd *cobra.Command, args []string) {
		showCurrent()
	},
}

var poolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Manage the install-wide reconciling cluster pool (superadmin)",
	Long: `The pool holds clusters a superadmin registers for the whole install, assigned
to an organization with 'hyve organization migrate <org> --reconciling-cluster
<name>'.`,
}

var poolAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a pool reconciling cluster",
	Long: `Reads a kubeconfig from --kubeconfig-file (or stdin, with --kubeconfig-file -)
and registers it in the pool under <name>. Idempotent by name: re-running
this rotates the stored kubeconfig.`,
	Args: cobra.ExactArgs(1),
	Run:  runPoolAdd,
}

// createCmd is the pre-pool name for 'pool add', kept so existing scripts
// keep working.
var createCmd = &cobra.Command{
	Use:        "create <name>",
	Short:      poolAddCmd.Short,
	Args:       cobra.ExactArgs(1),
	Hidden:     true,
	Deprecated: "use 'hyve reconciling-cluster pool add' instead",
	Run:        runPoolAdd,
}

var poolListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every registered reconciling cluster, install-wide",
	Long:  "Lists the pool and every organization's own clusters, each labeled with its owner.",
	Run: func(cmd *cobra.Command, args []string) {
		listPool()
	},
}

func init() {
	for _, c := range []*cobra.Command{addCmd, listCmd, useCmd, removeCmd, currentCmd} {
		c.Flags().StringVar(&orgFlag, "org", "", "Organization to act on (default: your own; superadmin only for any other)")
		Cmd.AddCommand(c)
	}
	addCmd.Flags().String("kubeconfig-file", "", "Path to the kubeconfig file to store (required) — pass - to read from stdin")
	_ = addCmd.MarkFlagRequired("kubeconfig-file")
	useCmd.Flags().Bool("home", false, "Move back onto the control plane's own home cluster")

	for _, c := range []*cobra.Command{poolAddCmd, createCmd} {
		c.Flags().String("kubeconfig-file", "", "Path to the kubeconfig file to register (required) — pass - to read from stdin")
		_ = c.MarkFlagRequired("kubeconfig-file")
	}
	poolCmd.AddCommand(poolAddCmd, poolListCmd)
	Cmd.AddCommand(poolCmd, createCmd)
}

func requireClusterMode() *shared.APIClient {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("This command requires cluster mode — run 'hyve context login' against a hyve-api server first.")
	}
	return shared.NewAPIClient(sess)
}

func clientAndOrg() (*shared.APIClient, string) {
	c := requireClusterMode()
	org, err := c.CurrentOrganization(orgFlag)
	if err != nil {
		log.Fatalf("Failed to resolve organization: %v", err)
	}
	return c, org
}

func readKubeconfig(path string) string {
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		log.Fatalf("Failed to read kubeconfig: %v", err)
	}
	return string(data)
}

func addCluster(name, kubeconfig string) {
	c, org := clientAndOrg()
	rc, err := c.AddOrgReconcilingCluster(org, name, kubeconfig)
	if err != nil {
		log.Fatalf("Failed to store reconciling cluster: %v", err)
	}
	if rc.Active {
		log.Printf("✅ Rotated the kubeconfig of %q (%s's active cluster)", rc.Name, org)
		return
	}
	log.Printf("✅ Stored reconciling cluster %q for %s", rc.Name, org)
	log.Printf("💡 Run 'hyve reconciling-cluster use %s' to move %s onto it.", rc.Name, org)
}

func listClusters() {
	c, org := clientAndOrg()
	clusters, err := c.ListOrgReconcilingClusters(org)
	if err != nil {
		log.Fatalf("Failed to list reconciling clusters: %v", err)
	}
	placement, err := c.GetOrgPlacement(org)
	if err != nil {
		log.Fatalf("Failed to get %s's current reconciling cluster: %v", org, err)
	}

	log.Printf("📦 Reconciling clusters for %s:", org)
	if placement.OnHomeCluster {
		log.Printf("  (home cluster) ← active")
	}
	if len(clusters) == 0 {
		log.Println("  none stored")
		log.Println("\n💡 Run 'hyve reconciling-cluster add <name> --kubeconfig-file <path>' to store one")
		return
	}
	for _, rc := range clusters {
		marker := ""
		if rc.Active {
			marker = " ← active"
		}
		log.Printf("  %s (%s)%s", rc.Name, rc.Ownership, marker)
		if rc.Server != "" {
			log.Printf("    Server: %s", rc.Server)
		}
		logHealth(rc.Reachable, rc.LastError, rc.LastCheckedAt, rc.KubernetesVersion)
	}
	if placement.Migrating {
		log.Println("\n⏳ A switch is in progress")
	}
}

func useCluster(name string) {
	c, org := clientAndOrg()
	if name == "" {
		log.Printf("⏳ Moving %s back onto the home cluster (copying every resource first)...", org)
	} else {
		log.Printf("⏳ Moving %s onto %q (copying every resource first)...", org, name)
	}
	placement, err := c.UseOrgReconcilingCluster(org, name)
	if err != nil {
		log.Fatalf("Failed to switch reconciling cluster: %v", err)
	}
	if placement.OnHomeCluster {
		log.Printf("✅ %s is on the home cluster", org)
		return
	}
	log.Printf("✅ %s is on %q (%s)", org, placement.Name, placement.Ownership)
}

func removeCluster(name string) {
	c, org := clientAndOrg()
	if err := c.RemoveOrgReconcilingCluster(org, name); err != nil {
		log.Fatalf("Failed to remove reconciling cluster: %v", err)
	}
	log.Printf("✅ Removed %q from %s's stored reconciling clusters", name, org)
}

func showCurrent() {
	c, org := clientAndOrg()
	placement, err := c.GetOrgPlacement(org)
	if err != nil {
		log.Fatalf("Failed to get %s's current reconciling cluster: %v", org, err)
	}
	switch {
	case placement.OnHomeCluster:
		fmt.Println("(home cluster)")
	default:
		fmt.Printf("%s (%s)\n", placement.Name, placement.Ownership)
	}
	if placement.Migrating {
		fmt.Println("⏳ switch in progress")
	}
}

func runPoolAdd(cmd *cobra.Command, args []string) {
	kubeconfigFile, _ := cmd.Flags().GetString("kubeconfig-file")
	rc, err := requireClusterMode().CreateReconcilingCluster(args[0], readKubeconfig(kubeconfigFile))
	if err != nil {
		log.Fatalf("Failed to register reconciling cluster: %v", err)
	}
	log.Printf("✅ Pool reconciling cluster %q registered", rc.Name)
	log.Println("💡 Assign it to an organization with 'hyve organization migrate <org> --reconciling-cluster " + rc.Name + "'.")
}

func listPool() {
	clusters, err := requireClusterMode().ListReconcilingClusters()
	if err != nil {
		log.Fatalf("Failed to list reconciling clusters: %v", err)
	}
	if len(clusters) == 0 {
		log.Println("❌ No reconciling clusters registered")
		log.Println("\n💡 Run 'hyve reconciling-cluster pool add <name> --kubeconfig-file <path>' to register one")
		return
	}
	log.Printf("📦 Reconciling clusters (%d):", len(clusters))
	for _, rc := range clusters {
		owner := "pool"
		if rc.Organization != "" {
			owner = "organization: " + rc.Organization
		}
		log.Printf("  %s (%s)", rc.Name, owner)
		if rc.Server != "" {
			log.Printf("    Server: %s", rc.Server)
		}
		logHealth(rc.Reachable, rc.LastError, rc.LastCheckedAt, rc.KubernetesVersion)
	}
}

func logHealth(reachable *bool, lastError, lastChecked, version *string) {
	switch {
	case reachable == nil:
		log.Printf("    Reachable: unknown (no health check yet)")
	case *reachable:
		log.Printf("    Reachable: yes")
	default:
		log.Printf("    Reachable: no")
		if lastError != nil {
			log.Printf("    Last error: %s", *lastError)
		}
	}
	if version != nil {
		log.Printf("    Kubernetes: %s", *version)
	}
	if lastChecked != nil {
		log.Printf("    Last checked: %s", *lastChecked)
	}
}
