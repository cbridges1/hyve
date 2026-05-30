package workflow

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/workflow"
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
						huh.NewOption("List workflows", "list"),
						huh.NewOption("Create a workflow", "create"),
						huh.NewOption("Run a workflow", "run"),
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

	const manualKey = "__manual__"
	const localKey = "__local__"

	var cluster string
	clusterNames := shared.FetchClusterNames()
	opts := make([]huh.Option[string], 0, len(clusterNames)+3)
	opts = append(opts, huh.NewOption("Enter cluster name manually...", manualKey))
	opts = append(opts, huh.NewOption("Run locally (no cluster)", localKey))
	for _, n := range clusterNames {
		opts = append(opts, huh.NewOption(n, n))
	}

	var selection string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Cluster").
				Options(opts...).
				Value(&selection),
		),
	).Run()
	if err != nil {
		return err
	}

	switch selection {
	case localKey:
		// leave cluster empty
	case manualKey:
		err = shared.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Cluster name").
					Validate(shared.RequireNotEmpty).
					Value(&cluster),
			),
		).Run()
		if err != nil {
			return err
		}
	default:
		cluster = selection
	}

	// Prompt for any required inputs that are not already in the environment
	setVars, err := promptMissingInputs(name)
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

	runWorkflow(name, cluster, showLogs, showOutput, setVars)
	return nil
}

// promptMissingInputs loads the named workflow and prompts for any declared inputs
// that are not already satisfied by the process environment. Returns a map of
// KEY → VALUE that can be passed to executor.InjectVars.
func promptMissingInputs(workflowName string) (map[string]string, error) {
	localPath := getWorkflowLocalPath()
	mgr, err := workflow.NewManager(localPath)
	if err != nil {
		return nil, err
	}

	wf, err := mgr.GetWorkflow(workflowName)
	if err != nil {
		// If we can't load the workflow (shouldn't normally happen at this point),
		// skip the input prompt — the executor will validate later.
		return nil, nil
	}

	if len(wf.Spec.Inputs) == 0 {
		return nil, nil
	}

	collected := make(map[string]string)

	for _, input := range wf.Spec.Inputs {
		// Skip inputs already satisfied by the process environment
		if os.Getenv(input.Name) != "" {
			continue
		}

		label := input.Name
		if input.Description != "" {
			label = input.Description
		}

		var value string
		formErr := shared.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fmt.Sprintf("Input: %s", label)).
					Description(fmt.Sprintf("Required by workflow '%s'", workflowName)).
					Placeholder(input.Default).
					Value(&value),
			),
		).Run()
		if formErr != nil {
			return nil, formErr
		}

		if value == "" && input.Default != "" {
			value = input.Default
		}
		if value != "" {
			collected[input.Name] = value
		}
	}

	return collected, nil
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
