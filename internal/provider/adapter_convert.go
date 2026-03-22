package provider

import (
	"hyve/internal/provider/aws"
	"hyve/internal/provider/azure"
	"hyve/internal/provider/civo"
	"hyve/internal/provider/gcp"
	"hyve/internal/types"
)

// ========== Civo conversion functions ==========

func convertCivoCluster(c *civo.Cluster) *Cluster {
	return &Cluster{
		ID:         c.ID,
		Name:       c.Name,
		Status:     c.Status,
		FirewallID: c.FirewallID,
		MasterIP:   c.MasterIP,
		KubeConfig: c.KubeConfig,
		CreatedAt:  c.CreatedAt,
	}
}

func convertCivoFirewall(f *civo.Firewall) *Firewall {
	var rules []FirewallRule
	for _, rule := range f.Rules {
		rules = append(rules, FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return &Firewall{
		ID:    f.ID,
		Name:  f.Name,
		Rules: rules,
	}
}

func convertFirewallRules(rules []FirewallRule) []civo.FirewallRule {
	var civoRules []civo.FirewallRule
	for _, rule := range rules {
		civoRules = append(civoRules, civo.FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return civoRules
}

func convertFirewallConfigToCivo(config *FirewallConfig) *civo.FirewallConfig {
	return &civo.FirewallConfig{
		Name:  config.Name,
		Rules: convertFirewallRules(config.Rules),
	}
}

// ========== GCP conversion functions ==========

func convertGCPCluster(c *gcp.Cluster) *Cluster {
	// GKE uses "RUNNING" for a healthy cluster; normalize to the canonical "ACTIVE"
	// used throughout Hyve so status checks behave uniformly across providers.
	status := c.Status
	if status == "RUNNING" {
		status = "ACTIVE"
	}
	return &Cluster{
		ID:         c.ID,
		Name:       c.Name,
		Status:     status,
		FirewallID: c.FirewallID,
		MasterIP:   c.MasterIP,
		KubeConfig: c.KubeConfig,
		CreatedAt:  c.CreatedAt,
	}
}

func convertGCPFirewall(f *gcp.Firewall) *Firewall {
	var rules []FirewallRule
	for _, rule := range f.Rules {
		rules = append(rules, FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return &Firewall{
		ID:    f.ID,
		Name:  f.Name,
		Rules: rules,
	}
}

func convertFirewallRulesToGCP(rules []FirewallRule) []gcp.FirewallRule {
	var gcpRules []gcp.FirewallRule
	for _, rule := range rules {
		gcpRules = append(gcpRules, gcp.FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return gcpRules
}

func convertFirewallConfigToGCP(config *FirewallConfig) *gcp.FirewallConfig {
	return &gcp.FirewallConfig{
		Name:  config.Name,
		Rules: convertFirewallRulesToGCP(config.Rules),
	}
}

// ========== AWS conversion functions ==========

func convertAWSCluster(c *aws.Cluster) *Cluster {
	return &Cluster{
		ID:         c.ID,
		Name:       c.Name,
		Status:     c.Status,
		FirewallID: c.FirewallID,
		MasterIP:   c.MasterIP,
		KubeConfig: c.KubeConfig,
		CreatedAt:  c.CreatedAt,
	}
}

func convertAWSFirewall(f *aws.Firewall) *Firewall {
	var rules []FirewallRule
	for _, rule := range f.Rules {
		rules = append(rules, FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return &Firewall{
		ID:    f.ID,
		Name:  f.Name,
		Rules: rules,
	}
}

func convertFirewallRulesToAWS(rules []FirewallRule) []aws.FirewallRule {
	var awsRules []aws.FirewallRule
	for _, rule := range rules {
		awsRules = append(awsRules, aws.FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return awsRules
}

func convertFirewallConfigToAWS(config *FirewallConfig) *aws.FirewallConfig {
	return &aws.FirewallConfig{
		Name:  config.Name,
		Rules: convertFirewallRulesToAWS(config.Rules),
	}
}

// ========== Azure conversion functions ==========

func convertAzureCluster(c *azure.Cluster) *Cluster {
	// AKS ProvisioningState is "Succeeded" for a running cluster; normalize to
	// the canonical "ACTIVE" used throughout Hyve.
	status := c.Status
	if status == "Succeeded" {
		status = "ACTIVE"
	}
	return &Cluster{
		ID:         c.ID,
		Name:       c.Name,
		Status:     status,
		FirewallID: c.FirewallID,
		MasterIP:   c.MasterIP,
		KubeConfig: c.KubeConfig,
		CreatedAt:  c.CreatedAt,
	}
}

func convertAzureFirewall(f *azure.Firewall) *Firewall {
	var rules []FirewallRule
	for _, rule := range f.Rules {
		rules = append(rules, FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return &Firewall{
		ID:    f.ID,
		Name:  f.Name,
		Rules: rules,
	}
}

func convertFirewallRulesToAzure(rules []FirewallRule) []azure.FirewallRule {
	var azureRules []azure.FirewallRule
	for _, rule := range rules {
		azureRules = append(azureRules, azure.FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return azureRules
}

func convertFirewallConfigToAzure(config *FirewallConfig) *azure.FirewallConfig {
	return &azure.FirewallConfig{
		Name:  config.Name,
		Rules: convertFirewallRulesToAzure(config.Rules),
	}
}

// clusterInfoFrom constructs a ClusterInfo from its individual fields.
func clusterInfoFrom(name, ip, port, kc, status, id string, ng []types.NodeGroup) *ClusterInfo {
	return &ClusterInfo{Name: name, IPAddress: ip, AccessPort: port, Kubeconfig: kc, Status: status, ID: id, NodeGroups: ng}
}
