package workflow

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"hyve/cmd/shared"
)

// RunInteractive runs the interactive workflow menu.
func RunInteractive() error {
	return runInteractiveWorkflow()
}

func runInteractiveWorkflow() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Workflow — what would you like to do?").
					Options(
						huh.NewOption("Create a workflow", "create"),
						huh.NewOption("Run a workflow", "run"),
						huh.NewOption("List workflows", "list"),
						huh.NewOption("Show workflow details", "show"),
						huh.NewOption("Validate a workflow", "validate"),
						huh.NewOption("Delete a workflow", "delete"),
						huh.NewOption("← Back", "back"),
					).
					Value(&action),
			),
		).Run()
		if err != nil {
			return err
		}

		switch action {
		case "back":
			return shared.ErrBack
		case "list":
			listWorkflows()
		case "create":
			if err := interactiveWorkflowCreate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "run":
			if err := interactiveWorkflowRun(); err != nil && err != shared.ErrBack {
				return err
			}
		case "show":
			if err := interactiveWorkflowShow(); err != nil && err != shared.ErrBack {
				return err
			}
		case "validate":
			if err := interactiveWorkflowValidate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "delete":
			if err := interactiveWorkflowDelete(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveWorkflowCreate() error {
	var mode string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Create from").
				Options(
					huh.NewOption("Default template", "template"),
					huh.NewOption("Existing YAML file", "file"),
					huh.NewOption("← Back", "back"),
				).
				Value(&mode),
		),
	).Run()
	if err != nil {
		return err
	}
	if mode == "back" {
		return shared.ErrBack
	}

	if mode == "file" {
		var fromFile string
		err = shared.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("Path to YAML file").Placeholder("./workflow.yaml").Validate(shared.RequireNotEmpty).Value(&fromFile),
			),
		).Run()
		if err != nil {
			return err
		}
		createWorkflowFromFile(fromFile)
		return nil
	}

	var name, description string
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Workflow name").Placeholder("deploy-app").Validate(shared.RequireNotEmpty).Value(&name),
			huh.NewInput().Title("Description (optional)").Value(&description),
		),
	).Run()
	if err != nil {
		return err
	}
	createWorkflowTemplate(name, description)
	return nil
}

func interactiveWorkflowRun() error {
	name := ""
	if err := shared.SelectFromList("Workflow to run", shared.FetchWorkflowNames(), &name); err != nil {
		return err
	}

	var cluster string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Cluster (leave blank to run locally)").
				Value(&cluster),
		),
	).Run()
	if err != nil {
		return err
	}

	showLogs := true
	var showOutput bool
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Show execution logs?").
				Affirmative("Yes").
				Negative("No").
				Value(&showLogs),
			huh.NewConfirm().
				Title("Show step outputs?").
				Affirmative("Yes").
				Negative("No").
				Value(&showOutput),
		),
	).Run()
	if err != nil {
		return err
	}

	runWorkflow(name, cluster, showLogs, showOutput)
	return nil
}

func interactiveWorkflowShow() error {
	name := ""
	if err := shared.SelectFromList("Workflow to show", shared.FetchWorkflowNames(), &name); err != nil {
		return err
	}
	showWorkflow(name)
	return nil
}

func interactiveWorkflowValidate() error {
	name := ""
	if err := shared.SelectFromList("Workflow to validate", shared.FetchWorkflowNames(), &name); err != nil {
		return err
	}
	validateWorkflow(name)
	return nil
}

func interactiveWorkflowDelete() error {
	name := ""
	if err := shared.SelectFromList("Workflow to delete", shared.FetchWorkflowNames(), &name); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete workflow '%s'?", name)).
				Affirmative("Yes, delete").
				Negative("Cancel").
				Value(&confirm),
		),
	).Run()
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	deleteWorkflow(name, true)
	return nil
}
