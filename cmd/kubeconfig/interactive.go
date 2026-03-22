package kubeconfig

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"hyve/cmd/shared"
)

// RunInteractive runs the interactive kubeconfig menu.
func RunInteractive() error {
	return runInteractiveKubeconfig()
}

func runInteractiveKubeconfig() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Kubeconfig — what would you like to do?").
					Options(
						huh.NewOption("Sync kubeconfigs from all clusters", "sync"),
						huh.NewOption("Get kubeconfig for a cluster", "get"),
						huh.NewOption("Use (merge + set active context)", "use"),
						huh.NewOption("Merge into ~/.kube/config", "merge"),
						huh.NewOption("Remove a kubeconfig", "remove"),
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
		case "sync":
			syncKubeconfigs()
		case "get":
			if err := interactiveKubeconfigGet(); err != nil && err != shared.ErrBack {
				return err
			}
		case "use":
			if err := interactiveKubeconfigUse(); err != nil && err != shared.ErrBack {
				return err
			}
		case "merge":
			if err := interactiveKubeconfigMerge(); err != nil && err != shared.ErrBack {
				return err
			}
		case "remove":
			if err := interactiveKubeconfigRemove(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveKubeconfigGet() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster", shared.FetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}
	getKubeconfig(kubeconfigGetCmd, clusterName)
	return nil
}

func interactiveKubeconfigUse() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster", shared.FetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}
	UseKubeconfig(clusterName)
	return nil
}

func interactiveKubeconfigMerge() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to merge", shared.FetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}
	mergeKubeconfig(clusterName)
	return nil
}

func interactiveKubeconfigRemove() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to remove kubeconfig for", shared.FetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Remove kubeconfig for '%s'?", clusterName)).
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

	shared.RemoveKubeconfig(clusterName)
	return nil
}
