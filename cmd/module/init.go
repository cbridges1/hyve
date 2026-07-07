package module

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	mod "github.com/cbridges1/hyve/internal/module"
)

var initAuthOnlyFlag bool

var initCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Scaffold a new module skeleton in ./modules/<name>/",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		dir, err := mod.InitModuleSkeleton(".", name, initAuthOnlyFlag)
		if err != nil {
			log.Fatalf("%v", err)
		}

		fmt.Printf("✅ Scaffolded module at %s\n", dir)
		fmt.Printf("\nAdd to your repo's hyve.lock with:\n  hyve module add ./%s 0.1.0\n", dir)
	},
}

func init() {
	initCmd.Flags().BoolVar(&initAuthOnlyFlag, "auth-only", false,
		"scaffold an auth-only module (module.yaml + auth.sh only — for non-provisionable clusters like k3d)")
}
