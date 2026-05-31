package kubeconfig

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
)

// RunInteractive runs the interactive kubeconfig menu.
func RunInteractive() error {
	return runInteractiveKubeconfig()
}

func fetchKubeconfigClusterNames() []string {
	mgr, _, err := createKubeconfigManager()
	if err != nil {
		return nil
	}
	defer mgr.Close()
	list, err := mgr.ListKubeconfigs()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, k := range list {
		names = append(names, k.ClusterName)
	}
	return names
}

func runInteractiveKubeconfig() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Kubeconfig — what would you like to do?").
					Options(
						huh.NewOption("Get kubeconfig for a cluster", "get"),
						huh.NewOption("Use (merge + set active context)", "use"),
						huh.NewOption("Merge into ~/.kube/config", "merge"),
						huh.NewOption("Remove a kubeconfig context", "remove"),
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
	if err := shared.SelectFromList("Cluster", fetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}
	getKubeconfig(kubeconfigGetCmd, clusterName)
	return nil
}

func interactiveKubeconfigUse() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster", fetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}
	UseKubeconfig(clusterName)
	return nil
}

func interactiveKubeconfigMerge() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to merge", fetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}
	mergeKubeconfig(clusterName)
	return nil
}

func interactiveKubeconfigRemove() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to remove kubeconfig for", fetchKubeconfigClusterNames(), &clusterName); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Remove kubeconfig context for '%s' from ~/.kube/config?", clusterName)).
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
