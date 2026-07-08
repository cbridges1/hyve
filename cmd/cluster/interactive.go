package cluster

import (
	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/template"
)

// RunInteractive runs the interactive cluster menu.
func RunInteractive() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Cluster — what would you like to do?").
					Options(
						huh.NewOption("Create a cluster", "create"),
						huh.NewOption("List clusters", "list"),
						huh.NewOption("Show cluster details", "show"),
						huh.NewOption("Configure kubeconfig (auth)", "auth"),
						huh.NewOption("Remove kubeconfig context (deauth)", "deauth"),
						huh.NewOption("Delete a cluster", "delete"),
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
		case "create":
			if err := interactiveClusterCreate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "list":
			listClusters()
		case "show":
			if err := interactiveClusterShow(); err != nil && err != shared.ErrBack {
				return err
			}
		case "auth":
			if err := interactiveClusterAuth(); err != nil && err != shared.ErrBack {
				return err
			}
		case "deauth":
			if err := interactiveClusterDeauth(); err != nil && err != shared.ErrBack {
				return err
			}
		case "delete":
			if err := interactiveClusterDelete(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveClusterCreate() error {
	templateName := ""
	if err := shared.SelectFromList("Template to use", shared.FetchTemplateNames(), &templateName); err != nil {
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
		createClusterFromTemplate(templateName, clusterName, region, overrides)
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
		manifest := shared.LoadManifest(driverSource, driverVersion)
		// Pre-populate with template defaults so users see current values.
		var existing map[string]string
		if tmpl != nil {
			existing = tmpl.Spec.Params
		}
		overrides, err = shared.CollectParamValues(manifest, existing, "Param overrides")
		if err != nil {
			return err
		}
	}

	createClusterFromTemplate(templateName, clusterName, region, overrides)
	return nil
}

func interactiveClusterShow() error {
	name := ""
	if err := shared.SelectFromList("Cluster to show", shared.FetchClusterNames(), &name); err != nil {
		return err
	}
	showCluster(name)
	return nil
}

func interactiveClusterAuth() error {
	name := ""
	if err := shared.SelectFromList("Cluster to configure", shared.FetchClusterNames(), &name); err != nil {
		return err
	}
	runClusterAuth(name, "")
	return nil
}

func interactiveClusterDeauth() error {
	name := ""
	if err := shared.SelectFromList("Cluster to remove context for", shared.FetchClusterNames(), &name); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Remove context '" + name + "' from ~/.kube/config?").
				Affirmative("Yes, remove").
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

	removeFromKubeConfig(name)
	return nil
}

func interactiveClusterDelete() error {
	name := ""
	if err := shared.SelectFromList("Cluster to delete", shared.FetchClusterNames(), &name); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Mark '" + name + "' for deletion?").
				Description("The cluster will be deleted on the next reconcile.").
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

	markClusterForDeletion(name)
	return nil
}
