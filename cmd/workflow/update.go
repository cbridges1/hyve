package workflow

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/workflowref"
)

var workflowUpdateCmd = &cobra.Command{
	Use:   "update <source>",
	Short: "Re-resolve a remote workflow reference to latest and refresh its hyve.lock entry(ies)",
	Long: `Version is part of the source string (github.com/org/repo[//path][@version]);
unlike a version-argument command, there is no separate <version> argument.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		pathFlag, _ := cmd.Flags().GetString("path")

		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		lf, err := mod.LoadLockFile(repoPath)
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}

		log.Printf("Re-resolving %s ...", source)
		// nil lf forces a full re-resolve, bypassing any cache short-circuit.
		files, err := workflowref.Resolve(source, pathFlag, nil)
		if err != nil {
			log.Fatalf("Failed to resolve workflow: %v", err)
		}
		for _, f := range files {
			lf.SetLockedWorkflow(f.CanonicalSource, f.RawVersion, &mod.LockedWorkflow{
				Name:     f.Name,
				Source:   f.CanonicalSource,
				Resolved: f.Resolved,
				SHA256:   f.SHA256,
			})
			log.Printf("✅ Updated %s@%s (name=%s, sha256=%s)", f.CanonicalSource, f.RawVersion, f.Name, f.SHA256)
		}

		if err := mod.SaveLockFile(repoPath, lf); err != nil {
			log.Fatalf("Failed to save lock file: %v", err)
		}
		shared.CommitStateChanges(ctx, stateMgr, "chore: update workflow "+source)
	},
}

func init() {
	workflowUpdateCmd.Flags().String("path", "", "Override/supply the source's path component")
	workflowCmd.AddCommand(workflowUpdateCmd)
}
