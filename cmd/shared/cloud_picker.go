package shared

import (
	gocontext "context"
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/cbridges1/hyve/internal/cloudlookup"
	"github.com/cbridges1/hyve/internal/providerconfig"
)

// SelectAWSVPC presents a three-option picker (fetch / manual / skip) for an
// AWS VPC ID. When the API lookup fails, an inline description explains why.
func SelectAWSVPC(ctx gocontext.Context, accountName, region string, vpcID *string) error {
	var method string
	if err := NewForm(huh.NewGroup(
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
		return NewForm(huh.NewGroup(
			huh.NewInput().Title("VPC ID (e.g. vpc-0abc123)").Value(vpcID),
		)).Run()
	case "fetch":
		// Build creds from stored config; fall back to SDK default chain if not stored.
		creds := cloudlookup.AWSCreds{}
		if keyID, secret, token, err := providerconfig.NewManager(GetRepoPath()).GetAWSCredentials(accountName); err == nil && keyID != "" {
			creds = cloudlookup.AWSCreds{AccessKeyID: keyID, SecretAccessKey: secret, SessionToken: token}
		}
		vpcs, lookupErr := cloudlookup.ListVPCs(ctx, creds, region)
		if lookupErr != nil || len(vpcs) == 0 {
			msg := "No non-default VPCs found in " + region
			if lookupErr != nil {
				msg = lookupErr.Error()
			}
			return NewForm(huh.NewGroup(
				huh.NewInput().
					Title("VPC ID (e.g. vpc-0abc123)").
					Description(msg + " — enter the VPC ID manually.").
					Value(vpcID),
			)).Run()
		}
		opts := make([]huh.Option[string], 0, len(vpcs)+1)
		for _, v := range vpcs {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s (%s, %s)", v.Name, v.ID, v.CIDR), v.ID))
		}
		opts = append(opts, huh.NewOption("Skip", ""))
		return NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select VPC").Options(opts...).Value(vpcID),
		)).Run()
	}
	return nil
}

// SelectAWSRole presents a three-option picker (fetch / manual / skip) for an
// IAM role name. When the API lookup fails, an inline description explains why.
func SelectAWSRole(ctx gocontext.Context, accountName, title string, roleName *string) error {
	var method string
	if err := NewForm(huh.NewGroup(
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
		return NewForm(huh.NewGroup(
			huh.NewInput().Title("IAM role name").Value(roleName),
		)).Run()
	case "fetch":
		// Build creds from stored config; fall back to SDK default chain if not stored.
		creds := cloudlookup.AWSCreds{}
		if keyID, secret, token, err := providerconfig.NewManager(GetRepoPath()).GetAWSCredentials(accountName); err == nil && keyID != "" {
			creds = cloudlookup.AWSCreds{AccessKeyID: keyID, SecretAccessKey: secret, SessionToken: token}
		}
		roles, lookupErr := cloudlookup.ListIAMRoles(ctx, creds, "")
		if lookupErr != nil || len(roles) == 0 {
			msg := "No IAM roles found"
			if lookupErr != nil {
				msg = lookupErr.Error()
			}
			return NewForm(huh.NewGroup(
				huh.NewInput().
					Title("IAM role name").
					Description(msg + " — enter the role name manually.").
					Value(roleName),
			)).Run()
		}
		opts := make([]huh.Option[string], 0, len(roles)+1)
		for _, r := range roles {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s (%s)", r.Name, r.ARN), r.Name))
		}
		opts = append(opts, huh.NewOption("Skip", ""))
		return NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select IAM role").Options(opts...).Value(roleName),
		)).Run()
	}
	return nil
}

// SelectAzureRG presents a three-option picker (fetch / manual / skip) for an
// Azure resource group. When the API lookup fails, an inline description explains why.
func SelectAzureRG(ctx gocontext.Context, subscriptionName string, resourceGroup *string) error {
	var method string
	if err := NewForm(huh.NewGroup(
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
		return NewForm(huh.NewGroup(
			huh.NewInput().Title("Resource group name").Value(resourceGroup),
		)).Run()
	case "fetch":
		pcMgr := providerconfig.NewManager(GetRepoPath())
		subID, err := pcMgr.GetAzureSubscriptionID(subscriptionName)
		if err != nil || subID == "" {
			return NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Resource group name").
					Description("Could not resolve subscription ID for '" + subscriptionName + "' — enter the resource group manually.").
					Value(resourceGroup),
			)).Run()
		}
		tenantID, clientID, clientSecret, _ := pcMgr.GetAzureCredentials(subscriptionName)
		rgs, lookupErr := cloudlookup.ListResourceGroups(ctx, cloudlookup.AzureCreds{
			TenantID:     tenantID,
			ClientID:     clientID,
			ClientSecret: clientSecret,
		}, subID)
		if lookupErr != nil || len(rgs) == 0 {
			msg := "No resource groups found"
			if lookupErr != nil {
				msg = lookupErr.Error()
			}
			return NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Resource group name").
					Description(msg + " — enter the resource group manually.").
					Value(resourceGroup),
			)).Run()
		}
		opts := make([]huh.Option[string], 0, len(rgs)+1)
		for _, rg := range rgs {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%s (%s)", rg.Name, rg.Location), rg.Name))
		}
		opts = append(opts, huh.NewOption("Skip", ""))
		return NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select resource group").Options(opts...).Value(resourceGroup),
		)).Run()
	}
	return nil
}

// SelectWorkflowHook shows a single lifecycle hook picker on its own screen.
// When wfNames is non-empty a multi-select is shown; otherwise a free-text
// input (comma-separated names) is shown. Either way dest is populated.
func SelectWorkflowHook(title string, wfNames []string, dest *[]string) error {
	if len(wfNames) > 0 {
		opts := make([]huh.Option[string], len(wfNames))
		for i, wf := range wfNames {
			opts[i] = huh.NewOption(wf, wf)
		}
		return NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title(title).
					Description("Space to toggle, enter to confirm. Leave all unselected to skip.").
					Options(opts...).
					Value(dest),
			),
		).Run()
	}
	var input string
	if err := NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Description("Enter comma-separated workflow names, or leave blank to skip.").
				Value(&input),
		),
	).Run(); err != nil {
		return err
	}
	if input != "" {
		parts := splitCSV(input)
		*dest = parts
	}
	return nil
}

// ShowNoCloudDataWarning displays a blocking notice when a cloud API call
// returned no data — typically because credentials are missing or expired.
func ShowNoCloudDataWarning(providerName string) error {
	return NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("⚠  Could not reach " + providerName + " API").
				Description("No regions or node sizes could be fetched. This usually means your credentials are missing or expired.\n\nYou can still continue and enter values manually.").
				Next(true).
				NextLabel("Continue →"),
		),
	).Run()
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range splitOnComma(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitOnComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, trimSpace(s[start:]))
	return parts
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
