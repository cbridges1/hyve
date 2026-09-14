// Package organization implements `hyve organization` — CLI surfacing for
// internal/api's /organizations endpoints (Milestone 2's own
// organization/environment model, see HYVE-ORGANIZATION-MODEL-PROPOSAL.md
// and HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs).
// Cluster-mode only — organizations are hyve-api's own multi-tenant
// concept, with no local-directory equivalent the way a cluster/template/
// workflow has (see internal/session's own doc comment on cluster mode
// and local directories being otherwise completely independent) — every
// command here requires a valid session (`hyve env login`) and fails
// clearly, not silently, without one.
package organization

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

// Cmd is the organization command.
var Cmd = &cobra.Command{
	Use:   "organization",
	Short: "Manage organizations (cluster mode only)",
	Long: `Create, list, delete, and manage organizations — hyve-api's own
multi-tenant isolation unit, one Kubernetes namespace plus a set of
environments/RBAC bindings/reconciling-cluster assignment. Requires
cluster mode: run 'hyve env login' against a hyve-api server first.

Creating an organization only provisions the namespace/environment — it
grants no one access. Use 'hyve cluster-config api create-user' (or POST
/accounts) to create its first account.`,
}

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new organization",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		adminIdentity, _ := cmd.Flags().GetString("admin-identity")
		adminRole, _ := cmd.Flags().GetString("admin-role")
		reconcilingCluster, _ := cmd.Flags().GetString("reconciling-cluster")
		createOrganization(args[0], adminIdentity, adminRole, reconcilingCluster)
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all organizations",
	Run: func(cmd *cobra.Command, args []string) {
		listOrganizations()
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an organization",
	Long: `Marks the organization for deletion and deletes its Kubernetes
namespace — permanent, and asynchronous: the organization's row is
removed once the namespace finishes terminating (a periodic sweep, or the
next call touching this same organization, finishes the job — see
internal/api's SweepPendingOrganizationDeletions).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deleteOrganization(args[0])
	},
}

var migrateCmd = &cobra.Command{
	Use:   "migrate <name>",
	Short: "Move an organization onto a different reconciling cluster",
	Long: `Copies every ClusterDefinition/Template/Workflow/Resource/AccessBinding
this organization owns onto the target reconciling cluster, then flips its
own reconciling-cluster assignment — see internal/migrate's own copy
primitives. Every request against this organization's resources returns
423 Locked for the duration. A failed copy aborts cleanly: the
organization stays on its original cluster.

Exactly one of --reconciling-cluster or --home is required: --reconciling-cluster
names an already-registered cluster (see 'hyve reconciling-cluster
create'/'list'); --home moves it back to the control plane's own home
cluster (only possible for an install that has one — see --home-cluster
on 'hyve cluster-config api run').`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		reconcilingCluster, _ := cmd.Flags().GetString("reconciling-cluster")
		home, _ := cmd.Flags().GetBool("home")
		if (reconcilingCluster != "") == home {
			log.Fatal("exactly one of --reconciling-cluster or --home is required")
		}
		migrateOrganization(args[0], reconcilingCluster)
	},
}

var environmentsCmd = &cobra.Command{
	Use:   "environments <name>",
	Short: "List an organization's environments",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		listOrganizationEnvironments(args[0])
	},
}

var createEnvironmentCmd = &cobra.Command{
	Use:   "create-environment <name> <environment>",
	Short: "Add a new environment to an existing organization",
	Long: `Every organization gets a 'default' environment automatically at
creation time — this is for every environment after that first one (e.g.
'staging', 'production').`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		createOrganizationEnvironment(args[0], args[1])
	},
}

func init() {
	createCmd.Flags().String("admin-identity", "", "Identity (username or OIDC subject) to grant an initial admin binding to, in the same transaction as the organization itself")
	createCmd.Flags().String("admin-role", "", "Role for --admin-identity (admin or read-only) — required if --admin-identity is set, must stay unset otherwise")
	createCmd.Flags().String("reconciling-cluster", "", "Name of an already-registered reconciling cluster (see 'hyve reconciling-cluster create') this organization's resources should live on instead of the control plane's own home cluster — omit to use the home cluster")

	migrateCmd.Flags().String("reconciling-cluster", "", "Name of an already-registered reconciling cluster to move this organization onto")
	migrateCmd.Flags().Bool("home", false, "Move this organization back to the control plane's own home cluster")

	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(deleteCmd)
	Cmd.AddCommand(migrateCmd)
	Cmd.AddCommand(environmentsCmd)
	Cmd.AddCommand(createEnvironmentCmd)
}

// requireClusterMode returns an APIClient for the current session or exits
// with a clear message — every command in this package needs one, since
// organizations have no local-mode equivalent at all.
func requireClusterMode() *shared.APIClient {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("This command requires cluster mode — run 'hyve env login' against a hyve-api server first.")
	}
	return shared.NewAPIClient(sess)
}

func createOrganization(name, adminIdentity, adminRole, reconcilingCluster string) {
	org, err := requireClusterMode().CreateOrganization(name, adminIdentity, adminRole, reconcilingCluster)
	if err != nil {
		log.Fatalf("Failed to create organization: %v", err)
	}
	log.Printf("✅ Organization %q created (namespace: %s)", org.Name, org.Namespace)
	if reconcilingCluster != "" {
		log.Printf("   Reconciling cluster: %s", reconcilingCluster)
	}
	if adminIdentity == "" {
		log.Println("💡 No --admin-identity given — use 'hyve cluster-config api create-user' or POST /accounts to create its first account.")
	}
}

func listOrganizations() {
	orgs, err := requireClusterMode().ListOrganizations()
	if err != nil {
		log.Fatalf("Failed to list organizations: %v", err)
	}
	if len(orgs) == 0 {
		log.Println("❌ No organizations found")
		log.Println("\n💡 Run 'hyve organization create <name>' to create one")
		return
	}
	log.Printf("📦 Organizations (%d):", len(orgs))
	for _, org := range orgs {
		log.Printf("  %s", org.Name)
		log.Printf("    Namespace: %s", org.Namespace)
		if org.Plan != "" {
			log.Printf("    Plan: %s", org.Plan)
		}
		if org.ReconcilingCluster != "" {
			log.Printf("    Reconciling cluster: %s", org.ReconcilingCluster)
		}
		if org.Migrating {
			log.Printf("    ⏳ Migration in progress")
		}
	}
}

func deleteOrganization(name string) {
	if err := requireClusterMode().DeleteOrganization(name); err != nil {
		log.Fatalf("Failed to delete organization: %v", err)
	}
	log.Printf("✅ Organization %q marked for deletion", name)
}

func migrateOrganization(name, reconcilingCluster string) {
	org, err := requireClusterMode().PatchOrganizationReconcilingCluster(name, reconcilingCluster)
	if err != nil {
		log.Fatalf("Failed to migrate organization: %v", err)
	}
	if reconcilingCluster == "" {
		log.Printf("✅ Organization %q migrated back to the home cluster", org.Name)
		return
	}
	log.Printf("✅ Organization %q migrated to reconciling cluster %q", org.Name, org.ReconcilingCluster)
}

func listOrganizationEnvironments(orgName string) {
	envs, err := requireClusterMode().ListOrganizationEnvironments(orgName)
	if err != nil {
		log.Fatalf("Failed to list environments: %v", err)
	}
	if len(envs) == 0 {
		log.Printf("❌ No environments found for organization %q", orgName)
		return
	}
	log.Printf("📦 Environments for %q (%d):", orgName, len(envs))
	for _, env := range envs {
		log.Printf("  %s", env.Name)
	}
}

func createOrganizationEnvironment(orgName, envName string) {
	env, err := requireClusterMode().CreateOrganizationEnvironment(orgName, envName)
	if err != nil {
		log.Fatalf("Failed to create environment: %v", err)
	}
	log.Printf("✅ Environment %q created for organization %q", env.Name, orgName)
}
