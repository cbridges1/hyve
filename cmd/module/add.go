package module

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var addCmd = &cobra.Command{
	Use:   "add <source> <version>",
	Short: "Add a module to hyve.lock",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		version := args[1]
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		lf, err := mod.LoadLockFile(repoPath)
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}
		if lf.GetLocked(source, version) != nil {
			log.Printf("Already locked: %s@%s", source, version)
			return
		}
		log.Printf("Resolving %s@%s...", source, version)
		resolved, err := mod.Resolve(source, version, nil, repoPath)
		if err != nil {
			log.Fatalf("Failed to resolve module: %v", err)
		}
		lf.SetLocked(source, version, &mod.LockedModule{
			Source:   source,
			Resolved: resolved.Resolved,
			SHA256:   resolved.SHA256,
			Runner:   resolved.Runner,
		})
		if err := mod.SaveLockFile(repoPath, lf); err != nil {
			log.Fatalf("Failed to save lock file: %v", err)
		}
		log.Printf("✅ Locked %s@%s (sha256: %s)", source, version, resolved.SHA256)
		shared.CommitStateChanges(ctx, stateMgr, "chore: add module "+source+"@"+version)
	},
}
