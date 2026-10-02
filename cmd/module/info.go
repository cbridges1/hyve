package module

import (
	"context"
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var infoCmd = &cobra.Command{
	Use:   "info <source> <version>",
	Short: "Show metadata for a locked module",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		version := args[1]

		if sess, ok := shared.UseClusterMode(); ok {
			showModuleAPI(shared.NewAPIClient(sess), mod.CRName(source, version))
			return
		}

		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		manifest, resolvedDir, err := mod.ModuleInfo(repoPath, source, version)
		if err != nil {
			log.Fatalf("%v", err)
		}

		fmt.Printf("📦 %s@%s\n", source, version)
		fmt.Printf("  Name:        %s\n", manifest.Metadata.Name)
		fmt.Printf("  Version:     %s\n", manifest.Metadata.Version)
		fmt.Printf("  Description: %s\n", manifest.Metadata.Description)
		if manifest.Metadata.Author != "" {
			fmt.Printf("  Author:      %s\n", manifest.Metadata.Author)
		}
		if manifest.Metadata.License != "" {
			fmt.Printf("  License:     %s\n", manifest.Metadata.License)
		}
		if len(manifest.Spec.Params) > 0 {
			fmt.Println("  Params:")
			for _, p := range manifest.Spec.Params {
				req := ""
				if p.Required {
					req = " (required)"
				}
				fmt.Printf("    - %s%s: %s\n", p.Name, req, p.Description)
				if p.Default != "" {
					fmt.Printf("        default: %s\n", p.Default)
				}
			}
		}
		fmt.Printf("  Path:        %s\n", resolvedDir)
	},
}
