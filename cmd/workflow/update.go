package workflow

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/workflowref"
)

var workflowUpdateCmd = &cobra.Command{
	Use:   "update <source>",
	Short: "Re-resolve a remote workflow reference to latest and refresh its hyve.lock entry(ies)",
	Long: `Version is part of the source string (github.com/org/repo[//path][@version]);
unlike a version-argument command, there is no separate <version> argument.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve workflow update` is not supported in cluster mode yet — hyve.lock-based remote-ref resolution is a local-checkout concept only.")
		}
		source := args[0]
		pathFlag, _ := cmd.Flags().GetString("path")

		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		log.Printf("Re-resolving %s ...", source)
		updated, err := workflowref.Update(repoPath, source, pathFlag)
		if err != nil {
			log.Fatalf("%v", err)
		}
		for _, f := range updated {
			log.Printf("✅ Updated %s@%s (name=%s, sha256=%s)", f.CanonicalSource, f.RawVersion, f.Name, f.SHA256)
		}

		shared.CommitStateChanges(ctx, stateMgr, "chore: update workflow "+source)
	},
}

func init() {
	workflowUpdateCmd.Flags().String("path", "", "Override/supply the source's path component")
	workflowCmd.AddCommand(workflowUpdateCmd)
}
