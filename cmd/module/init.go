package module

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Scaffold a new module skeleton in ./modules/<name>/",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		dir := filepath.Join("modules", name)
		if _, err := os.Stat(dir); err == nil {
			log.Fatalf("Directory %s already exists", dir)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("Failed to create %s: %v", dir, err)
		}

		manifest := fmt.Sprintf(`apiVersion: v1
kind: Module
metadata:
  name: %s
  version: 0.1.0
  description: TODO — describe what this module manages
spec:
  params:
    - name: example
      description: Example parameter
      required: false
      default: ""
`, name)

		writeFile := func(rel, content string) {
			p := filepath.Join(dir, rel)
			if err := os.WriteFile(p, []byte(content), 0644); err != nil {
				log.Fatalf("Failed to write %s: %v", p, err)
			}
		}
		writeFile("module.yaml", manifest)
		writeFile("create.sh", "#!/bin/sh\nset -e\necho 'HYVE_CLUSTER_STATUS=ACTIVE'\n")
		writeFile("delete.sh", "#!/bin/sh\nset -e\necho 'HYVE_CLUSTER_STATUS=NOT_FOUND'\n")
		writeFile("status.sh", "#!/bin/sh\nset -e\necho 'HYVE_CLUSTER_STATUS=NOT_FOUND'\n")
		writeFile("auth.sh", "#!/bin/sh\nset -e\necho 'auth: no-op'\n")
		writeFile("scale.sh", "#!/bin/sh\nset -e\necho 'scale: no-op'\n")

		// Make scripts executable
		for _, s := range []string{"create.sh", "delete.sh", "status.sh", "auth.sh", "scale.sh"} {
			os.Chmod(filepath.Join(dir, s), 0755)
		}

		fmt.Printf("✅ Scaffolded module at %s\n", dir)
		fmt.Printf("\nAdd to your repo's hyve.lock with:\n  hyve module add ./%s 0.1.0\n", dir)
	},
}
