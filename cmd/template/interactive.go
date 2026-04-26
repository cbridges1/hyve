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
		name             string
		description      string
		provider         string
		region           string
		nodesSizes       string
		clusterType      string
		orgName          string
		accountName      string
		vpcID            string
		eksRoleName      string
		nodeRoleName     string
		subscriptionName string
		resourceGroup    string
		projectName      string
		beforeCreate     []string
		onCreatedNames   []string
		onDestroyNames   []string
		afterDelete      []string
		schedule         string
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

	// Account / org / project / subscription first — so region/node API calls can authenticate.
	ctx := context.Background()
	accountAlias := ""
	switch provider {
	case "civo":
		if err := shared.SelectFromList("Civo organization", shared.FetchCivoOrgNames(), &orgName); err != nil && err != shared.ErrBack {
			return err
		}
		accountAlias = orgName
	case "aws":
		if err := shared.SelectFromList("AWS account alias", shared.FetchAWSAccountNames(), &accountName); err != nil && err != shared.ErrBack {
			return err
		}
		accountAlias = accountName
	case "gcp":
		if err := shared.SelectFromList("GCP project alias", shared.FetchGCPProjectNames(), &projectName); err != nil && err != shared.ErrBack {
			return err
		}
		accountAlias = projectName
	case "azure":
		if err := shared.SelectFromList("Azure subscription alias", shared.FetchAzureSubscriptionNames(), &subscriptionName); err != nil && err != shared.ErrBack {
			return err
		}
		accountAlias = subscriptionName
	}

	regionGroups := shared.FetchRegionGroups(ctx, provider, accountAlias)
	if len(regionGroups) == 0 {
		if err := shared.ShowNoCloudDataWarning(provider); err != nil {
			return err
		}
	}
	if err := shared.SelectFromGroups("Region", regionGroups, "us-east-1", &region); err != nil {
		return err
	}
	if err := shared.SelectFromGroups("Node size", shared.FetchNodeGroups(ctx, provider, region, accountAlias), "g4s.kube.medium", &nodesSizes); err != nil {
		return err
	}

	// For non-Civo providers collect node group details (count, name, scaling).
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
		nodeGroups = append(nodeGroups, types.NodeGroup{
			Name:         ngName,
			InstanceType: nodesSizes,
			Count:        count,
			MinCount:     min,
			MaxCount:     max,
		})
		nodesSizes = ""
	}

	// Cluster type only applies to Civo
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

	// Provider-specific cloud resource selection
	switch provider {
	case "aws":
		if err := shared.SelectAWSVPC(ctx, accountName, region, &vpcID); err != nil && err != shared.ErrBack {
			return err
		}
		if err := shared.SelectAWSRole(ctx, accountName, "EKS control plane role (optional)", &eksRoleName); err != nil && err != shared.ErrBack {
			return err
		}
		if err := shared.SelectAWSRole(ctx, accountName, "EKS node group role (optional)", &nodeRoleName); err != nil && err != shared.ErrBack {
			return err
		}
	case "azure":
		if err := shared.SelectAzureRG(ctx, subscriptionName, &resourceGroup); err != nil && err != shared.ErrBack {
			return err
		}
	}

	// Lifecycle hooks — one screen per hook
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

	// Optional expiry schedule
	var setSchedule bool
	if err := shared.NewForm(
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

	createTemplate(
		name, description, provider, region, nodesSizes, clusterType, nodeGroups,
		orgName, accountName, vpcID, eksRoleName, nodeRoleName, subscriptionName, resourceGroup, projectName,
		strings.Join(beforeCreate, ","),
		strings.Join(onCreatedNames, ","),
		strings.Join(onDestroyNames, ","),
		strings.Join(afterDelete, ","),
		schedule,
	)
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
	var org, account, vpcID, eksRoleName, nodeRoleName, subscription, resourceGroup, project string

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
			vpcID = tmpl.Spec.AWSVPCID
			eksRoleName = tmpl.Spec.AWSEKSRoleName
			nodeRoleName = tmpl.Spec.AWSNodeRoleName
			if vpcID == "" {
				if err := shared.SelectAWSVPC(context.Background(), account, tmpl.Spec.Region, &vpcID); err != nil && err != shared.ErrBack {
					return err
				}
			}
			if eksRoleName == "" {
				if err := shared.SelectAWSRole(context.Background(), account, "EKS control plane role (optional)", &eksRoleName); err != nil && err != shared.ErrBack {
					return err
				}
			}
			if nodeRoleName == "" {
				if err := shared.SelectAWSRole(context.Background(), account, "EKS node group role (optional)", &nodeRoleName); err != nil && err != shared.ErrBack {
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
				if err := shared.SelectAzureRG(context.Background(), subscription, &resourceGroup); err != nil && err != shared.ErrBack {
					return err
				}
			}
		}
	}

	executeTemplate(templateName, clusterName, org, account, vpcID, eksRoleName, nodeRoleName, subscription, resourceGroup, project)
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
