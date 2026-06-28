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

var validateCmd = &cobra.Command{
	Use:   "validate <source> <version>",
	Short: "Validate that a locked module has the expected structure",
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
			log.Fatalf("Module %s@%s not in hyve.lock", source, version)
		}
		resolved, err := mod.Resolve(source, version, locked, repoPath)
		if err != nil {
			log.Fatalf("Failed to resolve module: %v", err)
		}

		var errors []string
		manifestPath := filepath.Join(resolved.Dir, "module.yaml")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("missing module.yaml: %v", err))
		} else {
			var manifest mod.ModuleManifest
			if err := yaml.Unmarshal(data, &manifest); err != nil {
				errors = append(errors, fmt.Sprintf("invalid module.yaml: %v", err))
			} else {
				if manifest.Metadata.Name == "" {
					errors = append(errors, "module.yaml: metadata.name is required")
				}
				if manifest.Metadata.Version == "" {
					errors = append(errors, "module.yaml: metadata.version is required")
				}
			}
		}

		// Check at least one of create/status exists
		ops := []string{"create", "delete", "status", "auth", "scale"}
		anyOp := false
		for _, op := range ops {
			for _, ext := range []string{".yaml", ".sh", ""} {
				p := filepath.Join(resolved.Dir, op+ext)
				if _, err := os.Stat(p); err == nil {
					anyOp = true
					break
				}
			}
		}
		if !anyOp {
			errors = append(errors, "no operation files found (expected create.yaml/create.sh, etc.)")
		}

		// Validate auth.yaml if present
		authPath := filepath.Join(resolved.Dir, "auth.yaml")
		if authData, err := os.ReadFile(authPath); err == nil {
			var ca mod.ClusterAuth
			if err := yaml.Unmarshal(authData, &ca); err != nil {
				errors = append(errors, fmt.Sprintf("auth.yaml: invalid YAML: %v", err))
			} else if len(ca.Spec.Methods) > 0 {
				validExports := map[string]bool{"": true, "KUBECONFIG": true, "KEEPER_KUBECONFIG": true}
				seen := map[string]bool{}
				for i, m := range ca.Spec.Methods {
					if m.Name == "" {
						errors = append(errors, fmt.Sprintf("auth.yaml: methods[%d]: name is required", i))
					} else if seen[m.Name] {
						errors = append(errors, fmt.Sprintf("auth.yaml: duplicate method name %q", m.Name))
					} else {
						seen[m.Name] = true
					}
					if m.Auth.Script == "" {
						errors = append(errors, fmt.Sprintf("auth.yaml: method %q: auth.script is required", m.Name))
					}
					if !validExports[m.Exports] {
						errors = append(errors, fmt.Sprintf("auth.yaml: method %q: exports must be KUBECONFIG, KEEPER_KUBECONFIG, or empty", m.Name))
					}
				}
			}
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
