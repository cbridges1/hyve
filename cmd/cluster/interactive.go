package cluster

import (
	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
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
