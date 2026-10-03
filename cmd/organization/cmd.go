// Package organization implements `hyve organization` — CLI surfacing for
// internal/api's /organizations endpoints (Milestone 2's own
// organization/environment model, see HYVE-ORGANIZATION-MODEL-PROPOSAL.md
// and HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs).
// Cluster-mode only — organizations are hyve-api's own multi-tenant
// concept, with no local-directory equivalent the way a cluster/template/
// workflow has (see internal/session's own doc comment on cluster mode
// and local directories being otherwise completely independent) — every
// command here requires a valid session (`hyve context login`) and fails
// clearly, not silently, without one.
package organization

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

// Cmd is the organization command.
var Cmd = &cobra.Command{
	Use:   "organization",
	Short: "Manage organizations (cluster mode only)",
	Long: `Create, list, delete, and manage organizations — hyve-api's own
multi-tenant isolation unit, one Kubernetes namespace plus a set of
environments/RBAC bindings/reconciling-cluster assignment. Requires
cluster mode: run 'hyve context login' against a hyve-api server first.

An organization's environments are managed with 'hyve environment', and
its reconciling clusters with 'hyve reconciling-cluster'.

One login reaches every organization you're a member of. 'use' picks the
one commands act in, remembered on the active context; --org overrides it
for a single command:

  hyve organization list           # the organizations you can access
  hyve organization use acme
  hyve cluster list --org widget   # one-off

With nothing selected, the server picks: your only organization, or the
control plane for a superadmin.

Creating an organization only provisions the namespace/environment — it
grants no one access. Add its first member from the console's Users page
(or POST /accounts) — an existing user keeps their own login.`,
}

var useCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Select the organization commands act in",
	Long: `Records <name> on the active context. Every cluster-mode command then acts in
it until you run 'use' again or 'unset'. Clears the selected environment,
which belongs to the previous organization.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		useOrganization(args[0])
	},
}

var unsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Clear the selected organization",
	Long:  "Commands go back to letting the server pick: your only organization, or the control plane for a superadmin.",
	Run: func(cmd *cobra.Command, args []string) {
		setSelectedOrganization("")
		log.Println("✅ Cleared the selected organization")
	},
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the organization commands act in",
	Run: func(cmd *cobra.Command, args []string) {
		who, err := requireClusterMode().Whoami()
		if err != nil {
			log.Fatalf("Failed to resolve organization: %v", err)
		}
		name := who.Organization
		if name == "" {
			name = who.Namespace
		}
		fmt.Printf("%s (%s)\n", name, who.Role)
	},
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
	Short: "List the organizations you can access",
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
names a pool cluster (see 'hyve reconciling-cluster pool add'/'pool
list'); --home moves it back to the control plane's own home
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
	Use:        "environments <name>",
	Short:      "List an organization's environments",
	Args:       cobra.ExactArgs(1),
	Hidden:     true,
	Deprecated: "use 'hyve environment list --org <name>' instead",
	Run: func(cmd *cobra.Command, args []string) {
		listOrganizationEnvironments(args[0])
	},
}

var createEnvironmentCmd = &cobra.Command{
	Use:        "create-environment <name> <environment>",
	Short:      "Add a new environment to an existing organization",
	Hidden:     true,
	Deprecated: "use 'hyve environment create <environment> --org <name>' instead",
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
	createCmd.Flags().String("reconciling-cluster", "", "Name of an already-registered reconciling cluster (see 'hyve reconciling-cluster pool add') this organization's resources should live on instead of the control plane's own home cluster — omit to use the home cluster")

	migrateCmd.Flags().String("reconciling-cluster", "", "Name of an already-registered reconciling cluster to move this organization onto")
	migrateCmd.Flags().Bool("home", false, "Move this organization back to the control plane's own home cluster")

	Cmd.AddCommand(useCmd, unsetCmd, currentCmd, createCmd)
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
		log.Fatal("This command requires cluster mode — run 'hyve context login' against a hyve-api server first.")
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
	who, err := requireClusterMode().Whoami()
	if err != nil {
		log.Fatalf("Failed to list organizations: %v", err)
	}
	if len(who.Organizations) == 0 {
		log.Println("❌ You don't belong to any organization")
		return
	}
	log.Printf("📦 Organizations (%d):", len(who.Organizations))
	for _, org := range who.Organizations {
		marker := ""
		if org.Namespace == who.Namespace {
			marker = " ← current"
		}
		log.Printf("  %s (%s)%s", org.Name, org.Role, marker)
	}
	if len(who.Organizations) > 1 {
		log.Println("\n💡 Switch with 'hyve organization use <name>', or --org for one command")
	}
}

// useOrganization checks that name is one you can access, then records it
// on the active context.
func useOrganization(name string) {
	who, err := requireClusterMode().Whoami()
	if err != nil {
		log.Fatalf("Failed to list organizations: %v", err)
	}
	for _, org := range who.Organizations {
		if org.Name == name || org.Namespace == name {
			setSelectedOrganization(org.Name)
			log.Printf("✅ Now acting in organization %s (%s)", org.Name, org.Role)
			return
		}
	}
	log.Fatalf("❌ You don't have access to an organization named %q — 'hyve organization list' shows the ones you do", name)
}

func setSelectedOrganization(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to open contexts: %v", err)
	}
	defer repoMgr.Close()
	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No active context. Use 'hyve context login --api-url ...' first.")
	}
	if current.APIURL == "" {
		log.Fatalf("Context '%s' is a local directory — organizations only exist on a hyve-api server. Switch with 'hyve context use'.", current.Name)
	}
	if err := repoMgr.SetServerOrganization(current.Name, name); err != nil {
		log.Fatalf("Failed to save the selected organization: %v", err)
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
