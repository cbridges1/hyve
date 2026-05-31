package module

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		lf, err := mod.LoadLockFile(repoPath)
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}
		locked := lf.GetLocked(source, version)
		if locked == nil {
			log.Fatalf("Module %s@%s not in hyve.lock — run `hyve module add %s %s`", source, version, source, version)
		}
		resolved, err := mod.Resolve(source, version, locked, repoPath)
		if err != nil {
			log.Fatalf("Failed to resolve module: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(resolved.Dir, "module.yaml"))
		if err != nil {
			log.Fatalf("Failed to read module.yaml: %v", err)
		}
		var manifest mod.ModuleManifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			log.Fatalf("Failed to parse module.yaml: %v", err)
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
		fmt.Printf("  Path:        %s\n", resolved.Dir)
	},
}
