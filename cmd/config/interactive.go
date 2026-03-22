package config

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/cmd/shared"
)

// RunInteractive runs the interactive config menu.
func RunInteractive() error {
	return runInteractiveConfig()
}

func runInteractiveConfig() error {
	for {
		var section string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Config — which provider?").
					Options(
						huh.NewOption("Civo configuration", "civo"),
						huh.NewOption("GCP configuration", "gcp"),
						huh.NewOption("AWS configuration", "aws"),
						huh.NewOption("Azure configuration", "azure"),
						huh.NewOption("← Back", "back"),
					).
					Value(&section),
			),
		).Run()
		if err != nil {
			return err
		}

		switch section {
		case "back":
			return shared.ErrBack
		case "civo":
			if err := interactiveConfigCivo(); err != nil && err != shared.ErrBack {
				return err
			}
		case "gcp":
			if err := interactiveConfigGCP(); err != nil && err != shared.ErrBack {
				return err
			}
		case "aws":
			if err := interactiveConfigAWS(); err != nil && err != shared.ErrBack {
				return err
			}
		case "azure":
			if err := interactiveConfigAzure(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

// ── Civo ─────────────────────────────────────────────────────────────────────

func interactiveConfigCivo() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Civo token — action").
					Options(
						huh.NewOption("List orgs", "list"),
						huh.NewOption("Set token", "set"),
						huh.NewOption("Get token", "get"),
						huh.NewOption("Clear token", "clear"),
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
			configCivoOrgListCmd.Run(configCivoOrgListCmd, nil)
		case "set":
			var org, token string
			if err := shared.SelectFromList("Organization", shared.FetchCivoOrgNames(), &org); err != nil {
				return err
			}
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Token (leave blank to be prompted)").Value(&token),
				),
			).Run()
			if err != nil {
				return err
			}
			configCivoSetTokenCmd.Flags().Set("org", org)
			if token != "" {
				configCivoSetTokenCmd.Flags().Set("token", token)
			}
			configCivoSetTokenCmd.Run(configCivoSetTokenCmd, nil)
		case "get":
			org := ""
			if err := shared.SelectFromList("Organization", shared.FetchCivoOrgNames(), &org); err != nil {
				return err
			}
			configCivoGetTokenCmd.Flags().Set("org", org)
			configCivoGetTokenCmd.Run(configCivoGetTokenCmd, nil)
		case "clear":
			org := ""
			if err := shared.SelectFromList("Organization to clear token for", shared.FetchCivoOrgNames(), &org); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Clear Civo token for org '%s'?", org)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configCivoClearTokenCmd.Flags().Set("org", org)
			configCivoClearTokenCmd.Run(configCivoClearTokenCmd, nil)
		}
	}
}

// ── GCP ──────────────────────────────────────────────────────────────────────

func interactiveConfigGCP() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("GCP project — action").
					Options(
						huh.NewOption("List projects", "list"),
						huh.NewOption("Add project alias", "add"),
						huh.NewOption("Get project", "get"),
						huh.NewOption("Remove project alias", "remove"),
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
			configGCPListProjectsCmd.Run(configGCPListProjectsCmd, nil)
		case "add":
			var name, id string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Project alias").Placeholder("dev").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("GCP project ID").Placeholder("my-project-123").Validate(shared.RequireNotEmpty).Value(&id),
				),
			).Run()
			if err != nil {
				return err
			}
			configGCPAddProjectCmd.Flags().Set("name", name)
			configGCPAddProjectCmd.Flags().Set("id", id)
			configGCPAddProjectCmd.Run(configGCPAddProjectCmd, nil)
		case "get":
			alias := ""
			if err := shared.SelectFromList("Project alias", shared.FetchGCPProjectNames(), &alias); err != nil {
				return err
			}
			configGCPGetProjectCmd.Run(configGCPGetProjectCmd, []string{alias})
		case "remove":
			alias := ""
			if err := shared.SelectFromList("Project alias to remove", shared.FetchGCPProjectNames(), &alias); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Remove GCP project alias '%s'?", alias)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configGCPRemoveProjectCmd.Run(configGCPRemoveProjectCmd, []string{alias})
		}
	}
}

// ── AWS ──────────────────────────────────────────────────────────────────────

func interactiveConfigAWS() error {
	for {
		var resource string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("AWS — what to configure?").
					Options(
						huh.NewOption("Account aliases", "account"),
						huh.NewOption("EKS role aliases", "eks-role"),
						huh.NewOption("VPC aliases", "vpc"),
						huh.NewOption("← Back", "back"),
					).
					Value(&resource),
			),
		).Run()
		if err != nil {
			return err
		}

		switch resource {
		case "back":
			return shared.ErrBack
		case "account":
			if err := interactiveConfigAWSAccount(); err != nil && err != shared.ErrBack {
				return err
			}
		case "eks-role":
			if err := interactiveConfigAWSEKSRole(); err != nil && err != shared.ErrBack {
				return err
			}
		case "vpc":
			if err := interactiveConfigAWSVPC(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveConfigAWSAccount() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("AWS account — action").
					Options(
						huh.NewOption("List", "list"),
						huh.NewOption("Add", "add"),
						huh.NewOption("Get", "get"),
						huh.NewOption("Remove", "remove"),
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
			configAWSAccountListCmd.Run(configAWSAccountListCmd, nil)
		case "add":
			var name, id string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Account alias").Placeholder("prod").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("AWS account ID").Placeholder("123456789012").Validate(shared.RequireNotEmpty).Value(&id),
				),
			).Run()
			if err != nil {
				return err
			}
			configAWSAccountAddCmd.Flags().Set("name", name)
			configAWSAccountAddCmd.Flags().Set("id", id)
			configAWSAccountAddCmd.Run(configAWSAccountAddCmd, nil)
		case "get":
			alias := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &alias); err != nil {
				return err
			}
			configAWSAccountGetCmd.Run(configAWSAccountGetCmd, []string{alias})
		case "remove":
			alias := ""
			if err := shared.SelectFromList("Account alias to remove", shared.FetchAWSAccountNames(), &alias); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Remove AWS account alias '%s'?", alias)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configAWSAccountRemoveCmd.Run(configAWSAccountRemoveCmd, []string{alias})
		}
	}
}

func interactiveConfigAWSEKSRole() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("EKS role — action").
					Options(
						huh.NewOption("List", "list"),
						huh.NewOption("Add (register existing)", "add"),
						huh.NewOption("Create in AWS", "create"),
						huh.NewOption("Get", "get"),
						huh.NewOption("Remove alias", "remove"),
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
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			configAWSEKSRoleListCmd.Flags().Set("account", account)
			configAWSEKSRoleListCmd.Run(configAWSEKSRoleListCmd, nil)
		case "add":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			var name, roleARN string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Role alias").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("IAM role ARN").Placeholder("arn:aws:iam::123456789012:role/...").Validate(shared.RequireNotEmpty).Value(&roleARN),
				),
			).Run()
			if err != nil {
				return err
			}
			configAWSEKSRoleAddCmd.Flags().Set("account", account)
			configAWSEKSRoleAddCmd.Flags().Set("name", name)
			configAWSEKSRoleAddCmd.Flags().Set("role-arn", roleARN)
			configAWSEKSRoleAddCmd.Run(configAWSEKSRoleAddCmd, nil)
		case "create":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			var name, roleName, region string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Role alias").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("IAM role name in AWS").Placeholder("hyve-eks-role").Validate(shared.RequireNotEmpty).Value(&roleName),
					huh.NewInput().Title("Region").Placeholder("us-east-1").Validate(shared.RequireNotEmpty).Value(&region),
				),
			).Run()
			if err != nil {
				return err
			}
			configAWSEKSRoleCreateCmd.Flags().Set("account", account)
			configAWSEKSRoleCreateCmd.Flags().Set("name", name)
			configAWSEKSRoleCreateCmd.Flags().Set("role-name", roleName)
			configAWSEKSRoleCreateCmd.Flags().Set("region", region)
			configAWSEKSRoleCreateCmd.Run(configAWSEKSRoleCreateCmd, nil)
		case "get":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			name := ""
			if err := shared.SelectFromList("Role alias", shared.FetchAWSEKSRoleNames(account), &name); err != nil {
				return err
			}
			configAWSEKSRoleGetCmd.Flags().Set("account", account)
			configAWSEKSRoleGetCmd.Flags().Set("name", name)
			configAWSEKSRoleGetCmd.Run(configAWSEKSRoleGetCmd, nil)
		case "remove":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			name := ""
			if err := shared.SelectFromList("Role alias to remove", shared.FetchAWSEKSRoleNames(account), &name); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Remove EKS role alias '%s' from account '%s'?", name, account)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configAWSEKSRoleRemoveCmd.Flags().Set("account", account)
			configAWSEKSRoleRemoveCmd.Flags().Set("name", name)
			configAWSEKSRoleRemoveCmd.Run(configAWSEKSRoleRemoveCmd, nil)
		}
	}
}

func interactiveConfigAWSVPC() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("VPC — action").
					Options(
						huh.NewOption("List", "list"),
						huh.NewOption("Add (register existing)", "add"),
						huh.NewOption("Create in AWS", "create"),
						huh.NewOption("Get", "get"),
						huh.NewOption("Remove alias", "remove"),
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
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			configAWSVPCListCmd.Flags().Set("account", account)
			configAWSVPCListCmd.Run(configAWSVPCListCmd, nil)
		case "add":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			var name, id string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("VPC alias").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("VPC ID").Placeholder("vpc-0123456789abcdef0").Validate(shared.RequireNotEmpty).Value(&id),
				),
			).Run()
			if err != nil {
				return err
			}
			configAWSVPCAddCmd.Flags().Set("account", account)
			configAWSVPCAddCmd.Flags().Set("name", name)
			configAWSVPCAddCmd.Flags().Set("id", id)
			configAWSVPCAddCmd.Run(configAWSVPCAddCmd, nil)
		case "create":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			var name, region, cidr, subnets string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("VPC alias").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("Region").Placeholder("us-east-1").Validate(shared.RequireNotEmpty).Value(&region),
					huh.NewInput().Title("CIDR block (optional)").Placeholder("10.0.0.0/16").Value(&cidr),
					huh.NewInput().Title("Subnet CIDRs, comma-separated (optional)").Placeholder("10.0.1.0/24,10.0.2.0/24").Value(&subnets),
				),
			).Run()
			if err != nil {
				return err
			}
			configAWSVPCCreateCmd.Flags().Set("account", account)
			configAWSVPCCreateCmd.Flags().Set("name", name)
			configAWSVPCCreateCmd.Flags().Set("region", region)
			if cidr != "" {
				configAWSVPCCreateCmd.Flags().Set("cidr", cidr)
			}
			if subnets != "" {
				configAWSVPCCreateCmd.Flags().Set("subnets", subnets)
			}
			configAWSVPCCreateCmd.Run(configAWSVPCCreateCmd, nil)
		case "get":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			name := ""
			if err := shared.SelectFromList("VPC alias", shared.FetchAWSVPCNames(account), &name); err != nil {
				return err
			}
			configAWSVPCGetCmd.Flags().Set("account", account)
			configAWSVPCGetCmd.Flags().Set("name", name)
			configAWSVPCGetCmd.Run(configAWSVPCGetCmd, nil)
		case "remove":
			account := ""
			if err := shared.SelectFromList("Account alias", shared.FetchAWSAccountNames(), &account); err != nil {
				return err
			}
			name := ""
			if err := shared.SelectFromList("VPC alias to remove", shared.FetchAWSVPCNames(account), &name); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Remove VPC alias '%s' from account '%s'?", name, account)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configAWSVPCRemoveCmd.Flags().Set("account", account)
			configAWSVPCRemoveCmd.Flags().Set("name", name)
			configAWSVPCRemoveCmd.Run(configAWSVPCRemoveCmd, nil)
		}
	}
}

// ── Azure ─────────────────────────────────────────────────────────────────────

func interactiveConfigAzure() error {
	for {
		var resource string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Azure — what to configure?").
					Options(
						huh.NewOption("Subscription aliases", "subscription"),
						huh.NewOption("Resource groups", "resource-group"),
						huh.NewOption("← Back", "back"),
					).
					Value(&resource),
			),
		).Run()
		if err != nil {
			return err
		}

		switch resource {
		case "back":
			return shared.ErrBack
		case "subscription":
			if err := interactiveConfigAzureSubscription(); err != nil && err != shared.ErrBack {
				return err
			}
		case "resource-group":
			if err := interactiveConfigAzureResourceGroup(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveConfigAzureSubscription() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Subscription — action").
					Options(
						huh.NewOption("List", "list"),
						huh.NewOption("Add", "add"),
						huh.NewOption("Remove", "remove"),
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
			configAzureListSubscriptionIDsCmd.Run(configAzureListSubscriptionIDsCmd, nil)
		case "add":
			var name, id string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Subscription alias").Placeholder("prod-sub").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("Azure subscription ID").Placeholder("xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx").Validate(shared.RequireNotEmpty).Value(&id),
				),
			).Run()
			if err != nil {
				return err
			}
			configAzureAddSubscriptionIDsCmd.Flags().Set("name", name)
			configAzureAddSubscriptionIDsCmd.Flags().Set("id", id)
			configAzureAddSubscriptionIDsCmd.Run(configAzureAddSubscriptionIDsCmd, nil)
		case "remove":
			name := ""
			if err := shared.SelectFromList("Subscription to remove", shared.FetchAzureSubscriptionNames(), &name); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Remove Azure subscription '%s'?", name)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configAzureRemoveSubscriptionIDsCmd.Run(configAzureRemoveSubscriptionIDsCmd, []string{name})
		}
	}
}

func interactiveConfigAzureResourceGroup() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Resource group — action").
					Options(
						huh.NewOption("List", "list"),
						huh.NewOption("Add (create in Azure)", "add"),
						huh.NewOption("Delete", "delete"),
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
			sub := ""
			if err := shared.SelectFromList("Subscription alias", shared.FetchAzureSubscriptionNames(), &sub); err != nil {
				return err
			}
			configAzureListResourceGroupsCmd.Flags().Set("subscription", sub)
			configAzureListResourceGroupsCmd.Run(configAzureListResourceGroupsCmd, nil)
		case "add":
			sub := ""
			if err := shared.SelectFromList("Subscription alias", shared.FetchAzureSubscriptionNames(), &sub); err != nil {
				return err
			}
			var name, location string
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Resource group name").Placeholder("hyve-rg").Validate(shared.RequireNotEmpty).Value(&name),
					huh.NewInput().Title("Location/region").Placeholder("eastus").Validate(shared.RequireNotEmpty).Value(&location),
				),
			).Run()
			if err != nil {
				return err
			}
			configAzureAddResourceGroupCmd.Flags().Set("subscription", sub)
			configAzureAddResourceGroupCmd.Flags().Set("name", name)
			configAzureAddResourceGroupCmd.Flags().Set("location", location)
			configAzureAddResourceGroupCmd.Run(configAzureAddResourceGroupCmd, nil)
		case "delete":
			sub := ""
			if err := shared.SelectFromList("Subscription alias", shared.FetchAzureSubscriptionNames(), &sub); err != nil {
				return err
			}
			name := ""
			if err := shared.SelectFromList("Resource group to delete", shared.FetchAzureResourceGroupNames(sub), &name); err != nil {
				return err
			}
			var confirm bool
			err = shared.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Delete resource group '%s' from subscription '%s'? This cannot be undone.", name, sub)).
						Value(&confirm),
				),
			).Run()
			if err != nil {
				return err
			}
			if !confirm {
				continue
			}
			configAzureDeleteResourceGroupCmd.Flags().Set("subscription", sub)
			configAzureDeleteResourceGroupCmd.Flags().Set("name", name)
			configAzureDeleteResourceGroupCmd.Run(configAzureDeleteResourceGroupCmd, nil)
		}
	}
}
