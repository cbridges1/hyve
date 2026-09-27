// Package contextcmd implements `hyve context` — the sole mechanism for
// registering and switching between contexts. (Named contextcmd, not
// context, so it doesn't shadow the standard library package.) Formerly
// `hyve env`; renamed so it can't be confused with `hyve environment`, an
// organization's server-side sub-scope on a hyve-api server (see
// cmd/environment) — a context is this machine's own record of where the
// CLI's state lives. A context is a named entry in
// internal/repository's registry, and comes in two independent kinds that
// may be set on the same entry or separately: a local directory (--path)
// hyve reads/writes cluster definitions from (see
// internal/reconcile.StateProvider), and/or a cluster API URL (--api-url)
// pre-registered for `hyve context login` to target later. Registering a cluster
// context's URL is not the same as authenticating against it — no
// credential is stored here at all; see internal/session for `hyve
// login`'s own, separate, machine-wide session storage, which is what
// actually authenticates. That separation is deliberate: a local directory
// (or cluster URL) and a cluster-mode *session* used to be the same
// database row, and that conflation is why logging out or letting a
// session expire used to make cluster-mode commands silently fall back to
// whatever local files happened to be sitting in the current context's
// own directory — see internal/session's own doc comment. Git sync is not
// a native hyve capability either: these commands only ever manage entries
// in internal/repository's registry, they never clone, pull, commit, or
// push anything. If a directory happens to be a git checkout, that's
// between the user and their own `git` binary — hyve doesn't care either
// way.
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

var (
	createPath       string
	createAPIURL     string
	createCACertPath string
)

var createCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Register a new context and make it active",
	Long: `Registers a new context and makes it active immediately. A context
is a local directory (--path), a cluster API URL (--api-url), or both —
independent kinds of entry in the same registry.

--path defaults to the current working directory when omitted, unless
--api-url is given alone, in which case no local directory is
registered/created at all. name defaults to --path's directory basename
(deduplicated with -2, -3, ... on collision) when omitted — give one
explicitly to pick your own, which --api-url-only registration requires
(there's no directory to derive a default from).

--api-url only remembers where to point 'hyve context login' at later — it stores
no credential and does not authenticate anything by itself. Cluster-mode
access still requires running 'hyve context login' separately (with no --api-url of
its own, it defaults to the current context's --api-url); login is a
single, global credential independent of which context is active (see
'hyve context login's own --help).

--ca-cert (only meaningful alongside --api-url) names a PEM-encoded CA
certificate to trust in addition to the system trust store when talking
to --api-url — for an install whose TLS certificate isn't publicly
trusted (a self-signed CA for a bare IP/nip.io address with no real
domain). Not a credential either, same as --api-url itself; can also be
set later via 'hyve context login --ca-cert', which persists it onto whichever
context matches the URL you log into.

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
	Short: "Choose where the CLI's state lives: a local directory or a hyve-api server",
	Long: `A context is this machine's record of where hyve reads and writes state: a
local directory of cluster definitions, a hyve-api server to log into, or
both. See subcommands to create, list, switch, show, or remove contexts.
Cluster-mode login ('hyve context login') is separate — see its own --help.

Not the same as 'hyve environment', which selects one of your
organization's named scopes on a hyve-api server. Each context that points
at a server remembers its own selected environment.`,
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
	createCmd.Flags().StringVar(&createPath, "path", "", "Local directory to register (default: current working directory, unless --api-url is given alone)")
	createCmd.Flags().StringVar(&createAPIURL, "api-url", "", "Cluster API URL to pre-register for 'hyve context login' to target later (stores no credential)")
	createCmd.Flags().StringVar(&createCACertPath, "ca-cert", "", "Path to a PEM-encoded CA certificate to trust in addition to the system trust store for --api-url (not a credential)")

	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(currentCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(useCmd)
	Cmd.AddCommand(removeCmd)
	Cmd.AddCommand(pathCmd)
}

func runCreate(name string) {
	// --api-url alone registers a cluster context with no local
	// directory at all — the whole point is that this shouldn't require
	// (or silently create) a directory just to remember a URL.
	apiURLOnly := createAPIURL != "" && createPath == ""

	abs := ""
	if !apiURLOnly {
		path := createPath
		if path == "" {
			cwd, err := os.Getwd()
			if err != nil {
				log.Fatalf("Failed to resolve current directory: %v", err)
			}
			path = cwd
		}
		var err error
		abs, err = filepath.Abs(path)
		if err != nil {
			log.Fatalf("Failed to resolve path %q: %v", path, err)
		}
		if err := os.MkdirAll(abs, 0755); err != nil {
			log.Fatalf("Failed to create directory %q: %v", abs, err)
		}
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if name == "" {
		if apiURLOnly {
			log.Fatal("name is required when registering a context with --api-url and no --path")
		}
		name = shared.UniqueContextName(repoMgr, filepath.Base(abs))
	}

	if _, err := repoMgr.AddRepository(name, "", abs, createAPIURL); err != nil {
		log.Fatalf("Failed to register '%s': %v", name, err)
	}
	if createCACertPath != "" {
		data, err := os.ReadFile(createCACertPath)
		if err != nil {
			log.Fatalf("Failed to read --ca-cert %s: %v", createCACertPath, err)
		}
		if err := repoMgr.SetAPICACert(name, string(data)); err != nil {
			log.Fatalf("Failed to store --ca-cert for '%s': %v", name, err)
		}
	}
	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to activate '%s': %v", name, err)
	}

	switch {
	case apiURLOnly:
		log.Printf("✅ '%s' (%s) is now the active context", name, createAPIURL)
		log.Println("💡 Run 'hyve context login' to authenticate against it")
	case createAPIURL != "":
		log.Printf("✅ '%s' (%s, api: %s) is now the active context", name, abs, createAPIURL)
	default:
		log.Printf("✅ '%s' (%s) is now the active context", name, abs)
	}
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
