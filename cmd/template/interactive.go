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
						huh.NewOption("Execute a template", "execute"),
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
		case "execute":
			if err := interactiveTemplateExecute(); err != nil && err != shared.ErrBack {
				return err
			}
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

func interactiveTemplateExecute() error {
	templateName := ""
	if err := shared.SelectFromList("Template to execute", shared.FetchTemplateNames(), &templateName); err != nil {
		return err
	}

	var clusterName, region, paramsRaw string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Cluster name").
				Placeholder("my-cluster").
				Validate(shared.ValidateClusterName).
				Value(&clusterName),
			huh.NewInput().
				Title("Region override (leave blank to use template default)").
				Value(&region),
			huh.NewInput().
				Title("Param overrides (optional)").
				Description("KEY=VALUE pairs, comma-separated. e.g. node_size=g4s.kube.large,node_count=5").
				Value(&paramsRaw),
		),
	).Run()
	if err != nil {
		return err
	}

	overrides := parseParamOverrides(paramsRaw)
	executeTemplate(templateName, clusterName, region, overrides)
	return nil
}

func interactiveTemplateCreate() error {
	var name, description, driverSource, driverVersion, region, paramsRaw string
	var beforeCreate, onCreate, onDelete, afterDelete string

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
			huh.NewInput().
				Title("Default params (optional)").
				Description("KEY=VALUE pairs, comma-separated. e.g. node_size=g4s.kube.medium,node_count=3").
				Value(&paramsRaw),
		),
	).Run()
	if err != nil {
		return err
	}

	err = shared.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("beforeCreate workflows (optional, comma-separated)").
				Value(&beforeCreate),
			huh.NewInput().
				Title("onCreate workflows (optional, comma-separated)").
				Value(&onCreate),
			huh.NewInput().
				Title("onDelete workflows (optional, comma-separated)").
				Value(&onDelete),
			huh.NewInput().
				Title("afterDelete workflows (optional, comma-separated)").
				Value(&afterDelete),
		),
	).Run()
	if err != nil {
		return err
	}

	if driverVersion == "" {
		driverVersion = "latest"
	}

	params := parseParamOverrides(paramsRaw)
	createTemplate(name, description, driverSource, driverVersion, region, params,
		beforeCreate, onCreate, onDelete, afterDelete, "")
	return nil
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

// parseParamOverrides splits a "key=value,key2=value2" string into a map.
func parseParamOverrides(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}
