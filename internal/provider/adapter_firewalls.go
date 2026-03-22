package provider

import (
	"context"
)

// ListFirewalls lists all firewalls
func (a *ProviderAdapter) ListFirewalls(ctx context.Context) ([]*Firewall, error) {
	if a.aws != nil {
		awsFirewalls, err := a.aws.ListFirewalls(ctx)
		if err != nil {
			return nil, err
		}
		var firewalls []*Firewall
		for _, f := range awsFirewalls {
			firewalls = append(firewalls, convertAWSFirewall(f))
		}
		return firewalls, nil
	}
	if a.azure != nil {
		azureFirewalls, err := a.azure.ListFirewalls(ctx)
		if err != nil {
			return nil, err
		}
		var firewalls []*Firewall
		for _, f := range azureFirewalls {
			firewalls = append(firewalls, convertAzureFirewall(f))
		}
		return firewalls, nil
	}
	if a.gcp != nil {
		gcpFirewalls, err := a.gcp.ListFirewalls(ctx)
		if err != nil {
			return nil, err
		}
		var firewalls []*Firewall
		for _, f := range gcpFirewalls {
			firewalls = append(firewalls, convertGCPFirewall(f))
		}
		return firewalls, nil
	}

	civoFirewalls, err := a.civo.ListFirewalls(ctx)
	if err != nil {
		return nil, err
	}

	var firewalls []*Firewall
	for _, f := range civoFirewalls {
		firewalls = append(firewalls, convertCivoFirewall(f))
	}

	return firewalls, nil
}

// CreateFirewall creates a firewall
func (a *ProviderAdapter) CreateFirewall(ctx context.Context, config *FirewallConfig) (*Firewall, error) {
	if a.aws != nil {
		awsConfig := convertFirewallConfigToAWS(config)
		awsFirewall, err := a.aws.CreateFirewall(ctx, awsConfig)
		if err != nil {
			return nil, err
		}
		return convertAWSFirewall(awsFirewall), nil
	}
	if a.azure != nil {
		azureConfig := convertFirewallConfigToAzure(config)
		azureFirewall, err := a.azure.CreateFirewall(ctx, azureConfig)
		if err != nil {
			return nil, err
		}
		return convertAzureFirewall(azureFirewall), nil
	}
	if a.gcp != nil {
		gcpConfig := convertFirewallConfigToGCP(config)
		gcpFirewall, err := a.gcp.CreateFirewall(ctx, gcpConfig)
		if err != nil {
			return nil, err
		}
		return convertGCPFirewall(gcpFirewall), nil
	}

	civoConfig := convertFirewallConfigToCivo(config)
	civoFirewall, err := a.civo.CreateFirewall(ctx, civoConfig)
	if err != nil {
		return nil, err
	}

	return convertCivoFirewall(civoFirewall), nil
}

// DeleteFirewall deletes a firewall
func (a *ProviderAdapter) DeleteFirewall(ctx context.Context, firewallID string) error {
	if a.aws != nil {
		return a.aws.DeleteFirewall(ctx, firewallID)
	}
	if a.azure != nil {
		return a.azure.DeleteFirewall(ctx, firewallID)
	}
	if a.gcp != nil {
		return a.gcp.DeleteFirewall(ctx, firewallID)
	}
	return a.civo.DeleteFirewall(ctx, firewallID)
}

// FindFirewallByName finds a firewall by name
func (a *ProviderAdapter) FindFirewallByName(ctx context.Context, name string) (*Firewall, error) {
	if a.aws != nil {
		awsFirewall, err := a.aws.FindFirewallByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if awsFirewall == nil {
			return nil, nil
		}
		return convertAWSFirewall(awsFirewall), nil
	}
	if a.azure != nil {
		azureFirewall, err := a.azure.FindFirewallByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if azureFirewall == nil {
			return nil, nil
		}
		return convertAzureFirewall(azureFirewall), nil
	}
	if a.gcp != nil {
		gcpFirewall, err := a.gcp.FindFirewallByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if gcpFirewall == nil {
			return nil, nil
		}
		return convertGCPFirewall(gcpFirewall), nil
	}

	civoFirewall, err := a.civo.FindFirewallByName(ctx, name)
	if err != nil {
		return nil, err
	}

	if civoFirewall == nil {
		return nil, nil
	}

	return convertCivoFirewall(civoFirewall), nil
}
