package env

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

var secretsEnvFlag string

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets attached to an environment",
	Long: `Commands to read and update KEY=VALUE secrets.

Local mode: stored in hyve.db attached to an environment — not tied to any
repository/folder, and removed automatically when the environment itself is
(see 'hyve env remove').

Cluster mode (a valid 'hyve login' session exists): stored as a single
Kubernetes Secret in the hyve-api server's own namespace, shared by every
caller logged into that server — --env/the environment name argument have
no effect here, since cluster-mode secrets aren't scoped per local
environment. Listing key names works for any role; reading/setting/
unsetting values requires the admin role.

Loaded into the process environment before every command (see 'hyve
reconcile', 'hyve workflow run') — cluster-mode secrets take precedence
over local ones, which take precedence over a legacy repo-relative
hyve.yaml env.file, if one is also configured.`,
}

var secretsListCmd = &cobra.Command{
	Use:   "list [name]",
	Short: "Print every KEY=VALUE secret attached to an environment",
	Long:  "Defaults to the currently-active environment when name is omitted. Ignored in cluster mode.",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		listSecrets(name)
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print a single secret's value",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		getSecret(args[0])
	},
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <key> [value]",
	Short: "Add or update a secret",
	Long:  "Omit value to be prompted for it without echo, rather than leaving it in your shell history.",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		value := ""
		if len(args) > 1 {
			value = args[1]
		}
		setSecret(args[0], value)
	},
}

var secretsUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Remove a secret",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		unsetSecret(args[0])
	},
}

func init() {
	secretsGetCmd.Flags().StringVar(&secretsEnvFlag, "env", "", "Target environment (default: the active one; ignored in cluster mode)")
	secretsSetCmd.Flags().StringVar(&secretsEnvFlag, "env", "", "Target environment (default: the active one; ignored in cluster mode)")
	secretsUnsetCmd.Flags().StringVar(&secretsEnvFlag, "env", "", "Target environment (default: the active one; ignored in cluster mode)")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsUnsetCmd)
	Cmd.AddCommand(secretsCmd)
}

// warnEnvIgnoredInClusterMode notes that a --env/positional-name value has
// no effect once cluster mode is in play — cluster secrets are shared per
// hyve-api server, not scoped per local environment.
func warnEnvIgnoredInClusterMode(name string) {
	if name != "" {
		log.Println("Note: the target environment has no effect in cluster mode — secrets are shared per hyve-api server, not per local environment")
	}
}

// resolveEnvironment returns the named environment, or the current one if
// name is empty — the common resolution every local-mode secrets subcommand
// needs.
func resolveEnvironment(repoMgr *repository.Manager, name string) (*repository.Repository, error) {
	if name != "" {
		return repoMgr.GetRepositoryByName(name)
	}
	env, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return nil, fmt.Errorf("no active environment — use --env <name>, or 'hyve env use <name>' to activate one")
	}
	return env, nil
}

func listSecrets(name string) {
	if sess, ok := shared.UseClusterMode(); ok {
		warnEnvIgnoredInClusterMode(name)
		listSecretsAPI(shared.NewAPIClient(sess))
		return
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	target, err := resolveEnvironment(repoMgr, name)
	if err != nil {
		log.Fatalf("%v", err)
	}

	vars, err := repoMgr.ListSecrets(target.ID)
	if err != nil {
		log.Fatalf("Failed to list secrets: %v", err)
	}
	if len(vars) == 0 {
		fmt.Println("(no secrets set)")
		return
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s=%s\n", k, vars[k])
	}
}

// listSecretsAPI tries the admin-only values endpoint first; a 403 (a
// read-only session) falls back to key names only, so a read-only caller
// still gets a useful listing instead of a hard failure.
func listSecretsAPI(client *shared.APIClient) {
	vars, err := client.ListSecretValues()
	if err == nil {
		if len(vars) == 0 {
			fmt.Println("(no secrets set)")
			return
		}
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s=%s\n", k, vars[k])
		}
		return
	}
	if !strings.Contains(err.Error(), "403") {
		log.Fatalf("Failed to list secrets: %v", err)
	}

	keys, err := client.ListSecretKeys()
	if err != nil {
		log.Fatalf("Failed to list secrets: %v", err)
	}
	if len(keys) == 0 {
		fmt.Println("(no secrets set)")
		return
	}
	fmt.Println("(values hidden — admin role required)")
	for _, k := range keys {
		fmt.Println(k)
	}
}

func getSecret(key string) {
	if sess, ok := shared.UseClusterMode(); ok {
		warnEnvIgnoredInClusterMode(secretsEnvFlag)
		value, err := shared.NewAPIClient(sess).GetSecret(key)
		if err != nil {
			log.Fatalf("Failed to get secret: %v", err)
		}
		fmt.Println(value)
		return
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	target, err := resolveEnvironment(repoMgr, secretsEnvFlag)
	if err != nil {
		log.Fatalf("%v", err)
	}

	value, ok, err := repoMgr.GetSecret(target.ID, key)
	if err != nil {
		log.Fatalf("Failed to get secret: %v", err)
	}
	if !ok {
		log.Fatalf("%q is not set on environment '%s'", key, target.Name)
	}
	fmt.Println(value)
}

func setSecret(key, value string) {
	if sess, ok := shared.UseClusterMode(); ok {
		warnEnvIgnoredInClusterMode(secretsEnvFlag)
		if value == "" {
			var err error
			value, err = shared.PromptSecret(fmt.Sprintf("%s: ", key))
			if err != nil {
				log.Fatalf("Failed to read value: %v", err)
			}
		}
		if err := shared.NewAPIClient(sess).SetSecret(key, value); err != nil {
			log.Fatalf("Failed to set secret: %v", err)
		}
		fmt.Printf("Set %s\n", key)
		return
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	target, err := resolveEnvironment(repoMgr, secretsEnvFlag)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if value == "" {
		value, err = shared.PromptSecret(fmt.Sprintf("%s: ", key))
		if err != nil {
			log.Fatalf("Failed to read value: %v", err)
		}
	}

	if err := repoMgr.SetSecret(target.ID, key, value); err != nil {
		log.Fatalf("Failed to set secret: %v", err)
	}
	fmt.Printf("Set %s on environment '%s'\n", key, target.Name)
}

func unsetSecret(key string) {
	if sess, ok := shared.UseClusterMode(); ok {
		warnEnvIgnoredInClusterMode(secretsEnvFlag)
		if err := shared.NewAPIClient(sess).UnsetSecret(key); err != nil {
			log.Fatalf("Failed to unset secret: %v", err)
		}
		fmt.Printf("Unset %s\n", key)
		return
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	target, err := resolveEnvironment(repoMgr, secretsEnvFlag)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if err := repoMgr.UnsetSecret(target.ID, key); err != nil {
		log.Fatalf("Failed to unset secret: %v", err)
	}
	fmt.Printf("Unset %s on environment '%s'\n", key, target.Name)
}
