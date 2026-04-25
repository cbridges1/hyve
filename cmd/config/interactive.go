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
					Title("Civo — action").
					Options(
						huh.NewOption("List orgs", "list"),
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
