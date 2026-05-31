package module

import "github.com/spf13/cobra"

// Cmd is the parent command for module management.
var Cmd = &cobra.Command{
	Use:   "module",
	Short: "Manage Hyve modules (drivers)",
}

func init() {
	Cmd.AddCommand(listCmd, installCmd, addCmd, updateCmd, infoCmd, validateCmd, removeCmd, initCmd)
}
