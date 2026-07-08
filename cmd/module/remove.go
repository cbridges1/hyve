package module

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var removeCmd = &cobra.Command{
	Use:   "remove <source> <version>",
	Short: "Remove a module from hyve.lock",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		version := args[1]
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		removed, err := mod.RemoveModule(repoPath, source, version)
		if err != nil {
			log.Fatalf("%v", err)
		}
		if !removed {
			log.Printf("Not locked: %s@%s", source, version)
			return
		}
		log.Printf("✅ Removed %s@%s from hyve.lock", source, version)
		shared.CommitStateChanges(ctx, stateMgr, "chore: remove module "+source+"@"+version)
	},
}
