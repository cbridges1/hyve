package module

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List locked modules from hyve.lock",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()
		lf, err := mod.LoadLockFile(repoPath)
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}
		if len(lf.Modules) == 0 {
			fmt.Println("No modules locked.")
			fmt.Println("\nAdd one with: hyve module add <source> <version>")
			return
		}
		keys := make([]string, 0, len(lf.Modules))
		for k := range lf.Modules {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("📦 Locked modules (%d):\n", len(keys))
		for _, k := range keys {
			m := lf.Modules[k]
			fmt.Printf("  %s\n", k)
			fmt.Printf("    sha256:   %s\n", m.SHA256)
			fmt.Printf("    resolved: %s\n", m.Resolved)
			if m.Runner.Image != "" {
				fmt.Printf("    runner:   %s\n", m.Runner.Image)
			}
		}
	},
}
