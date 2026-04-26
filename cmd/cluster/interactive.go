package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/cloudlookup"
	"github.com/cbridges1/hyve/internal/providerconfig"
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
		if err := selectAWSVPC(ctx, accountName, region, &vpcID); err != nil {
			return err
		}
		if err := selectAWSRole(ctx, accountName, "EKS control plane role (optional)", &eksRoleName); err != nil {
			return err
		}
		if err := selectAWSRole(ctx, accountName, "EKS node group role (optional)", &nodeRoleName); err != nil {
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
		if err := selectAzureRG(ctx, subscriptionName, &resourceGroup); err != nil {
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
					Title("Before-create workflows (optional — space to select, enter to confirm)").
					Options(makeOpts()...).
					Value(&beforeCreate),
				huh.NewMultiSelect[string]().
					Title("On-created workflows (optional — space to select, enter to confirm)").
					Options(makeOpts()...).
					Value(&onCreatedNames),
				huh.NewMultiSelect[string]().
					Title("On-destroy workflows (optional — space to select, enter to confirm)").
					Options(makeOpts()...).
					Value(&onDestroyNames),
				huh.NewMultiSelect[string]().
					Title("After-delete workflows (optional — space to select, enter to confirm)").
					Options(makeOpts()...).
					Value(&afterDelete),
			),
		).Run(); err != nil {
			return err
		}
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

// selectAWSVPC presents a three-option picker for an AWS VPC ID.
// Options: fetch from AWS (live DescribeVpcs), enter manually, or skip.
func selectAWSVPC(ctx gocontext.Context, accountName, region string, vpcID *string) error {
	var method string
	if err := shared.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("AWS VPC").
			Options(
				huh.NewOption("Fetch from AWS", "fetch"),
				huh.NewOption("Enter VPC ID manually", "manual"),
				huh.NewOption("Skip (set via HYVE_VPC_ID hook)", "skip"),
			).
			Value(&method),
	)).Run(); err != nil {
		return err
	}
	switch method {
	case "skip":
		return nil
	case "manual":
		return shared.NewForm(huh.NewGroup(
			huh.NewInput().Title("VPC ID (e.g. vpc-0abc123)").Value(vpcID),
		)).Run()
	case "fetch":
		keyID, secret, token, err := providerconfig.NewManager(shared.GetRepoPath()).GetAWSCredentials(accountName)
		if err != nil || keyID == "" {
			log.Printf("Could not fetch AWS credentials for account '%s' — enter manually.", accountName)
			return shared.NewForm(huh.NewGroup(
				huh.NewInput().Title("VPC ID (e.g. vpc-0abc123)").Value(vpcID),
			)).Run()
		}
		vpcs, lookupErr := cloudlookup.ListVPCs(ctx, cloudlookup.AWSCreds{
			AccessKeyID:     keyID,
			SecretAccessKey: secret,
			SessionToken:    token,
		}, region)
		if lookupErr != nil || len(vpcs) == 0 {
			log.Printf("No VPCs found in region %s: %v — enter manually.", region, lookupErr)
			return shared.NewForm(huh.NewGroup(
				huh.NewInput().Title("VPC ID (e.g. vpc-0abc123)").Value(vpcID),
			)).Run()
		}
		opts := make([]huh.Option[string], 0, len(vpcs)+1)
		for _, v := range vpcs {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s (%s, %s)", v.Name, v.ID, v.CIDR), v.ID))
		}
		opts = append(opts, huh.NewOption("Skip", ""))
		return shared.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select VPC").Options(opts...).Value(vpcID),
		)).Run()
	}
	return nil
}

// selectAWSRole presents a three-option picker for an IAM role name.
func selectAWSRole(ctx gocontext.Context, accountName, title string, roleName *string) error {
	var method string
	if err := shared.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Options(
				huh.NewOption("Fetch from AWS", "fetch"),
				huh.NewOption("Enter role name manually", "manual"),
				huh.NewOption("Skip (set via hook)", "skip"),
			).
			Value(&method),
	)).Run(); err != nil {
		return err
	}
	switch method {
	case "skip":
		return nil
	case "manual":
		return shared.NewForm(huh.NewGroup(
			huh.NewInput().Title("IAM role name").Value(roleName),
		)).Run()
	case "fetch":
		keyID, secret, token, err := providerconfig.NewManager(shared.GetRepoPath()).GetAWSCredentials(accountName)
		if err != nil || keyID == "" {
			log.Printf("Could not fetch AWS credentials for account '%s' — enter manually.", accountName)
			return shared.NewForm(huh.NewGroup(
				huh.NewInput().Title("IAM role name").Value(roleName),
			)).Run()
		}
		roles, lookupErr := cloudlookup.ListIAMRoles(ctx, cloudlookup.AWSCreds{
			AccessKeyID:     keyID,
			SecretAccessKey: secret,
			SessionToken:    token,
		}, "")
		if lookupErr != nil || len(roles) == 0 {
			log.Printf("No IAM roles found: %v — enter manually.", lookupErr)
			return shared.NewForm(huh.NewGroup(
				huh.NewInput().Title("IAM role name").Value(roleName),
			)).Run()
		}
		opts := make([]huh.Option[string], 0, len(roles)+1)
		for _, r := range roles {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s (%s)", r.Name, r.ARN), r.Name))
		}
		opts = append(opts, huh.NewOption("Skip", ""))
		return shared.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select IAM role").Options(opts...).Value(roleName),
		)).Run()
	}
	return nil
}

// selectAzureRG presents a three-option picker for an Azure resource group name.
func selectAzureRG(ctx gocontext.Context, subscriptionName string, resourceGroup *string) error {
	var method string
	if err := shared.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Azure resource group").
			Options(
				huh.NewOption("Fetch from Azure", "fetch"),
				huh.NewOption("Enter resource group name manually", "manual"),
				huh.NewOption("Skip (set via HYVE_RESOURCE_GROUP_NAME hook)", "skip"),
			).
			Value(&method),
	)).Run(); err != nil {
		return err
	}
	switch method {
	case "skip":
		return nil
	case "manual":
		return shared.NewForm(huh.NewGroup(
			huh.NewInput().Title("Resource group name").Value(resourceGroup),
		)).Run()
	case "fetch":
		pcMgr := providerconfig.NewManager(shared.GetRepoPath())
		subID, err := pcMgr.GetAzureSubscriptionID(subscriptionName)
		if err != nil || subID == "" {
			log.Printf("Could not resolve subscription ID for '%s' — enter manually.", subscriptionName)
			return shared.NewForm(huh.NewGroup(
				huh.NewInput().Title("Resource group name").Value(resourceGroup),
			)).Run()
		}
		tenantID, clientID, clientSecret, _ := pcMgr.GetAzureCredentials(subscriptionName)
		rgs, lookupErr := cloudlookup.ListResourceGroups(ctx, cloudlookup.AzureCreds{
			TenantID:     tenantID,
			ClientID:     clientID,
			ClientSecret: clientSecret,
		}, subID)
		if lookupErr != nil || len(rgs) == 0 {
			log.Printf("No resource groups found: %v — enter manually.", lookupErr)
			return shared.NewForm(huh.NewGroup(
				huh.NewInput().Title("Resource group name").Value(resourceGroup),
			)).Run()
		}
		opts := make([]huh.Option[string], 0, len(rgs)+1)
		for _, rg := range rgs {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s (%s)", rg.Name, rg.Location), rg.Name))
		}
		opts = append(opts, huh.NewOption("Skip", ""))
		return shared.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select resource group").Options(opts...).Value(resourceGroup),
		)).Run()
	}
	return nil
}
