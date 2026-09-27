// Package environment implements `hyve environment` — an organization's
// named sub-scopes on a hyve-api server (internal/orgdb.Environment), which
// ClusterDefinitions are created in and addressed through. Not to be
// confused with `hyve context`, the local CLI's own record of where its
// state lives (a directory, or a hyve-api server to log into): a context
// is client-side and per-machine, an environment is server-side and shared
// by everyone in the organization.
//
// `use` records the selection on the active context, so every cluster-mode
// command sends it as ?env= until changed — see cmd/shared's
// SelectedServerEnvironment. The root --env flag overrides it for one
// command. Cluster-mode only.
package environment

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

var orgFlag string

// Cmd is the environment command.
var Cmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"environments"},
	Short:   "Manage and select your organization's environments (cluster mode only)",
	Long: `Environments are named scopes within an organization on a hyve-api server
(e.g. 'default', 'staging', 'production'). Clusters are created in one, and
two environments can each have a cluster with the same name. Every
organization starts with 'default'.

'use' selects an environment for the active context, so cluster commands
target it until you change it:

  hyve environment use staging
  hyve cluster list                  # staging's clusters only
  hyve cluster get web --env default # one-off override

With nothing selected, the server picks when the organization has exactly
one environment, and asks you to choose when it has several.

Not the same as 'hyve context', which is this machine's record of where the
CLI's state lives (a local directory or a hyve-api server). Requires cluster
mode: run 'hyve context login' first.`,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List your organization's environments",
	Run: func(cmd *cobra.Command, args []string) {
		listEnvironments()
	},
}

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Add an environment to your organization",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		createEnvironment(args[0])
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an environment",
	Long:  "Refused while the environment still has clusters — delete them first.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deleteEnvironment(args[0])
	},
}

var useCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Select the environment cluster commands target",
	Long: `Records <name> on the active context. Every cluster-mode command then targets
it until you run 'use' again or 'unset'. Each context keeps its own
selection, so switching contexts switches environments with it.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		useEnvironment(args[0])
	},
}

var unsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Clear the selected environment",
	Long:  "Cluster commands go back to letting the server pick (which works when the organization has exactly one environment).",
	Run: func(cmd *cobra.Command, args []string) {
		unsetEnvironment()
	},
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the selected environment",
	Run: func(cmd *cobra.Command, args []string) {
		showCurrent()
	},
}

func init() {
	for _, c := range []*cobra.Command{listCmd, createCmd, deleteCmd, useCmd} {
		c.Flags().StringVar(&orgFlag, "org", "", "Organization to act on (default: your own; superadmin only for any other)")
	}
	Cmd.AddCommand(listCmd, createCmd, deleteCmd, useCmd, unsetCmd, currentCmd)
}

func clientAndOrg() (*shared.APIClient, string) {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("This command requires cluster mode — run 'hyve context login' against a hyve-api server first.")
	}
	c := shared.NewAPIClient(sess)
	org, err := c.CurrentOrganization(orgFlag)
	if err != nil {
		log.Fatalf("Failed to resolve organization: %v", err)
	}
	return c, org
}

// activeClusterContext returns the active context, which must point at a
// hyve-api server — a local-directory context has no server environments.
func activeClusterContext(repoMgr *repository.Manager) *repository.Repository {
	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No active context. Use 'hyve context create --api-url ...' to register one.")
	}
	if current.APIURL == "" {
		log.Fatalf("Context '%s' is a local directory — environments only exist on a hyve-api server. Switch with 'hyve context use'.", current.Name)
	}
	return current
}

func listEnvironments() {
	c, org := clientAndOrg()
	envs, err := c.ListOrganizationEnvironments(org)
	if err != nil {
		log.Fatalf("Failed to list environments: %v", err)
	}
	if len(envs) == 0 {
		log.Printf("❌ No environments found for organization %q", org)
		return
	}
	log.Printf("📦 Environments for %s (%d):", org, len(envs))
	for _, env := range envs {
		marker := ""
		if env.Name == c.Env {
			marker = " ← selected"
		}
		log.Printf("  %s%s", env.Name, marker)
	}
	if c.Env == "" {
		log.Println("\n💡 None selected — run 'hyve environment use <name>' to pick one")
	}
}

func createEnvironment(name string) {
	c, org := clientAndOrg()
	env, err := c.CreateOrganizationEnvironment(org, name)
	if err != nil {
		log.Fatalf("Failed to create environment: %v", err)
	}
	log.Printf("✅ Environment %q created for %s", env.Name, org)
	log.Printf("💡 Run 'hyve environment use %s' to target it", env.Name)
}

func deleteEnvironment(name string) {
	c, org := clientAndOrg()
	if err := c.DeleteOrganizationEnvironment(org, name); err != nil {
		log.Fatalf("Failed to delete environment: %v", err)
	}
	log.Printf("✅ Environment %q deleted from %s", name, org)

	if name == c.Env && shared.ServerEnvFlagValue == "" {
		clearSelection("it was the selected environment, so the selection has been cleared")
	}
}

func useEnvironment(name string) {
	c, org := clientAndOrg()
	envs, err := c.ListOrganizationEnvironments(org)
	if err != nil {
		log.Fatalf("Failed to list environments: %v", err)
	}
	found := false
	for _, env := range envs {
		found = found || env.Name == name
	}
	if !found {
		log.Fatalf("Organization %q has no environment named %q — see 'hyve environment list'", org, name)
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to open context registry: %v", err)
	}
	defer repoMgr.Close()
	current := activeClusterContext(repoMgr)
	if err := repoMgr.SetServerEnvironment(current.Name, name); err != nil {
		log.Fatalf("Failed to select environment: %v", err)
	}
	log.Printf("✅ Context '%s' now targets environment %q", current.Name, name)
}

func unsetEnvironment() {
	clearSelection("")
}

func clearSelection(reason string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to open context registry: %v", err)
	}
	defer repoMgr.Close()
	current := activeClusterContext(repoMgr)
	if err := repoMgr.SetServerEnvironment(current.Name, ""); err != nil {
		log.Fatalf("Failed to clear environment: %v", err)
	}
	if reason != "" {
		log.Printf("   (%s)", reason)
		return
	}
	log.Printf("✅ Context '%s' no longer targets a specific environment", current.Name)
}

func showCurrent() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to open context registry: %v", err)
	}
	defer repoMgr.Close()
	current := activeClusterContext(repoMgr)
	switch {
	case shared.ServerEnvFlagValue != "":
		fmt.Printf("%s (from --env)\n", shared.ServerEnvFlagValue)
	case current.ServerEnvironment != "":
		fmt.Println(current.ServerEnvironment)
	default:
		fmt.Println("(none selected — the server picks when the organization has exactly one)")
	}
}
