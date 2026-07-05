package module

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var updateCmd = &cobra.Command{
	Use:   "update <source> <version>",
	Short: "Re-resolve a locked module to refresh its SHA256",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		version := args[1]
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		log.Printf("Re-resolving %s@%s...", source, version)
		resolved, err := mod.UpdateModule(repoPath, source, version)
		if err != nil {
			log.Fatalf("%v", err)
		}
		log.Printf("✅ Updated %s@%s (sha256: %s)", source, version, resolved.SHA256)
		shared.CommitStateChanges(ctx, stateMgr, "chore: update module "+source+"@"+version)
	},
}
