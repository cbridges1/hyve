// Package env implements `hyve env` — the sole mechanism for registering
// and switching between environments: named local directories hyve reads
// and writes cluster definitions from (see internal/reconcile.StateProvider)
// paired, optionally, with cluster-mode login credentials (see 'hyve
// login'). Git sync is not a native hyve capability: these commands only
// ever manage entries in internal/repository's registry, they never clone,
// pull, commit, or push anything. If a directory happens to be a git
// checkout, that's between the user and their own `git` binary — hyve
// doesn't care either way.
package env

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

var (
	createPath     string
	createAPIURL   string
	createUsername string
	createPassword string
)

var createCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Register a new environment and make it active",
	Long: `Registers a new environment — a local directory hyve reads/writes cluster
definitions from, optionally paired with cluster-mode login credentials —
and makes it active immediately.

--path defaults to the current working directory when omitted. name
defaults to --path's directory basename (deduplicated with -2, -3, ... on
collision) when omitted — give one explicitly to pick your own.

Passing --api-url also logs in immediately, equivalent to running 'hyve
login' right after creating the environment. Omit it for a pure local/
GitOps environment that never talks to a hyve API server.

Registration only — this never clones, pulls, commits, or pushes anything.
If you want the directory kept in sync with a git remote, that's entirely
your own 'git' CLI: 'git clone' it yourself before registering it, then
'git pull'/'git add'/'git commit'/'git push' around your workflow as usual.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		runCreate(name)
	},
}

var Cmd = &cobra.Command{
	Use:   "env",
	Short: "Show or manage environments (local directory + optional cluster-mode login)",
	Long:  "See subcommands to create, list, switch, show, or remove registered environments.",
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the currently-active environment",
	Run: func(cmd *cobra.Command, args []string) {
		showCurrent()
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered environments",
	Long:  "Display every environment registered via 'hyve env create' and which one is currently active.",
	Run: func(cmd *cobra.Command, args []string) {
		listEnvironments()
	},
}

var useCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch the active environment",
	Long:  "Set the named environment as current — switches its directory and any attached cluster-mode login together.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switchEnvironment(args[0])
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a registered environment",
	Long: `Remove the named environment from hyve's registry, including any attached
cluster-mode credentials. The directory itself and everything in it is left
untouched on disk, since hyve never owned it in the first place.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeEnvironment(args[0])
	},
}

var pathCmd = &cobra.Command{
	Use:   "path [name]",
	Short: "Print a registered environment's local filesystem path",
	Long: `Print the absolute local path for the current environment (or a named one,
given as an argument). Nothing else is written to stdout, so it composes
with shell substitution:

  cd "$(hyve env path)"
  cd "$(hyve env path my-other-env)"`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		printPath(name)
	},
}

func init() {
	createCmd.Flags().StringVar(&createPath, "path", "", "Local directory to register (default: current working directory)")
	createCmd.Flags().StringVar(&createAPIURL, "api-url", "", "Base URL of a hyve API server — also logs in immediately if given")
	createCmd.Flags().StringVar(&createUsername, "username", "", "Username for --api-url (omit to be prompted)")
	createCmd.Flags().StringVar(&createPassword, "password", "", "Password for --api-url (scripting only — omit to be prompted without echo)")

	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(currentCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(useCmd)
	Cmd.AddCommand(removeCmd)
	Cmd.AddCommand(pathCmd)
}

func runCreate(name string) {
	path := createPath
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Failed to resolve current directory: %v", err)
		}
		path = cwd
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Fatalf("Failed to resolve path %q: %v", path, err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		log.Fatalf("Failed to create directory %q: %v", abs, err)
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if name == "" {
		name = shared.UniqueEnvironmentName(repoMgr, filepath.Base(abs))
	}

	if _, err := repoMgr.AddRepository(name, "", abs); err != nil {
		log.Fatalf("Failed to register '%s': %v", name, err)
	}
	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to activate '%s': %v", name, err)
	}

	log.Printf("✅ '%s' (%s) is now the active environment", name, abs)

	if createAPIURL != "" {
		username := createUsername
		if username == "" {
			username, err = shared.PromptLine("Username: ")
			if err != nil {
				log.Fatalf("Failed to read username: %v", err)
			}
		}
		password := createPassword
		if password == "" {
			password, err = shared.PromptSecret("Password: ")
			if err != nil {
				log.Fatalf("Failed to read password: %v", err)
			}
		}

		apiURL := strings.TrimRight(createAPIURL, "/")
		token, expiresAt, err := shared.PerformLogin(apiURL, username, password)
		if err != nil {
			log.Fatal(err)
		}
		if err := repoMgr.SetSession(name, apiURL, token, expiresAt); err != nil {
			log.Fatalf("Failed to attach session: %v", err)
		}
		log.Printf("✅ Logged in as %s (session expires %s)", username, expiresAt)
	}

	log.Println("💡 Run 'hyve env' any time to see what's active")
}

func showCurrent() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No active environment. Use 'hyve env create' to register one.")
	}

	fmt.Printf("%s\n  %s\n", current.Name, current.LocalPath)
	if current.RepoURL != "" {
		fmt.Printf("  (git: %s)\n", current.RepoURL)
	}
	if current.LoggedIn() {
		fmt.Printf("  (api: %s)\n", current.APIURL)
	}
}

func listEnvironments() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	envs, err := repoMgr.ListRepositories()
	if err != nil {
		log.Fatalf("Failed to list environments: %v", err)
	}

	if len(envs) == 0 {
		log.Println("❌ No environments registered")
		log.Println("\nRegister one with: hyve env create [name]")
		return
	}

	log.Printf("📁 Registered environments (%d):\n", len(envs))

	for _, e := range envs {
		status := ""
		if e.IsCurrent {
			status = " (current) ⭐"
		}

		log.Printf("  %s%s", e.Name, status)
		log.Printf("    Local: %s", e.LocalPath)
		if e.RepoURL != "" {
			log.Printf("    Git remote: %s", e.RepoURL)
		}
		if e.LoggedIn() {
			log.Printf("    API: %s", e.APIURL)
		}
		log.Printf("    Registered: %s", e.CreatedAt.Format("2006-01-02 15:04"))
		log.Println()
	}
}

func switchEnvironment(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to switch environment: %v", err)
	}

	log.Printf("✅ Switched to '%s'", name)

	env, err := repoMgr.GetRepositoryByName(name)
	if err != nil {
		log.Fatalf("Failed to get environment details: %v", err)
	}

	log.Printf("Local path: %s", env.LocalPath)
	if env.LoggedIn() {
		log.Printf("API: %s", env.APIURL)
	}
}

func removeEnvironment(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if _, err := repoMgr.GetRepositoryByName(name); err != nil {
		log.Fatalf("Failed to look up '%s': %v", name, err)
	}

	if err := repoMgr.DeleteRepository(name); err != nil {
		log.Fatalf("Failed to remove '%s': %v", name, err)
	}

	log.Printf("✅ '%s' removed from the registry (directory on disk left untouched)", name)

	envs, err := repoMgr.ListRepositories()
	if err == nil && len(envs) > 0 {
		if current, err := repoMgr.GetCurrentRepository(); err == nil {
			log.Printf("Current environment is now: %s", current.Name)
		}
	} else {
		log.Println("No environments remaining. Register one with: hyve env create")
	}
}

func printPath(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	var env *repository.Repository
	if name != "" {
		env, err = repoMgr.GetRepositoryByName(name)
	} else {
		env, err = repoMgr.GetCurrentRepository()
	}
	if err != nil {
		log.Fatalf("%v", err)
	}

	fmt.Println(env.LocalPath)
}
