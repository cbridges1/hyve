package cluster

import (
	gocontext "context"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/types"
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
						huh.NewOption("List clusters", "list"),
						huh.NewOption("Show cluster details", "show"),
						huh.NewOption("Create a new cluster", "create"),
						huh.NewOption("Modify an existing cluster", "modify"),
						huh.NewOption("Delete a cluster", "delete"),
						huh.NewOption("Force-delete a cluster from cloud", "force-delete"),
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
		case "create":
			if err := interactiveClusterCreate(); err != nil && err != shared.ErrBack {
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

func interactiveClusterShow() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to show", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}
	showCluster(clusterName)
	return nil
}

func interactiveClusterCreate() error {
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
		vpcID            string
		eksRoleName      string
		nodeRoleName     string
		resourceGroup    string
		beforeCreate     []string
		onCreatedNames   []string
		onDestroyNames   []string
		afterDelete      []string
		pause            bool
		expiresAt        string
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

	// Account / org / project / subscription first — so region/node API calls can authenticate.
	ctx := gocontext.Background()
	accountAlias := ""
	switch providerName {
	case "civo":
		if err := shared.SelectFromList("Civo organization", shared.FetchCivoOrgNames(), &orgName); err != nil {
			return err
		}
		accountAlias = orgName
	case "aws":
		if err := shared.SelectFromList("AWS account alias", shared.FetchAWSAccountNames(), &accountName); err != nil {
			return err
		}
		accountAlias = accountName
	case "gcp":
		if err := shared.SelectFromList("GCP project alias", shared.FetchGCPProjectNames(), &projectName); err != nil {
			return err
		}
		accountAlias = projectName
	case "azure":
		if err := shared.SelectFromList("Azure subscription alias", shared.FetchAzureSubscriptionNames(), &subscriptionName); err != nil {
			return err
		}
		accountAlias = subscriptionName
	}

	regionGroups := shared.FetchRegionGroups(ctx, providerName, accountAlias)
	if len(regionGroups) == 0 {
		if err := shared.ShowNoCloudDataWarning(providerName); err != nil {
			return err
		}
	}
	if err := shared.SelectFromGroups("Region", regionGroups, defaultRegionPlaceholder(providerName), &region); err != nil {
		return err
	}
	if err := shared.SelectFromGroups("Node size", shared.FetchNodeGroups(ctx, providerName, region, accountAlias), defaultNodePlaceholder(providerName), &nodesStr); err != nil {
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

	// Provider-specific cloud resource selection
	switch providerName {
	case "aws":
		if err := shared.SelectAWSVPC(ctx, accountName, region, &vpcID); err != nil {
			return err
		}
		if err := shared.SelectAWSRole(ctx, accountName, "EKS control plane role (optional)", &eksRoleName); err != nil {
			return err
		}
		if err := shared.SelectAWSRole(ctx, accountName, "EKS node group role (optional)", &nodeRoleName); err != nil {
			return err
		}
	case "azure":
		if err := shared.SelectAzureRG(ctx, subscriptionName, &resourceGroup); err != nil {
			return err
		}
	}

	// Lifecycle hooks — one screen per hook so titles are never clipped
	wfNames := shared.FetchWorkflowNames()
	if err := shared.SelectWorkflowHook("Before-create workflows (optional)", wfNames, &beforeCreate); err != nil {
		return err
	}
	if err := shared.SelectWorkflowHook("On-created workflows (optional)", wfNames, &onCreatedNames); err != nil {
		return err
	}
	if err := shared.SelectWorkflowHook("On-destroy workflows (optional)", wfNames, &onDestroyNames); err != nil {
		return err
	}
	if err := shared.SelectWorkflowHook("After-delete workflows (optional)", wfNames, &afterDelete); err != nil {
		return err
	}

	// Expiry option — opt in explicitly
	var setExpiry bool
	if err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Set an expiry for this cluster?").
				Description("The cluster will be automatically deleted when the expiry time is reached.").
				Affirmative("Yes — set expiry").
				Negative("No — run indefinitely").
				Value(&setExpiry),
		),
	).Run(); err != nil {
		return err
	}
	if setExpiry {
		var expiryErr error
		expiresAt, expiryErr = shared.PromptExpiresAt("")
		if expiryErr != nil {
			return expiryErr
		}
	}

	// Pause option — asked last
	if err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Pause reconciliation on create?").
				Description("The cluster definition will be saved but the reconciler will skip it until unpaused.").
				Affirmative("Yes — pause").
				Negative("No — reconcile normally").
				Value(&pause),
		),
	).Run(); err != nil {
		return err
	}

	nodes := splitAndTrim(nodesStr, ",")
	var confirm bool
	summary := fmt.Sprintf("Create cluster '%s' on %s in %s with nodes: %s", clusterName, providerName, region, strings.Join(nodes, ", "))
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

	createClusterFromCLI(clusterName, region, providerName, nodes, []types.NodeGroup{}, clusterType, accountName, projectName, subscriptionName, orgName, vpcID, eksRoleName, nodeRoleName, resourceGroup, beforeCreate, onCreatedNames, onDestroyNames, afterDelete, pause, expiresAt)
	return nil
}

func interactiveClusterModify() error {
	clusterName := ""
	if err := shared.SelectFromList("Cluster to modify", shared.FetchClusterNames(), &clusterName); err != nil {
		return err
	}

	var region, nodesStr, providerForModify string
	var currentPause bool
	var currentExpiresAt string
	sm, _ := shared.CreateStateManager(gocontext.Background())
	if sm != nil {
		defs, _ := sm.LoadClusterDefinitions()
		for _, d := range defs {
			if d.Metadata.Name == clusterName {
				providerForModify = d.Spec.Provider
				currentPause = d.Spec.Pause
				currentExpiresAt = d.Spec.ExpiresAt
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

	// Pause option
	pauseAction := "keep"
	pauseOpts := []huh.Option[string]{
		huh.NewOption("Keep current ("+pauseStatus(currentPause)+")", "keep"),
	}
	if currentPause {
		pauseOpts = append(pauseOpts, huh.NewOption("Unpause — resume reconciliation", "unpause"))
	} else {
		pauseOpts = append(pauseOpts, huh.NewOption("Pause — skip reconciliation", "pause"))
	}
	if err := shared.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Reconciliation pause").
				Options(pauseOpts...).
				Value(&pauseAction),
		),
	).Run(); err != nil {
		return err
	}

	// Expiry option
	expiresAtInput, expiryErr := shared.PromptExpiresAt(currentExpiresAt)
	if expiryErr != nil {
		return expiryErr
	}

	modifyCmd.Flags().Set("region", region)
	if nodesStr != "" {
		for _, n := range splitAndTrim(nodesStr, ",") {
			modifyCmd.Flags().Set("nodes", n)
		}
	}
	switch pauseAction {
	case "pause":
		modifyCmd.Flags().Set("pause", "true")
	case "unpause":
		modifyCmd.Flags().Set("unpause", "true")
	}
	if expiresAtInput != currentExpiresAt {
		modifyCmd.Flags().Set("expires-at", expiresAtInput)
	}
	modifyClusterFromCLI(modifyCmd, clusterName)
	return nil
}

func pauseStatus(paused bool) string {
	if paused {
		return "paused"
	}
	return "active"
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
