package cmd

import (
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/cluster"
	gitpkg "github.com/cbridges1/hyve/cmd/git"
	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/cmd/template"
	"github.com/cbridges1/hyve/cmd/workflow"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive TUI",
	Long:  "Navigate and run any Hyve command through a guided terminal user interface.",
	RunE: func(cmd *cobra.Command, args []string) error {
		for {
			var section string
			err := shared.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Hyve — what would you like to do?").
						Options(
							huh.NewOption("cluster  — list, inspect, delete, auth/deauth", "cluster"),
							huh.NewOption("template — create, execute, manage templates", "template"),
							huh.NewOption("git      — manage Git repositories", "git"),
							huh.NewOption("workflow — automated pipelines", "workflow"),
							huh.NewOption("Quit", "quit"),
						).
						Value(&section),
				),
			).Run()
			if err == huh.ErrUserAborted {
				return nil
			}
			if err != nil {
				return err
			}

			if section == "quit" {
				return nil
			}

			var runErr error
			switch section {
			case "cluster":
				runErr = cluster.RunInteractive()
			case "template":
				runErr = template.RunInteractive()
			case "git":
				runErr = gitpkg.RunInteractive()
			case "workflow":
				runErr = workflow.RunInteractive()
			}
			// ErrBack from a top-level section just returns to this menu
			if runErr == huh.ErrUserAborted {
				return nil
			}
			if runErr != nil && runErr != shared.ErrBack {
				return runErr
			}
		}
	},
}
