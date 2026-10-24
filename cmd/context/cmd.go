//go:build ignore

// Package contextcmd implements `hyve context` — the sole mechanism for
// registering and switching between contexts. (Named contextcmd, not
// context, so it doesn't shadow the standard library package.) A context
// is a named entry in internal/repository's registry: a local directory
// hyve reads/writes cluster definitions from (see
// internal/reconcile.StateProvider). Contexts that point at a hyve-api
// server (--api-url) can no longer be created: hyve-api's login was
// replaced by 'hyve login' (HYVE-PROPOSAL.md, "Signing in"). Existing ones
// still list, and cluster commands refuse to run against them. Git sync is
// not a native hyve capability either: these commands only ever manage
// entries in internal/repository's registry, they never clone, pull,
// commit, or push anything.
package contextcmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

var createPath string

var createCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Register a new context and make it active",
	Long: `Registers a local directory as a new context and makes it active
immediately. --path defaults to the current working directory. name
defaults to --path's directory basename (deduplicated with -2, -3, ... on
collision) when omitted.

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
	Use:   "context",
	Short: "Choose where the CLI's state lives",
	Long: `A context is this machine's record of where hyve reads and writes state: a
local directory of cluster definitions. See subcommands to create, list,
switch, show, or remove contexts.

Signing in to a hyve host is 'hyve login'.`,
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the currently-active context",
	Run: func(cmd *cobra.Command, args []string) {
		showCurrent()
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered contexts",
	Long:  "Display every context registered via 'hyve context create' and which one is currently active.",
	Run: func(cmd *cobra.Command, args []string) {
		listContexts()
	},
}

var useCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch the active context",
	Long:  "Set the named context as current.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switchContext(args[0])
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a registered context",
	Long: `Remove the named context from hyve's registry. The directory itself and
everything in it is left untouched on disk, since hyve never owned it in
the first place.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeContext(args[0])
	},
}

var pathCmd = &cobra.Command{
	Use:   "path [name]",
	Short: "Print a registered context's local filesystem path",
	Long: `Print the absolute local path for the current context (or a named one,
given as an argument). Nothing else is written to stdout, so it composes
with shell substitution:

  cd "$(hyve context path)"
  cd "$(hyve context path my-other-context)"`,
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
		name = shared.UniqueContextName(repoMgr, filepath.Base(abs))
	}

	if _, err := repoMgr.AddRepository(name, "", abs, ""); err != nil {
		log.Fatalf("Failed to register '%s': %v", name, err)
	}
	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to activate '%s': %v", name, err)
	}

	log.Printf("✅ '%s' (%s) is now the active context", name, abs)
	log.Println("💡 Run 'hyve context' any time to see what's active")
}

func showCurrent() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No active context. Use 'hyve context create' to register one.")
	}

	fmt.Printf("%s\n", current.Name)
	if current.LocalPath != "" {
		fmt.Printf("  Local: %s\n", current.LocalPath)
	}
	if current.RepoURL != "" {
		fmt.Printf("  (git: %s)\n", current.RepoURL)
	}
	if current.APIURL != "" {
		fmt.Printf("  API: %s\n", current.APIURL)
		if current.ServerEnvironment != "" {
			fmt.Printf("  Environment: %s\n", current.ServerEnvironment)
		}
	}
}

func listContexts() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	envs, err := repoMgr.ListRepositories()
	if err != nil {
		log.Fatalf("Failed to list contexts: %v", err)
	}

	if len(envs) == 0 {
		log.Println("❌ No contexts registered")
		log.Println("\nRegister one with: hyve context create [name]")
		return
	}

	log.Printf("📁 Registered contexts (%d):\n", len(envs))

	for _, e := range envs {
		status := ""
		if e.IsCurrent {
			status = " (current) ⭐"
		}

		log.Printf("  %s%s", e.Name, status)
		if e.LocalPath != "" {
			log.Printf("    Local: %s", e.LocalPath)
		}
		if e.RepoURL != "" {
			log.Printf("    Git remote: %s", e.RepoURL)
		}
		if e.APIURL != "" {
			log.Printf("    API: %s", e.APIURL)
			if e.ServerEnvironment != "" {
				log.Printf("    Environment: %s", e.ServerEnvironment)
			}
		}
		log.Printf("    Registered: %s", e.CreatedAt.Format("2006-01-02 15:04"))
		log.Println()
	}
}

func switchContext(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to switch context: %v", err)
	}

	log.Printf("✅ Switched to '%s'", name)

	env, err := repoMgr.GetRepositoryByName(name)
	if err != nil {
		log.Fatalf("Failed to get context details: %v", err)
	}

	if env.LocalPath != "" {
		log.Printf("Local path: %s", env.LocalPath)
	}
	if env.APIURL != "" {
		log.Printf("API: %s", env.APIURL)
	}
}

func removeContext(name string) {
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
			log.Printf("Current context is now: %s", current.Name)
		}
	} else {
		log.Println("No contexts remaining. Register one with: hyve context create")
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
	if env.LocalPath == "" {
		log.Fatalf("'%s' has no local directory (it's a cluster context, api: %s)", env.Name, env.APIURL)
	}

	fmt.Println(env.LocalPath)
}
