// Package state implements `hyve set-state` and `hyve state` — local-dev
// convenience commands for pointing hyve at a plain local directory without
// any git ceremony. Git sync is not a native hyve capability (see
// internal/reconcile.StateProvider); these commands only ever register a
// directory in the same repository registry `hyve git add` uses, they never
// clone, pull, commit, or push anything.
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
If you also want the directory kept in sync with a git remote, that's a
separate step: use 'hyve git add' instead, or 'hyve git pull'/'hyve git push'
around your own workflow.`,
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
// `hyve state current` for clarity/scriptability.
var Cmd = &cobra.Command{
	Use:   "state",
	Short: "Show the currently-active state directory",
	Long:  "Prints the registered name and local path of the currently-active state directory.",
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

func init() {
	Cmd.AddCommand(stateCurrentCmd)
}

func showCurrent() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No active state directory. Use 'hyve set-state' or 'hyve git add' to configure one.")
	}

	fmt.Printf("%s\n  %s\n", current.Name, current.LocalPath)
	if current.RepoURL != "" {
		fmt.Printf("  (git: %s)\n", current.RepoURL)
	}
}
