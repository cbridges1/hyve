package module

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var validateCmd = &cobra.Command{
	Use:   "validate <source> <version>",
	Short: "Validate that a locked module has the expected structure",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve module validate` is local-mode only — it checks a locked module's structure against your local hyve.lock, which has no equivalent in cluster mode.")
		}
		source := args[0]
		version := args[1]
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		defs, defsErr := stateMgr.LoadClusterDefinitions()
		if defsErr != nil {
			log.Fatalf("Failed to load cluster definitions: %v", defsErr)
		}
		existingClusterNames := make([]string, len(defs))
		for i, d := range defs {
			existingClusterNames[i] = d.Metadata.Name
		}

		errors, err := mod.ValidateModule(repoPath, source, version, existingClusterNames)
		if err != nil {
			log.Fatalf("%v", err)
		}

		if len(errors) > 0 {
			fmt.Printf("❌ Validation failed for %s@%s:\n", source, version)
			for _, e := range errors {
				fmt.Printf("  - %s\n", e)
			}
			os.Exit(1)
		}
		fmt.Printf("✅ %s@%s is valid\n", source, version)
	},
}
