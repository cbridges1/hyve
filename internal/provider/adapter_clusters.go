package provider

import (
	"context"
	"log"

	"github.com/cbridges1/hyve/internal/provider/aws"
	"github.com/cbridges1/hyve/internal/provider/azure"
	"github.com/cbridges1/hyve/internal/provider/civo"
	"github.com/cbridges1/hyve/internal/provider/gcp"
)

// ListClusters lists all clusters
func (a *ProviderAdapter) ListClusters(ctx context.Context) ([]*Cluster, error) {
	if a.aws != nil {
		awsClusters, err := a.aws.ListClusters(ctx)
		if err != nil {
			return nil, err
		}
		var clusters []*Cluster
		for _, c := range awsClusters {
			clusters = append(clusters, convertAWSCluster(c))
		}
		return clusters, nil
	}
	if a.azure != nil {
		azureClusters, err := a.azure.ListClusters(ctx)
		if err != nil {
			return nil, err
		}
		var clusters []*Cluster
		for _, c := range azureClusters {
			clusters = append(clusters, convertAzureCluster(c))
		}
		return clusters, nil
	}
	if a.gcp != nil {
		gcpClusters, err := a.gcp.ListClusters(ctx)
		if err != nil {
			return nil, err
		}
		var clusters []*Cluster
		for _, c := range gcpClusters {
			clusters = append(clusters, convertGCPCluster(c))
		}
		return clusters, nil
	}

	civoClusters, err := a.civo.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	var clusters []*Cluster
	for _, c := range civoClusters {
		clusters = append(clusters, convertCivoCluster(c))
	}

	return clusters, nil
}

// GetCluster gets a cluster by ID
func (a *ProviderAdapter) GetCluster(ctx context.Context, clusterID string) (*Cluster, error) {
	if a.aws != nil {
		awsCluster, err := a.aws.GetCluster(ctx, clusterID)
		if err != nil {
			return nil, err
		}
		return convertAWSCluster(awsCluster), nil
	}
	if a.azure != nil {
		azureCluster, err := a.azure.GetCluster(ctx, clusterID)
		if err != nil {
			return nil, err
		}
		return convertAzureCluster(azureCluster), nil
	}
	if a.gcp != nil {
		gcpCluster, err := a.gcp.GetCluster(ctx, clusterID)
		if err != nil {
			return nil, err
		}
		return convertGCPCluster(gcpCluster), nil
	}

	civoCluster, err := a.civo.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	return convertCivoCluster(civoCluster), nil
}

// FindClusterByName finds a cluster by name
func (a *ProviderAdapter) FindClusterByName(ctx context.Context, name string) (*Cluster, error) {
	if a.aws != nil {
		awsCluster, err := a.aws.FindClusterByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if awsCluster == nil {
			return nil, nil
		}
		return convertAWSCluster(awsCluster), nil
	}
	if a.azure != nil {
		azureCluster, err := a.azure.FindClusterByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if azureCluster == nil {
			return nil, nil
		}
		return convertAzureCluster(azureCluster), nil
	}
	if a.gcp != nil {
		gcpCluster, err := a.gcp.FindClusterByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if gcpCluster == nil {
			return nil, nil
		}
		return convertGCPCluster(gcpCluster), nil
	}

	civoCluster, err := a.civo.FindClusterByName(ctx, name)
	if err != nil {
		return nil, err
	}

	if civoCluster == nil {
		return nil, nil
	}

	return convertCivoCluster(civoCluster), nil
}

// CreateCluster creates a new cluster
func (a *ProviderAdapter) CreateCluster(ctx context.Context, config *ClusterConfig) (*Cluster, error) {
	if a.aws != nil {
		awsConfig := &aws.ClusterConfig{
			Name:         config.Name,
			Region:       config.Region,
			Nodes:        config.Nodes,
			NodeGroups:   config.NodeGroups,
			ClusterType:  config.ClusterType,
			FirewallID:   config.FirewallID,
			Applications: config.Applications,
			// EKS-specific configuration
			RoleARN:     config.AWSRoleARN,
			NodeRoleARN: config.AWSNodeRoleARN,
			VPCID:       config.AWSVPCID,
			SubnetIDs:   config.AWSSubnetIDs,
		}
		log.Printf("Creating AWS cluster with configuration: %+v", awsConfig)
		awsCluster, err := a.aws.CreateCluster(ctx, awsConfig)
		if err != nil {
			return nil, err
		}
		return convertAWSCluster(awsCluster), nil
	}
	if a.azure != nil {
		azureConfig := &azure.ClusterConfig{
			Name:         config.Name,
			Region:       config.Region,
			Nodes:        config.Nodes,
			NodeGroups:   config.NodeGroups,
			ClusterType:  config.ClusterType,
			FirewallID:   config.FirewallID,
			Applications: config.Applications,
		}
		log.Printf("Creating Azure cluster with configuration: %+v", azureConfig)
		azureCluster, err := a.azure.CreateCluster(ctx, azureConfig)
		if err != nil {
			return nil, err
		}
		return convertAzureCluster(azureCluster), nil
	}
	if a.gcp != nil {
		gcpConfig := &gcp.ClusterConfig{
			Name:         config.Name,
			Region:       config.Region,
			Nodes:        config.Nodes,
			NodeGroups:   config.NodeGroups,
			ClusterType:  config.ClusterType,
			FirewallID:   config.FirewallID,
			Applications: config.Applications,
		}
		log.Printf("Creating GCP cluster with configuration: %+v", gcpConfig)
		gcpCluster, err := a.gcp.CreateCluster(ctx, gcpConfig)
		if err != nil {
			return nil, err
		}
		return convertGCPCluster(gcpCluster), nil
	}

	civoConfig := &civo.ClusterConfig{
		Name:         config.Name,
		Region:       config.Region,
		Nodes:        config.Nodes,
		NodeGroups:   config.NodeGroups,
		ClusterType:  config.ClusterType,
		FirewallID:   config.FirewallID,
		Applications: config.Applications,
	}

	log.Printf("Creating cluster with configuration: %+v", civoConfig)
	civoCluster, err := a.civo.CreateCluster(ctx, civoConfig)
	if err != nil {
		return nil, err
	}

	return convertCivoCluster(civoCluster), nil
}

// UpdateCluster updates an existing cluster
func (a *ProviderAdapter) UpdateCluster(ctx context.Context, clusterID string, config *ClusterUpdateConfig) (*Cluster, error) {
	if a.aws != nil {
		awsConfig := &aws.ClusterUpdateConfig{
			Name:       config.Name,
			Nodes:      config.Nodes,
			NodeGroups: config.NodeGroups,
		}
		awsCluster, err := a.aws.UpdateCluster(ctx, clusterID, awsConfig)
		if err != nil {
			return nil, err
		}
		return convertAWSCluster(awsCluster), nil
	}
	if a.azure != nil {
		azureConfig := &azure.ClusterUpdateConfig{
			Name:       config.Name,
			Nodes:      config.Nodes,
			NodeGroups: config.NodeGroups,
		}
		azureCluster, err := a.azure.UpdateCluster(ctx, clusterID, azureConfig)
		if err != nil {
			return nil, err
		}
		return convertAzureCluster(azureCluster), nil
	}
	if a.gcp != nil {
		gcpConfig := &gcp.ClusterUpdateConfig{
			Name:       config.Name,
			Nodes:      config.Nodes,
			NodeGroups: config.NodeGroups,
		}
		gcpCluster, err := a.gcp.UpdateCluster(ctx, clusterID, gcpConfig)
		if err != nil {
			return nil, err
		}
		return convertGCPCluster(gcpCluster), nil
	}

	civoConfig := &civo.ClusterUpdateConfig{
		Name:       config.Name,
		Nodes:      config.Nodes,
		NodeGroups: config.NodeGroups,
	}

	civoCluster, err := a.civo.UpdateCluster(ctx, clusterID, civoConfig)
	if err != nil {
		return nil, err
	}

	return convertCivoCluster(civoCluster), nil
}

// DeleteCluster deletes a cluster
func (a *ProviderAdapter) DeleteCluster(ctx context.Context, clusterID string) error {
	if a.aws != nil {
		return a.aws.DeleteCluster(ctx, clusterID)
	}
	if a.azure != nil {
		return a.azure.DeleteCluster(ctx, clusterID)
	}
	if a.gcp != nil {
		return a.gcp.DeleteCluster(ctx, clusterID)
	}
	return a.civo.DeleteCluster(ctx, clusterID)
}

// WaitForClusterReady waits for cluster to be ready
func (a *ProviderAdapter) WaitForClusterReady(ctx context.Context, clusterID string) error {
	if a.aws != nil {
		return a.aws.WaitForClusterReady(ctx, clusterID)
	}
	if a.azure != nil {
		return a.azure.WaitForClusterReady(ctx, clusterID)
	}
	if a.gcp != nil {
		return a.gcp.WaitForClusterReady(ctx, clusterID)
	}
	return a.civo.WaitForClusterReady(ctx, clusterID)
}

// GetClusterInfo gets cluster information for export
func (a *ProviderAdapter) GetClusterInfo(ctx context.Context, name string) (*ClusterInfo, error) {
	if a.aws != nil {
		info, err := a.aws.GetClusterInfo(ctx, name)
		if err != nil {
			return nil, err
		}
		return clusterInfoFrom(info.Name, info.IPAddress, info.AccessPort, info.Kubeconfig, info.Status, info.ID, info.NodeGroups), nil
	}
	if a.azure != nil {
		info, err := a.azure.GetClusterInfo(ctx, name)
		if err != nil {
			return nil, err
		}
		return clusterInfoFrom(info.Name, info.IPAddress, info.AccessPort, info.Kubeconfig, info.Status, info.ID, info.NodeGroups), nil
	}
	if a.gcp != nil {
		info, err := a.gcp.GetClusterInfo(ctx, name)
		if err != nil {
			return nil, err
		}
		return clusterInfoFrom(info.Name, info.IPAddress, info.AccessPort, info.Kubeconfig, info.Status, info.ID, info.NodeGroups), nil
	}

	info, err := a.civo.GetClusterInfo(ctx, name)
	if err != nil {
		return nil, err
	}

	return clusterInfoFrom(info.Name, info.IPAddress, info.AccessPort, info.Kubeconfig, info.Status, info.ID, info.NodeGroups), nil
}
