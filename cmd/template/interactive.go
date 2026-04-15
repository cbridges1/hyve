package template

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/types"
)

// RunInteractive runs the interactive template menu.
func RunInteractive() error {
	return runInteractiveTemplate()
}

func runInteractiveTemplate() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Template — what would you like to do?").
					Options(
						huh.NewOption("List templates", "list"),
						huh.NewOption("Create a template", "create"),
						huh.NewOption("Execute a template", "execute"),
						huh.NewOption("Show template details", "show"),
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
		case "create":
			if err := interactiveTemplateCreate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "execute":
			if err := interactiveTemplateExecute(); err != nil && err != shared.ErrBack {
				return err
			}
		case "show":
			if err := interactiveTemplateShow(); err != nil && err != shared.ErrBack {
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

func interactiveTemplateCreate() error {
	var (
		name           string
		description    string
		provider       string
		region         string
		nodesSizes     string
		clusterType    string
		onCreatedNames []string
		onDestroyNames []string
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Template name").Placeholder("my-template").Validate(shared.RequireNotEmpty).Value(&name),
			huh.NewInput().Title("Description (optional)").Value(&description),
			huh.NewSelect[string]().
				Title("Cloud provider").
				Options(
					huh.NewOption("Civo", "civo"),
					huh.NewOption("AWS (EKS)", "aws"),
					huh.NewOption("GCP (GKE)", "gcp"),
					huh.NewOption("Azure (AKS)", "azure"),
					huh.NewOption("← Back", "back"),
				).
				Value(&provider),
		),
	).Run()
	if err != nil {
		return err
	}
	if provider == "back" {
		return shared.ErrBack
	}

	ctx := context.Background()
	if err := shared.SelectFromGroups("Region", shared.FetchRegionGroups(ctx, provider, ""), "us-east-1", &region); err != nil {
		return err
	}
	if err := shared.SelectFromGroups("Node size", shared.FetchNodeGroups(ctx, provider, region, ""), "g4s.kube.medium", &nodesSizes); err != nil {
		return err
	}

	// For non-Civo providers collect node group details (count, name, scaling).
	// Civo uses the flat node size list; AWS/GCP/Azure need node groups with counts.
	var nodeGroups []types.NodeGroup
	if provider != "civo" {
		var ngName, ngCountStr, ngMinStr, ngMaxStr string
		ngName = "default"
		ngCountStr = "1"
		err = shared.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Node group name").
					Placeholder("default").
					Validate(shared.RequireNotEmpty).
					Value(&ngName),
				huh.NewInput().
					Title("Node count").
					Placeholder("1").
					Validate(shared.RequireNotEmpty).
					Value(&ngCountStr),
				huh.NewInput().
					Title("Min count (leave blank to match count)").
					Value(&ngMinStr),
				huh.NewInput().
					Title("Max count (leave blank to match count)").
					Value(&ngMaxStr),
			),
		).Run()
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(ngCountStr)
		if count < 1 {
			count = 1
		}
		min, _ := strconv.Atoi(ngMinStr)
		max, _ := strconv.Atoi(ngMaxStr)
		ng := types.NodeGroup{
			Name:         ngName,
			InstanceType: nodesSizes,
			Count:        count,
			MinCount:     min,
			MaxCount:     max,
		}
		nodeGroups = append(nodeGroups, ng)
		nodesSizes = "" // NodeGroups takes precedence; clear the flat nodes field
	}

	// Cluster type is only applicable to Civo
	if provider == "civo" {
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

	// Workflow attachment — optional last step
	if wfNames := shared.FetchWorkflowNames(); len(wfNames) > 0 {
		makeOpts := func() []huh.Option[string] {
			opts := make([]huh.Option[string], len(wfNames))
			for i, wf := range wfNames {
				opts[i] = huh.NewOption(wf, wf)
			}
			return opts
		}
		err = shared.NewForm(
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
		).Run()
		if err != nil {
			return err
		}
	}

	onCreatedStr := strings.Join(onCreatedNames, ",")
	onDestroyStr := strings.Join(onDestroyNames, ",")
	createTemplate(name, description, provider, region, nodesSizes, clusterType, nodeGroups, onCreatedStr, onDestroyStr)
	return nil
}

func interactiveTemplateExecute() error {
	templateName := ""
	if err := shared.SelectFromList("Template to execute", shared.FetchTemplateNames(), &templateName); err != nil {
		return err
	}

	var clusterName string
	if err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("New cluster name").Validate(shared.RequireNotEmpty).Value(&clusterName),
		),
	).Run(); err != nil {
		return err
	}

	// Load the template to determine which account fields are already set.
	// For any missing required fields, prompt the user.
	var org, account, vpcName, eksRole, nodeRole, subscription, resourceGroup, project, expiresAt string

	if tmpl := shared.FetchTemplate(templateName); tmpl != nil {
		switch strings.ToLower(tmpl.Spec.Provider) {
		case "civo":
			org = tmpl.Spec.CivoOrganization
			if org == "" {
				if err := shared.SelectFromList("Civo organization", shared.FetchCivoOrgNames(), &org); err != nil && err != shared.ErrBack {
					return err
				}
			}

		case "aws":
			account = tmpl.Spec.AWSAccount
			if account == "" {
				if err := shared.SelectFromList("AWS account alias", shared.FetchAWSAccountNames(), &account); err != nil && err != shared.ErrBack {
					return err
				}
			}
			vpcName = tmpl.Spec.AWSVPCName
			if vpcName == "" {
				if err := shared.SelectFromList("VPC alias", shared.FetchAWSVPCNames(account), &vpcName); err != nil && err != shared.ErrBack {
					return err
				}
			}
			eksRole = tmpl.Spec.AWSEKSRole
			if eksRole == "" {
				if err := shared.SelectFromList("EKS role alias", shared.FetchAWSEKSRoleNames(account), &eksRole); err != nil && err != shared.ErrBack {
					return err
				}
			}
			nodeRole = tmpl.Spec.AWSNodeRole
			if nodeRole == "" {
				if err := shared.SelectFromList("Node role alias", shared.FetchAWSNodeRoleNames(account), &nodeRole); err != nil && err != shared.ErrBack {
					return err
				}
			}

		case "gcp":
			project = tmpl.Spec.GCPProject
			if project == "" {
				if err := shared.SelectFromList("GCP project alias", shared.FetchGCPProjectNames(), &project); err != nil && err != shared.ErrBack {
					return err
				}
			}

		case "azure":
			subscription = tmpl.Spec.AzureSubscription
			if subscription == "" {
				if err := shared.SelectFromList("Azure subscription alias", shared.FetchAzureSubscriptionNames(), &subscription); err != nil && err != shared.ErrBack {
					return err
				}
			}
			resourceGroup = tmpl.Spec.AzureResourceGroup
			if resourceGroup == "" {
				if err := shared.SelectFromList("Azure resource group", shared.FetchAzureResourceGroupNames(subscription), &resourceGroup); err != nil && err != shared.ErrBack {
					return err
				}
			}
		}
	}

	// Optional expiry
	var expiryErr error
	expiresAt, expiryErr = shared.PromptExpiresAt("")
	if expiryErr != nil {
		return expiryErr
	}

	executeTemplate(templateName, clusterName, org, account, vpcName, eksRole, nodeRole, subscription, resourceGroup, project, expiresAt)
	return nil
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

func interactiveTemplateDelete() error {
	name := ""
	if err := shared.SelectFromList("Template to delete", shared.FetchTemplateNames(), &name); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete template '%s'?", name)).
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
