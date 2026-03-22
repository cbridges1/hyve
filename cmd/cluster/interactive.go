package cluster

import (
	gocontext "context"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"hyve/cmd/shared"
	"hyve/internal/types"
)

// RunInteractive runs the interactive cluster menu.
func RunInteractive() error {
	return runInteractiveCluster()
}

func runInteractiveCluster() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Cluster — what would you like to do?").
					Options(
						huh.NewOption("Add a new cluster", "add"),
						huh.NewOption("Import an existing cluster", "import"),
						huh.NewOption("Modify an existing cluster", "modify"),
						huh.NewOption("Release a cluster from management", "release"),
						huh.NewOption("Delete a cluster", "delete"),
						huh.NewOption("Force-delete a cluster from cloud", "force-delete"),
						huh.NewOption("List clusters", "list"),
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
		case "add":
			if err := interactiveClusterAdd(); err != nil && err != shared.ErrBack {
				return err
			}
		case "import":
			if err := interactiveClusterImport(); err != nil && err != shared.ErrBack {
				return err
			}
		case "release":
			if err := interactiveClusterRelease(); err != nil && err != shared.ErrBack {
				return err
			}
		case "modify":
			if err := interactiveClusterModify(); err != nil && err != shared.ErrBack {
				return err
			}
		case "delete":
			if err := interactiveClusterDelete(); err != nil && err != shared.ErrBack {
				return err
			}
		case "force-delete":
			if err := interactiveClusterForceDelete(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveClusterAdd() error {
	var (
		clusterName      string
		providerName     string
		region           string
		nodesStr         string
		clusterType      string
		accountName      string
		projectName      string
		subscriptionName string
		orgName          string
		vpcName          string
		eksRoleName      string
		nodeRoleName     string
		onCreatedNames   []string
		onDestroyNames   []string
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Cluster name").
				Placeholder("my-cluster").
				Validate(shared.RequireNotEmpty).
				Value(&clusterName),
			huh.NewSelect[string]().
				Title("Cloud provider").
				Options(
					huh.NewOption("Civo", "civo"),
					huh.NewOption("AWS (EKS)", "aws"),
					huh.NewOption("GCP (GKE)", "gcp"),
					huh.NewOption("Azure (AKS)", "azure"),
					huh.NewOption("← Back", "back"),
				).
				Value(&providerName),
		),
	).Run()
	if err != nil {
		return err
	}
	if providerName == "back" {
		return shared.ErrBack
	}

	ctx := gocontext.Background()
	if err := shared.SelectFromGroups("Region", shared.FetchRegionGroups(ctx, providerName, ""), defaultRegionPlaceholder(providerName), &region); err != nil {
		return err
	}
	if err := shared.SelectFromGroups("Node size", shared.FetchNodeGroups(ctx, providerName, region, ""), defaultNodePlaceholder(providerName), &nodesStr); err != nil {
		return err
	}

	if providerName == "civo" {
		err = shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Cluster type").
					Options(
						huh.NewOption("k3s (default)", ""),
						huh.NewOption("talos", "talos"),
					).
					Value(&clusterType),
			),
		).Run()
		if err != nil {
			return err
		}
	}

	switch providerName {
	case "civo":
		if err := shared.SelectFromList("Civo organization", shared.FetchCivoOrgNames(), &orgName); err != nil {
			return err
		}
	case "aws":
		if err := shared.SelectFromList("AWS account alias", shared.FetchAWSAccountNames(), &accountName); err != nil {
			return err
		}
		if err := shared.SelectFromList("VPC alias", shared.FetchAWSVPCNames(accountName), &vpcName); err != nil {
			return err
		}
		if err := shared.SelectFromList("EKS role alias", shared.FetchAWSEKSRoleNames(accountName), &eksRoleName); err != nil {
			return err
		}
		if err := shared.SelectFromList("Node role alias", shared.FetchAWSNodeRoleNames(accountName), &nodeRoleName); err != nil {
			return err
		}
	case "gcp":
		if err := shared.SelectFromList("GCP project alias", shared.FetchGCPProjectNames(), &projectName); err != nil {
			return err
		}
	case "azure":
		if err := shared.SelectFromList("Azure subscription alias", shared.FetchAzureSubscriptionNames(), &subscriptionName); err != nil {
			return err
		}
	}

	// Workflow attachment — optional
	if wfNames := shared.FetchWorkflowNames(); len(wfNames) > 0 {
		makeOpts := func() []huh.Option[string] {
			opts := make([]huh.Option[string], len(wfNames))
			for i, wf := range wfNames {
				opts[i] = huh.NewOption(wf, wf)
			}
			return opts
		}
		if err := shared.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("On-created workflows (optional — space to select, enter to confirm)").
					Options(makeOpts()...).
					Value(&onCreatedNames),
				huh.NewMultiSelect[string]().
					Title("On-destroy workflows (optional — space to select, enter to confirm)").
					Options(makeOpts()...).
					Value(&onDestroyNames),
			),
		).Run(); err != nil {
			return err
		}
	}

	nodes := splitAndTrim(nodesStr, ",")
	var confirm bool
	summary := fmt.Sprintf("Add cluster '%s' on %s in %s with nodes: %s", clusterName, providerName, region, strings.Join(nodes, ", "))
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(summary).
				Affirmative("Create").
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

	addClusterFromCLI(clusterName, region, providerName, nodes, []types.NodeGroup{}, clusterType, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName, onCreatedNames, onDestroyNames)
	return nil
}

func interactiveClusterModify() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to modify", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}

	var region, nodesStr, providerForModify string
	sm, _ := shared.CreateStateManager(gocontext.Background())
	if sm != nil {
		defs, _ := sm.LoadClusterDefinitions()
		for _, d := range defs {
			if d.Metadata.Name == clusterName {
				providerForModify = d.Spec.Provider
				break
			}
		}
	}

	ctx2 := gocontext.Background()
	if err := shared.SelectFromGroupsOptional("New region", shared.FetchRegionGroups(ctx2, providerForModify, ""), &region); err != nil {
		return err
	}
	if err := shared.SelectFromGroupsOptional("New node size", shared.FetchNodeGroups(ctx2, providerForModify, region, ""), &nodesStr); err != nil {
		return err
	}

	modifyCmd.Flags().Set("region", region)
	if nodesStr != "" {
		for _, n := range splitAndTrim(nodesStr, ",") {
			modifyCmd.Flags().Set("nodes", n)
		}
	}
	modifyClusterFromCLI(modifyCmd, clusterName)
	return nil
}

func interactiveClusterDelete() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to delete", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}

	var forceCloud bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Delete from cloud immediately?").
				Description("No = remove from state only (GitOps reconcile handles cloud deletion)").
				Affirmative("Yes — delete from cloud now").
				Negative("No — GitOps").
				Value(&forceCloud),
		),
	).Run()
	if err != nil {
		return err
	}

	action := "remove from Git state (GitOps)"
	if forceCloud {
		action = "DELETE from cloud immediately"
	}
	var confirm bool
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Confirm: %s cluster '%s'?", action, clusterName)).
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

	deleteClusterFromCLI(clusterName, false, forceCloud)
	return nil
}

func interactiveClusterForceDelete() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to force-delete", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}

	var providerName string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Cloud provider").
				Options(
					huh.NewOption("Civo", "civo"),
					huh.NewOption("AWS (EKS)", "aws"),
					huh.NewOption("GCP (GKE)", "gcp"),
					huh.NewOption("Azure (AKS)", "azure"),
				).
				Value(&providerName),
		),
	).Run()
	if err != nil {
		return err
	}

	var accountAlias, projectName string
	switch providerName {
	case "civo":
		if err := shared.SelectFromList("Civo organization", shared.FetchCivoOrgNames(), &accountAlias); err != nil {
			return err
		}
		projectName = accountAlias
	case "aws":
		if err := shared.SelectFromList("AWS account alias", shared.FetchAWSAccountNames(), &accountAlias); err != nil {
			return err
		}
	case "gcp":
		if err := shared.SelectFromList("GCP project alias", shared.FetchGCPProjectNames(), &projectName); err != nil {
			return err
		}
		accountAlias = projectName
	case "azure":
		if err := shared.SelectFromList("Azure subscription alias", shared.FetchAzureSubscriptionNames(), &accountAlias); err != nil {
			return err
		}
	}

	ctxFD := gocontext.Background()
	var region string
	if err := shared.SelectFromGroups("Region", shared.FetchRegionGroups(ctxFD, providerName, accountAlias), defaultRegionPlaceholder(providerName), &region); err != nil {
		return err
	}

	var confirm bool
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Force-delete '%s' from %s/%s? This cannot be undone.", clusterName, providerName, region)).
				Affirmative("Yes, force-delete").
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

	forceDeleteClusterFromCloud(clusterName, region, providerName, projectName, accountAlias)
	return nil
}

func defaultRegionPlaceholder(provider string) string {
	switch provider {
	case "aws":
		return "us-east-1"
	case "gcp":
		return "us-central1"
	case "azure":
		return "eastus"
	case "civo":
		return "PHX1"
	default:
		return "region"
	}
}

func defaultNodePlaceholder(provider string) string {
	switch provider {
	case "aws":
		return "t3.medium"
	case "gcp":
		return "e2-medium"
	case "azure":
		return "Standard_B2s"
	case "civo":
		return "g4s.kube.medium"
	default:
		return "machine"
	}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func interactiveClusterImport() error {
	if sm, _ := shared.CreateStateManager(gocontext.Background()); sm != nil {
		if repoCfg, err := sm.LoadRepoConfig(); err == nil && repoCfg.Reconcile.StrictDelete {
			fmt.Println("❌ Import is disabled: this repository has strictDelete enabled.")
			fmt.Println("   In strict-delete mode hyve owns the full desired-state; importing an unmanaged cluster would cause it to be deleted on the next reconciliation.")
			return nil
		}
	}

	var (
		providerName string
		accountAlias string
		region       string
		clusterName  string
		vpcName      string
		eksRoleName  string
		nodeRoleName string
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Cloud provider").
				Options(
					huh.NewOption("Civo", "civo"),
					huh.NewOption("AWS (EKS)", "aws"),
					huh.NewOption("GCP (GKE)", "gcp"),
					huh.NewOption("Azure (AKS)", "azure"),
					huh.NewOption("← Back", "back"),
				).
				Value(&providerName),
		),
	).Run()
	if err != nil {
		return err
	}
	if providerName == "back" {
		return shared.ErrBack
	}

	switch providerName {
	case "civo":
		if err := shared.SelectFromList("Civo organization", shared.FetchCivoOrgNames(), &accountAlias); err != nil {
			return err
		}
	case "aws":
		if err := shared.SelectFromList("AWS account alias", shared.FetchAWSAccountNames(), &accountAlias); err != nil {
			return err
		}
		if err := shared.SelectFromList("VPC alias", shared.FetchAWSVPCNames(accountAlias), &vpcName); err != nil {
			return err
		}
		if err := shared.SelectFromList("EKS role alias", shared.FetchAWSEKSRoleNames(accountAlias), &eksRoleName); err != nil {
			return err
		}
		if err := shared.SelectFromList("Node role alias", shared.FetchAWSNodeRoleNames(accountAlias), &nodeRoleName); err != nil {
			return err
		}
	case "gcp":
		if err := shared.SelectFromList("GCP project alias", shared.FetchGCPProjectNames(), &accountAlias); err != nil {
			return err
		}
	case "azure":
		if err := shared.SelectFromList("Azure subscription alias", shared.FetchAzureSubscriptionNames(), &accountAlias); err != nil {
			return err
		}
	}

	ctx := gocontext.Background()
	if err := shared.SelectFromGroups("Region", shared.FetchRegionGroups(ctx, providerName, accountAlias), defaultRegionPlaceholder(providerName), &region); err != nil {
		return err
	}

	cloudNames := shared.FetchCloudClusterNames(ctx, providerName, region, accountAlias)
	const manualKey = "__manual__"
	if len(cloudNames) > 0 {
		opts := make([]huh.Option[string], 0, len(cloudNames)+2)
		opts = append(opts, huh.NewOption("Enter manually...", manualKey))
		for _, n := range cloudNames {
			opts = append(opts, huh.NewOption(n, n))
		}
		opts = append(opts, huh.NewOption("← Back", "__back__"))

		selection := ""
		if err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select cluster to import").
					Options(opts...).
					Value(&selection),
			),
		).Run(); err != nil {
			return err
		}
		switch selection {
		case "__back__":
			return shared.ErrBack
		case manualKey:
			// fall through to manual input below
		default:
			clusterName = selection
		}
	}

	if clusterName == "" {
		if err := shared.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Cluster name (must match the name in your cloud provider)").
					Placeholder("my-cluster").
					Validate(shared.RequireNotEmpty).
					Value(&clusterName),
			),
		).Run(); err != nil {
			return err
		}
	}

	var orgName, projectName, subscriptionName, accountName string
	switch providerName {
	case "civo":
		orgName = accountAlias
	case "aws":
		accountName = accountAlias
	case "gcp":
		projectName = accountAlias
	case "azure":
		subscriptionName = accountAlias
	}

	var confirm bool
	summary := fmt.Sprintf("Import '%s' (%s, %s) into hyve — cloud cluster will NOT be reprovisioned", clusterName, providerName, region)
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(summary).
				Affirmative("Import").
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

	importClusterFromCLI(clusterName, region, providerName, nil, []types.NodeGroup{}, accountName, projectName, subscriptionName, orgName, vpcName, eksRoleName, nodeRoleName)
	return nil
}

func interactiveClusterRelease() error {
	if sm, _ := shared.CreateStateManager(gocontext.Background()); sm != nil {
		if repoCfg, err := sm.LoadRepoConfig(); err == nil && repoCfg.Reconcile.StrictDelete {
			fmt.Println("❌ Release is disabled: this repository has strictDelete enabled.")
			fmt.Println("   In strict-delete mode removing a cluster definition would cause the cloud cluster to be deleted on the next reconciliation.")
			fmt.Println("   Use 'hyve cluster delete' instead.")
			return nil
		}
	}

	clusterName := ""
	if err := shared.SelectFromList("Cluster to release from management", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Release '%s' from hyve management? The cloud cluster will NOT be deleted.", clusterName)).
				Affirmative("Yes, release").
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

	releaseClusterFromCLI(clusterName)
	return nil
}
