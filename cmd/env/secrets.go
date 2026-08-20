package env

import (
	"fmt"
	"log"
	"sort"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

var secretsEnvFlag string

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets attached to an environment",
	Long: `Commands to read and update KEY=VALUE secrets attached to an environment,
stored in hyve.db alongside the rest of its registration — not tied to any
repository/folder, and removed automatically when the environment itself is
(see 'hyve env remove').

Loaded into the process environment before every command (see 'hyve
reconcile', 'hyve workflow run') — takes precedence over a legacy
repo-relative hyve.yaml env.file, if one is also configured.`,
}

var secretsListCmd = &cobra.Command{
	Use:   "list [name]",
	Short: "Print every KEY=VALUE secret attached to an environment",
	Long:  "Defaults to the currently-active environment when name is omitted.",
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
	secretsGetCmd.Flags().StringVar(&secretsEnvFlag, "env", "", "Target environment (default: the active one)")
	secretsSetCmd.Flags().StringVar(&secretsEnvFlag, "env", "", "Target environment (default: the active one)")
	secretsUnsetCmd.Flags().StringVar(&secretsEnvFlag, "env", "", "Target environment (default: the active one)")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsUnsetCmd)
	Cmd.AddCommand(secretsCmd)
}

// resolveEnvironment returns the named environment, or the current one if
// name is empty — the common resolution every secrets subcommand needs.
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

func getSecret(key string) {
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
