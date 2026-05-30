package cloudlookup

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// AzureCreds holds credentials for Azure API calls. All fields are optional;
// empty fields fall back to DefaultAzureCredential.
type AzureCreds struct {
	TenantID     string
	ClientID     string
	ClientSecret string
}

// ResourceGroupOption represents an Azure resource group available for selection.
type ResourceGroupOption struct {
	Name     string
	Location string
}

// ListResourceGroups returns all resource groups in the given subscription.
func ListResourceGroups(ctx context.Context, creds AzureCreds, subscriptionID string) ([]ResourceGroupOption, error) {
	var rgClient *armresources.ResourceGroupsClient
	var err error

	if creds.TenantID != "" && creds.ClientID != "" && creds.ClientSecret != "" {
		cred, credErr := azidentity.NewClientSecretCredential(creds.TenantID, creds.ClientID, creds.ClientSecret, nil)
		if credErr != nil {
			return nil, fmt.Errorf("create service principal credential: %w", credErr)
		}
		rgClient, err = armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	} else {
		defCred, credErr := azidentity.NewDefaultAzureCredential(nil)
		if credErr != nil {
			return nil, fmt.Errorf("create default credential: %w", credErr)
		}
		rgClient, err = armresources.NewResourceGroupsClient(subscriptionID, defCred, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create resource groups client: %w", err)
	}

	var result []ResourceGroupOption
	pager := rgClient.NewListPager(nil)
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return nil, fmt.Errorf("list resource groups: %w", pageErr)
		}
		for _, rg := range page.Value {
			if rg.Name != nil && rg.Location != nil {
				result = append(result, ResourceGroupOption{
					Name:     *rg.Name,
					Location: *rg.Location,
				})
			}
		}
	}
	return result, nil
}
