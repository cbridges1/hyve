package template

import (
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
)

// RunInteractive runs the interactive template menu.
func RunInteractive() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Template — what would you like to do?").
					Options(
						huh.NewOption("List templates", "list"),
						huh.NewOption("Show template details", "show"),
						huh.NewOption("Create a template", "create"),
						huh.NewOption("Validate a template", "validate"),
						huh.NewOption("Delete a template", "delete"),
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
			listTemplates()
		case "show":
			if err := interactiveTemplateShow(); err != nil && err != shared.ErrBack {
				return err
			}
		case "create":
			if err := interactiveTemplateCreate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "validate":
			if err := interactiveTemplateValidate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "delete":
			if err := interactiveTemplateDelete(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveTemplateShow() error {
	name := ""
	if err := shared.SelectFromList("Template to show", shared.FetchTemplateNames(), &name); err != nil {
		return err
	}
	showTemplate(name)
	return nil
}

func interactiveTemplateValidate() error {
	name := ""
	if err := shared.SelectFromList("Template to validate", shared.FetchTemplateNames(), &name); err != nil {
		return err
	}
	validateTemplate(name)
	return nil
}

func interactiveTemplateCreate() error {
	var name, description, driverSource, driverVersion, region string

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Template name").
				Placeholder("my-template").
				Validate(shared.RequireNotEmpty).
				Value(&name),
			huh.NewInput().
				Title("Description (optional)").
				Value(&description),
		),
	).Run()
	if err != nil {
		return err
	}

	err = shared.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Driver source").
				Placeholder("./custom-modules/civo  or  github.com/hyve-modules/eks").
				Validate(shared.RequireNotEmpty).
				Value(&driverSource),
			huh.NewInput().
				Title("Driver version").
				Placeholder("latest").
				Value(&driverVersion),
			huh.NewInput().
				Title("Default region (optional)").
				Value(&region),
		),
	).Run()
	if err != nil {
		return err
	}

	if driverVersion == "" {
		driverVersion = "latest"
	}

	manifest := shared.LoadManifest(driverSource, driverVersion)
	params, err := shared.CollectParamValues(manifest, nil, "Default params (optional)")
	if err != nil {
		return err
	}

	var schedule string
	var setSchedule bool
	if err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Set an expiry schedule for this template?").
				Description("Clusters created from this template will be automatically deleted on the given schedule.").
				Affirmative("Yes — set schedule").
				Negative("No — no expiry").
				Value(&setSchedule),
		),
	).Run(); err != nil {
		return err
	}
	if setSchedule {
		var schedErr error
		schedule, schedErr = shared.PromptSchedule("")
		if schedErr != nil {
			return schedErr
		}
	}

	var lockParams bool
	if err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Lock params?").
				Description("When locked, users cannot override default param values when creating a cluster from this template.").
				Affirmative("Yes — lock params").
				Negative("No — allow overrides").
				Value(&lockParams),
		),
	).Run(); err != nil {
		return err
	}

	var beforeCreate, onCreate, onDelete, afterDelete []string
	if err := interactiveSelectWorkflows(&beforeCreate, &onCreate, &onDelete, &afterDelete); err != nil {
		return err
	}

	createTemplate(name, description, driverSource, driverVersion, region, params,
		strings.Join(beforeCreate, ","), strings.Join(onCreate, ","),
		strings.Join(onDelete, ","), strings.Join(afterDelete, ","), schedule, lockParams)
	return nil
}

// interactiveSelectWorkflows shows a multi-select dropdown for each lifecycle
// hook. When no workflows are defined in the repository the step is skipped.
func interactiveSelectWorkflows(beforeCreate, onCreate, onDelete, afterDelete *[]string) error {
	names := shared.FetchWorkflowNames()
	if len(names) == 0 {
		return nil
	}

	// Each multi-select must have its own option slice; sharing option instances
	// causes huh to mirror selections across all widgets.
	makeOpts := func() []huh.Option[string] {
		opts := make([]huh.Option[string], len(names))
		for i, n := range names {
			opts[i] = huh.NewOption(n, n)
		}
		return opts
	}

	return shared.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("beforeCreate workflows").
				Description("Run before the cluster is provisioned (no kubeconfig available).").
				Options(makeOpts()...).
				Value(beforeCreate),
			huh.NewMultiSelect[string]().
				Title("onCreate workflows").
				Description("Run after the cluster is active.").
				Options(makeOpts()...).
				Value(onCreate),
			huh.NewMultiSelect[string]().
				Title("onDelete workflows").
				Description("Run before the cluster is deleted.").
				Options(makeOpts()...).
				Value(onDelete),
			huh.NewMultiSelect[string]().
				Title("afterDelete workflows").
				Description("Run after the cluster is deleted (no kubeconfig available).").
				Options(makeOpts()...).
				Value(afterDelete),
		),
	).Run()
}

func interactiveTemplateDelete() error {
	name := ""
	if err := shared.SelectFromList("Template to delete", shared.FetchTemplateNames(), &name); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Delete template '" + name + "'?").
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

	deleteTemplate(name)
	return nil
}
