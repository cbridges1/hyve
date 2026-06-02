package kubeconfig

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
)

// RunInteractive runs the interactive kubeconfig menu.
func RunInteractive() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Kubeconfig — what would you like to do?").
					Options(
						huh.NewOption("Remove a cluster context", "remove"),
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
		case "remove":
			if err := interactiveKubeconfigRemove(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveKubeconfigRemove() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to remove context for", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Remove context '%s' from ~/.kube/config?", clusterName)).
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

	removeFromKubeConfig(clusterName)
	return nil
}
