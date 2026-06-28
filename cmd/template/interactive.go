package template

import (
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/template"
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

	// Load template early — needed for LockParams check and driver info.
	tmpl, _ := template.NewManager(shared.GetRepoPath()).GetTemplate(templateName)

	var clusterName, region string
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
		),
	).Run()
	if err != nil {
		return err
	}

	overrides := map[string]string{}

	// Skip param overrides entirely when the template admin has locked them.
	if tmpl != nil && tmpl.Spec.LockParams {
		executeTemplate(templateName, clusterName, region, overrides)
		return nil
	}

	// Ask whether the user wants to override any default params.
	var wantOverrides bool
	if err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Override default params?").
				Description("The template provides defaults for all params. Select Yes to customise them.").
				Affirmative("Yes — customise params").
				Negative("No — use defaults").
				Value(&wantOverrides),
		),
	).Run(); err != nil {
		return err
	}

	if wantOverrides {
		var driverSource, driverVersion string
		if tmpl != nil {
			driverSource = tmpl.Spec.Driver.Source
			driverVersion = tmpl.Spec.Driver.Version
		}
		manifest := loadManifest(driverSource, driverVersion)
		// Pre-populate with template defaults so users see current values.
		var existing map[string]string
		if tmpl != nil {
			existing = tmpl.Spec.Params
		}
		overrides, err = collectParamValues(manifest, existing, "Param overrides")
		if err != nil {
			return err
		}
	}

	executeTemplate(templateName, clusterName, region, overrides)
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

	manifest := loadManifest(driverSource, driverVersion)
	params, err := collectParamValues(manifest, nil, "Default params (optional)")
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
				Description("When locked, users cannot override default param values at execute time.").
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

// loadManifest loads the module.yaml for the given driver source/version.
// Returns nil if the manifest is not available locally.
func loadManifest(source, version string) *module.ModuleManifest {
	repoRoot := shared.GetRepoPath()
	lf, _ := module.LoadLockFile(repoRoot)
	m, _ := module.LoadManifestForSource(source, version, repoRoot, lf)
	return m
}

// collectParamValues runs a per-param form when a manifest is available.
// Choice params use a dropdown; free-text params use an input field.
// Falls back to a raw KEY=VALUE input when manifest is nil or has no params.
// existing is used to pre-populate values (e.g. when editing overrides).
func collectParamValues(manifest *module.ModuleManifest, existing map[string]string, fallbackTitle string) (map[string]string, error) {
	if manifest == nil || len(manifest.Spec.Params) == 0 {
		var paramsRaw string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(fallbackTitle).
					Description("KEY=VALUE pairs, comma-separated. e.g. node_size=g4s.kube.medium,node_count=3").
					Value(&paramsRaw),
			),
		).Run()
		if err != nil {
			return nil, err
		}
		return parseParamOverrides(paramsRaw), nil
	}

	params := manifest.Spec.Params
	values := make([]string, len(params))
	for i, p := range params {
		values[i] = p.Default
		if existing != nil {
			if v, ok := existing[p.Name]; ok {
				values[i] = v
			}
		}
	}

	fields := make([]huh.Field, 0, len(params))
	for i, p := range params {
		title := p.Name
		if p.Description != "" {
			title = p.Name + " — " + p.Description
		}
		if len(p.Choices) > 0 {
			opts := make([]huh.Option[string], len(p.Choices))
			for j, c := range p.Choices {
				opts[j] = huh.NewOption(c, c)
			}
			fields = append(fields, huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(&values[i]))
		} else {
			fields = append(fields, huh.NewInput().
				Title(title).
				Value(&values[i]))
		}
	}

	if err := shared.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
		return nil, err
	}

	result := make(map[string]string, len(params))
	for i, p := range params {
		if values[i] != "" {
			result[p.Name] = values[i]
		}
	}
	return result, nil
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
