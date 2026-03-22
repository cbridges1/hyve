package civo

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/civo/civogo"

	"hyve/internal/types"
)

// Cluster represents a generic cluster
type Cluster struct {
	ID         string
	Name       string
	Status     string
	FirewallID string
	MasterIP   string
	KubeConfig string
	CreatedAt  time.Time
}

// Firewall represents a generic firewall
type Firewall struct {
	ID    string
	Name  string
	Rules []FirewallRule
}

// FirewallRule represents a generic firewall rule
type FirewallRule struct {
	Protocol  string
	StartPort string
	EndPort   string
	Cidr      []string
	Direction string
}

// LoadBalancer represents a generic load balancer
type LoadBalancer struct {
	ID        string
	Name      string
	PublicIP  string
	ClusterID string
}

// ClusterConfig represents cluster creation configuration
type ClusterConfig struct {
	Name         string
	Region       string
	Nodes        []string
	NodeGroups   []types.NodeGroup
	ClusterType  string
	FirewallID   string
	Applications []string
}

// ClusterUpdateConfig represents cluster update configuration
type ClusterUpdateConfig struct {
	Name       string
	Nodes      []string
	NodeGroups []types.NodeGroup
}

// FirewallConfig represents firewall creation configuration
type FirewallConfig struct {
	Name  string
	Rules []FirewallRule
}

// ClusterInfo represents exported cluster information
type ClusterInfo struct {
	Name       string
	IPAddress  string
	AccessPort string
	Kubeconfig string
	Status     string
	ID         string
	NodeGroups []types.NodeGroup
}

// Provider implements the provider interfaces for Civo
type Provider struct {
	client *civogo.Client
	region string
}

// NewProvider creates a new Civo provider
func NewProvider(apiKey, region string) (*Provider, error) {
	client, err := civogo.NewClient(apiKey, region)
	if err != nil {
		return nil, fmt.Errorf("failed to create Civo client: %w", err)
	}

	return &Provider{
		client: client,
		region: region,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "civo"
}

// Region returns the provider region
func (p *Provider) Region() string {
	return p.region
}

// ListClusters lists all clusters
func (p *Provider) ListClusters(ctx context.Context) ([]*Cluster, error) {
	civoClusters, err := p.client.ListKubernetesClusters()
	if err != nil {
		return nil, err
	}

	var clusters []*Cluster
	for _, c := range civoClusters.Items {
		clusters = append(clusters, p.convertCluster(&c))
	}

	return clusters, nil
}

// GetCluster gets a cluster by ID
func (p *Provider) GetCluster(ctx context.Context, clusterID string) (*Cluster, error) {
	civoCluster, err := p.client.GetKubernetesCluster(clusterID)
	if err != nil {
		return nil, err
	}

	return p.convertCluster(civoCluster), nil
}

// FindClusterByName finds a cluster by name
func (p *Provider) FindClusterByName(ctx context.Context, name string) (*Cluster, error) {
	clusters, err := p.client.ListKubernetesClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusters.Items {
		if cluster.Name == name {
			return p.convertCluster(&cluster), nil
		}
	}

	return nil, nil
}

// CreateCluster creates a new cluster
func (p *Provider) CreateCluster(ctx context.Context, config *ClusterConfig) (*Cluster, error) {
	log.Printf("Creating cluster %s in region %s", config.Name, config.Region)

	// Resolve node size and count from NodeGroups or legacy Nodes
	nodeSize := "g4s.kube.small"
	nodeCount := len(config.Nodes)

	if len(config.NodeGroups) > 0 {
		// Civo only supports a single homogeneous pool; use the first node group
		ng := config.NodeGroups[0]
		if ng.InstanceType != "" {
			nodeSize = ng.InstanceType
		}
		if ng.Count > 0 {
			nodeCount = ng.Count
		}
		if len(config.NodeGroups) > 1 {
			log.Printf("Warning: Civo only supports a single node pool; using first node group '%s'", ng.Name)
		}
	} else if len(config.Nodes) > 0 {
		nodeSize = config.Nodes[0]
	}
	if nodeCount < 1 {
		nodeCount = 1
	}

	clusterConfig := &civogo.KubernetesClusterConfig{
		Name:            config.Name,
		Region:          config.Region,
		NumTargetNodes:  nodeCount,
		TargetNodesSize: nodeSize,
		NodeDestroy:     "",
		NetworkID:       "",
		Tags:            "",
		Applications:    "",
	}

	log.Printf("Creating cluster %v", clusterConfig)

	if len(config.Applications) > 0 {
		clusterConfig.Applications = config.Applications[0]
	}

	cluster, err := p.client.NewKubernetesClusters(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster: %w", err)
	}

	log.Printf("Cluster creation started with ID: %s", cluster.ID)
	return p.convertCluster(cluster), nil
}

// UpdateCluster updates an existing cluster
func (p *Provider) UpdateCluster(ctx context.Context, clusterID string, config *ClusterUpdateConfig) (*Cluster, error) {
	// Resolve node size and count from NodeGroups or legacy Nodes
	nodeSize := ""
	nodeCount := len(config.Nodes)

	if len(config.NodeGroups) > 0 {
		ng := config.NodeGroups[0]
		if ng.InstanceType != "" {
			nodeSize = ng.InstanceType
		}
		if ng.Count > 0 {
			nodeCount = ng.Count
		}
	} else if len(config.Nodes) > 0 {
		nodeSize = config.Nodes[0]
	}

	updateConfig := &civogo.KubernetesClusterConfig{
		Name:            config.Name,
		NumTargetNodes:  nodeCount,
		TargetNodesSize: nodeSize,
	}

	cluster, err := p.client.UpdateKubernetesCluster(clusterID, updateConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to update cluster: %w", err)
	}

	return p.convertCluster(cluster), nil
}

// DeleteCluster deletes a cluster
func (p *Provider) DeleteCluster(ctx context.Context, clusterID string) error {
	_, err := p.client.DeleteKubernetesCluster(clusterID)
	return err
}

// WaitForClusterReady waits for cluster to be ready
func (p *Provider) WaitForClusterReady(ctx context.Context, clusterID string) error {
	for {
		cluster, err := p.client.GetKubernetesCluster(clusterID)
		if err != nil {
			return fmt.Errorf("failed to get cluster status: %w", err)
		}

		log.Printf("Cluster status: %s, waiting...", cluster.Status)

		if cluster.Status == "FAILED" {
			return fmt.Errorf("cluster creation failed")
		}

		// Wait for both ACTIVE status and a non-empty kubeconfig. The Civo API
		// may briefly report ACTIVE before the kubeconfig is available.
		if cluster.Status == "ACTIVE" && cluster.KubeConfig != "" {
			break
		}

		time.Sleep(30 * time.Second)
	}

	return nil
}

// GetClusterInfo gets cluster information for export
func (p *Provider) GetClusterInfo(ctx context.Context, name string) (*ClusterInfo, error) {
	cluster, err := p.FindClusterByName(ctx, name)
	if err != nil {
		return nil, err
	}

	if cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", name)
	}

	clusterDetails, err := p.client.GetKubernetesCluster(cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster details for %s: %w", name, err)
	}

	var nodeGroups []types.NodeGroup
	for _, pool := range clusterDetails.Pools {
		nodeGroups = append(nodeGroups, types.NodeGroup{
			Name:         pool.ID,
			InstanceType: pool.Size,
			Count:        pool.Count,
		})
	}

	info := &ClusterInfo{
		Name:       cluster.Name,
		IPAddress:  clusterDetails.MasterIP,
		AccessPort: "6443",
		Kubeconfig: clusterDetails.KubeConfig,
		Status:     cluster.Status,
		ID:         cluster.ID,
		NodeGroups: nodeGroups,
	}

	return info, nil
}

// ListFirewalls lists all firewalls
func (p *Provider) ListFirewalls(ctx context.Context) ([]*Firewall, error) {
	civoFirewalls, err := p.client.ListFirewalls()
	if err != nil {
		return nil, err
	}

	var firewalls []*Firewall
	for _, f := range civoFirewalls {
		firewalls = append(firewalls, p.convertFirewall(&f))
	}

	return firewalls, nil
}

// CreateFirewall creates a firewall
func (p *Provider) CreateFirewall(ctx context.Context, config *FirewallConfig) (*Firewall, error) {
	var rules []civogo.FirewallRule
	for _, rule := range config.Rules {
		rules = append(rules, civogo.FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	firewallConfig := &civogo.FirewallConfig{
		Name:  config.Name,
		Rules: rules,
	}

	firewall, err := p.client.NewFirewall(firewallConfig)
	if err != nil {
		return nil, err
	}

	return &Firewall{
		ID:    firewall.ID,
		Name:  firewall.Name,
		Rules: config.Rules,
	}, nil
}

// DeleteFirewall deletes a firewall
func (p *Provider) DeleteFirewall(ctx context.Context, firewallID string) error {
	_, err := p.client.DeleteFirewall(firewallID)
	return err
}

// FindFirewallByName finds a firewall by name
func (p *Provider) FindFirewallByName(ctx context.Context, name string) (*Firewall, error) {
	firewalls, err := p.client.ListFirewalls()
	if err != nil {
		return nil, err
	}

	for _, fw := range firewalls {
		if fw.Name == name {
			return p.convertFirewall(&fw), nil
		}
	}

	return nil, nil
}

// convertCluster converts a Civo cluster to provider cluster
func (p *Provider) convertCluster(civoCluster *civogo.KubernetesCluster) *Cluster {
	return &Cluster{
		ID:         civoCluster.ID,
		Name:       civoCluster.Name,
		Status:     civoCluster.Status,
		FirewallID: civoCluster.FirewallID,
		MasterIP:   civoCluster.MasterIP,
		KubeConfig: civoCluster.KubeConfig,
		CreatedAt:  civoCluster.CreatedAt,
	}
}

// convertFirewall converts a Civo firewall to provider firewall
func (p *Provider) convertFirewall(civoFirewall *civogo.Firewall) *Firewall {
	var rules []FirewallRule
	for _, rule := range civoFirewall.Rules {
		rules = append(rules, FirewallRule{
			Protocol:  rule.Protocol,
			StartPort: rule.StartPort,
			EndPort:   rule.EndPort,
			Cidr:      rule.Cidr,
			Direction: rule.Direction,
		})
	}

	return &Firewall{
		ID:    civoFirewall.ID,
		Name:  civoFirewall.Name,
		Rules: rules,
	}
}
