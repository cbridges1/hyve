// Package state implements `hyve set-state` and `hyve state` — the sole
// mechanism for registering and managing local directories hyve reads and
// writes cluster definitions from. Git sync is not a native hyve capability
// (see internal/reconcile.StateProvider): these commands only ever manage
// entries in internal/repository's registry, they never clone, pull, commit,
// or push anything. If a directory happens to be a git checkout, that's
// between the user and their own `git` binary — hyve doesn't care either way.
package state

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/repository"
)

// SetStateCmd registers a local directory as the active state directory.
var SetStateCmd = &cobra.Command{
	Use:   "set-state [path]",
	Short: "Make a local directory the active state directory",
	Long: `Registers a local directory as the active state directory hyve reads and
writes cluster definitions from — no git repository or remote required.

Defaults to the current working directory when no path is given (or when
--current is passed explicitly). Useful for local development: cd into a
project directory and run this to make it active immediately.

Registration only — this never clones, pulls, commits, or pushes anything.
If you want the directory kept in sync with a git remote, that's entirely
your own 'git' CLI: 'git clone' it yourself before registering it, then
'git pull'/'git add'/'git commit'/'git push' around your workflow as usual.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		useCurrent, _ := cmd.Flags().GetBool("current")

		target := ""
		if len(args) > 0 {
			target = args[0]
		}
		if target == "" || useCurrent {
			cwd, err := os.Getwd()
			if err != nil {
				log.Fatalf("Failed to resolve current directory: %v", err)
			}
			target = cwd
		}

		setState(target)
	},
}

func init() {
	SetStateCmd.Flags().Bool("current", false, "Use the current working directory (default when no path is given)")
}

func setState(path string) {
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

	name := uniqueName(repoMgr, filepath.Base(abs))

	if _, err := repoMgr.AddRepository(name, "", abs); err != nil {
		log.Fatalf("Failed to register %q: %v", abs, err)
	}
	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to set '%s' as current: %v", name, err)
	}

	log.Printf("✅ '%s' (%s) is now the active state directory", name, abs)
	log.Println("💡 Run 'hyve state' any time to see what's active")
}

// uniqueName returns base, or base-2, base-3, ... — whichever is the first
// name not already registered.
func uniqueName(repoMgr *repository.Manager, base string) string {
	name := base
	for i := 2; ; i++ {
		if _, err := repoMgr.GetRepositoryByName(name); err != nil {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// Cmd prints the currently-active state directory. Also reachable as
// `hyve state current` for clarity/scriptability, and is the parent for the
// registry-management subcommands (`list`/`use`/`remove`/`path`).
var Cmd = &cobra.Command{
	Use:   "state",
	Short: "Show or manage registered state directories",
	Long:  "Prints the registered name and local path of the currently-active state directory. See subcommands to list, switch, or remove registered directories.",
	Run: func(cmd *cobra.Command, args []string) {
		showCurrent()
	},
}

var stateCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the currently-active state directory (same as `hyve state`)",
	Run: func(cmd *cobra.Command, args []string) {
		showCurrent()
	},
}

var stateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered state directories",
	Long:  "Display every directory registered via 'hyve set-state' and which one is currently active.",
	Run: func(cmd *cobra.Command, args []string) {
		listStateDirectories()
	},
}

var stateUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch the active state directory",
	Long:  "Set the specified registered directory as the current active one.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switchState(args[0])
	},
}

var stateRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a registered state directory",
	Long: `Remove the specified directory from hyve's registry. This only drops the
registration — the directory itself and everything in it is left untouched
on disk, since hyve never owned it in the first place.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeState(args[0])
	},
}

var statePathCmd = &cobra.Command{
	Use:   "path [name]",
	Short: "Print a registered directory's local filesystem path",
	Long: `Print the absolute local path for the current state directory (or a named
one, given as an argument). Nothing else is written to stdout, so it
composes with shell substitution:

  cd "$(hyve state path)"
  cd "$(hyve state path my-other-dir)"`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		printStatePath(name)
	},
}

func init() {
	Cmd.AddCommand(stateCurrentCmd)
	Cmd.AddCommand(stateListCmd)
	Cmd.AddCommand(stateUseCmd)
	Cmd.AddCommand(stateRemoveCmd)
	Cmd.AddCommand(statePathCmd)
}

func showCurrent() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No active state directory. Use 'hyve set-state' to register one.")
	}

	fmt.Printf("%s\n  %s\n", current.Name, current.LocalPath)
	if current.RepoURL != "" {
		fmt.Printf("  (git: %s)\n", current.RepoURL)
	}
}

func listStateDirectories() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	dirs, err := repoMgr.ListRepositories()
	if err != nil {
		log.Fatalf("Failed to list state directories: %v", err)
	}

	if len(dirs) == 0 {
		log.Println("❌ No state directories registered")
		log.Println("\nRegister one with: hyve set-state [path]")
		return
	}

	log.Printf("📁 Registered state directories (%d):\n", len(dirs))

	for _, d := range dirs {
		status := ""
		if d.IsCurrent {
			status = " (current) ⭐"
		}

		log.Printf("  %s%s", d.Name, status)
		log.Printf("    Local: %s", d.LocalPath)
		if d.RepoURL != "" {
			log.Printf("    Git remote: %s", d.RepoURL)
		}
		log.Printf("    Registered: %s", d.CreatedAt.Format("2006-01-02 15:04"))
		log.Println()
	}
}

func switchState(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to switch state directory: %v", err)
	}

	log.Printf("✅ Switched to '%s'", name)

	repo, err := repoMgr.GetRepositoryByName(name)
	if err != nil {
		log.Fatalf("Failed to get directory details: %v", err)
	}

	log.Printf("Local path: %s", repo.LocalPath)
}

func removeState(name string) {
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

	dirs, err := repoMgr.ListRepositories()
	if err == nil && len(dirs) > 0 {
		if current, err := repoMgr.GetCurrentRepository(); err == nil {
			log.Printf("Current state directory is now: %s", current.Name)
		}
	} else {
		log.Println("No state directories remaining. Register one with: hyve set-state [path]")
	}
}

func printStatePath(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	var repo *repository.Repository
	if name != "" {
		repo, err = repoMgr.GetRepositoryByName(name)
	} else {
		repo, err = repoMgr.GetCurrentRepository()
	}
	if err != nil {
		log.Fatalf("%v", err)
	}

	fmt.Println(repo.LocalPath)
}
