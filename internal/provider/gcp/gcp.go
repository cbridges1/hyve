package gcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	container "google.golang.org/api/container/v1"
	"google.golang.org/api/option"

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
	Location   string // Zone or region where the cluster is located
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

// Provider implements the provider interfaces for GCP
type Provider struct {
	containerService *container.Service
	projectID        string
	region           string
}

// NewProvider creates a new GCP provider
func NewProvider(credentialsJSON, projectID, region string) (*Provider, error) {
	ctx := context.Background()

	var svc *container.Service
	var err error

	if credentialsJSON != "" {
		svc, err = container.NewService(ctx, option.WithCredentialsJSON([]byte(credentialsJSON)))
	} else {
		// Use default credentials (ADC)
		svc, err = container.NewService(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create GCP container service: %w", err)
	}

	return &Provider{
		containerService: svc,
		projectID:        projectID,
		region:           region,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "gcp"
}

// Region returns the provider region
func (p *Provider) Region() string {
	return p.region
}

// clusterPath returns the full path for a cluster
// Uses the zone for zonal clusters created by this provider
func (p *Provider) clusterPath(clusterName string) string {
	zone := p.getDefaultZone()
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.projectID, zone, clusterName)
}

// clusterPathRegional returns the full path for a cluster using region (for listing)
func (p *Provider) clusterPathRegional(clusterName string) string {
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.projectID, p.region, clusterName)
}

// parentPath returns the parent path for listing clusters (uses region to find all)
func (p *Provider) parentPath() string {
	return fmt.Sprintf("projects/%s/locations/%s", p.projectID, p.region)
}

// ListClusters lists all clusters
func (p *Provider) ListClusters(ctx context.Context) ([]*Cluster, error) {
	resp, err := p.containerService.Projects.Locations.Clusters.List(p.parentPath()).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list GKE clusters: %w", err)
	}

	var clusters []*Cluster
	for _, c := range resp.Clusters {
		clusters = append(clusters, p.convertCluster(c))
	}

	return clusters, nil
}

// GetCluster gets a cluster by ID (name in GKE)
func (p *Provider) GetCluster(ctx context.Context, clusterID string) (*Cluster, error) {
	// Try to find the cluster (handles both zonal and regional)
	cluster, err := p.FindClusterByName(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get GKE cluster: %w", err)
	}
	if cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", clusterID)
	}
	return cluster, nil
}

// FindClusterByName finds a cluster by name
func (p *Provider) FindClusterByName(ctx context.Context, name string) (*Cluster, error) {
	// When using the wildcard location "-", skip the zone-specific GET (which would
	// produce an invalid zone like "--b") and go straight to listing all clusters.
	if p.region != "-" {
		cluster, err := p.containerService.Projects.Locations.Clusters.Get(p.clusterPath(name)).Context(ctx).Do()
		if err == nil {
			return p.convertCluster(cluster), nil
		}
		if !strings.Contains(err.Error(), "notFound") && !strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("failed to find GKE cluster: %w", err)
		}
	}

	// List all clusters in the location (or all locations when region == "-") and find by name
	resp, listErr := p.containerService.Projects.Locations.Clusters.List(p.parentPath()).Context(ctx).Do()
	if listErr != nil {
		return nil, nil // Cluster not found
	}

	for _, c := range resp.Clusters {
		if c.Name == name {
			return p.convertClusterWithLocation(c), nil
		}
	}
	return nil, nil // Cluster not found
}

// CreateCluster creates a new cluster
func (p *Provider) CreateCluster(ctx context.Context, config *ClusterConfig) (*Cluster, error) {
	log.Printf("Creating GKE cluster %s in region %s", config.Name, p.region)

	// Create a zonal cluster for precise node count control
	zone := p.getDefaultZone()
	zonalPath := fmt.Sprintf("projects/%s/locations/%s", p.projectID, zone)

	var createReq *container.CreateClusterRequest

	if len(config.NodeGroups) > 0 {
		// Multi-pool cluster from NodeGroups
		var nodePools []*container.NodePool
		for _, ng := range config.NodeGroups {
			poolName := ng.Name
			if poolName == "" {
				poolName = "default-pool"
			}
			machineType := ng.InstanceType
			if machineType == "" {
				machineType = "e2-medium"
			}
			count := int64(ng.Count)
			if count < 1 {
				count = 1
			}
			pool := &container.NodePool{
				Name:             poolName,
				InitialNodeCount: count,
				Config: &container.NodeConfig{
					MachineType: machineType,
				},
			}
			if ng.Spot {
				pool.Config.Spot = true
			}
			if ng.DiskSize > 0 {
				pool.Config.DiskSizeGb = int64(ng.DiskSize)
			}
			if len(ng.Labels) > 0 {
				pool.Config.Labels = ng.Labels
			}
			if ng.MinCount > 0 || ng.MaxCount > 0 {
				minCount := int64(ng.MinCount)
				if minCount < 1 {
					minCount = 1
				}
				maxCount := int64(ng.MaxCount)
				if maxCount < count {
					maxCount = count + 2
				}
				pool.Autoscaling = &container.NodePoolAutoscaling{
					Enabled:      true,
					MinNodeCount: minCount,
					MaxNodeCount: maxCount,
				}
			}
			nodePools = append(nodePools, pool)
		}
		log.Printf("Creating GKE cluster with %d node pool(s)", len(nodePools))
		createReq = &container.CreateClusterRequest{
			Cluster: &container.Cluster{
				Name:      config.Name,
				NodePools: nodePools,
			},
		}
	} else {
		// Legacy single pool from Nodes slice
		machineType := "e2-medium"
		nodeCount := int64(len(config.Nodes))
		if nodeCount == 0 {
			nodeCount = 1
		}
		if len(config.Nodes) > 0 {
			machineType = config.Nodes[0]
		}
		log.Printf("Creating GKE cluster with %d nodes of type %s", nodeCount, machineType)
		createReq = &container.CreateClusterRequest{
			Cluster: &container.Cluster{
				Name:             config.Name,
				InitialNodeCount: nodeCount,
				NodeConfig: &container.NodeConfig{
					MachineType: machineType,
				},
			},
		}
	}

	op, err := p.containerService.Projects.Locations.Clusters.Create(zonalPath, createReq).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create GKE cluster: %w", err)
	}

	log.Printf("GKE cluster creation started in zone %s, operation: %s", zone, op.Name)

	return &Cluster{
		ID:     config.Name,
		Name:   config.Name,
		Status: "PROVISIONING",
	}, nil
}

// getDefaultZone returns a default zone for the region
// GCP zones don't follow a consistent pattern, so we use common mappings
func (p *Provider) getDefaultZone() string {
	// Common zone suffixes by region - using "-b" as it's most universally available
	zoneOverrides := map[string]string{
		"us-east1":             "us-east1-b",
		"us-east4":             "us-east4-a",
		"us-central1":          "us-central1-a",
		"us-west1":             "us-west1-a",
		"us-west2":             "us-west2-a",
		"us-west3":             "us-west3-a",
		"us-west4":             "us-west4-a",
		"europe-west1":         "europe-west1-b",
		"europe-west2":         "europe-west2-a",
		"europe-west3":         "europe-west3-a",
		"europe-west4":         "europe-west4-a",
		"europe-north1":        "europe-north1-a",
		"asia-east1":           "asia-east1-a",
		"asia-east2":           "asia-east2-a",
		"asia-northeast1":      "asia-northeast1-a",
		"asia-northeast2":      "asia-northeast2-a",
		"asia-northeast3":      "asia-northeast3-a",
		"asia-south1":          "asia-south1-a",
		"asia-southeast1":      "asia-southeast1-a",
		"asia-southeast2":      "asia-southeast2-a",
		"australia-southeast1": "australia-southeast1-a",
		"southamerica-east1":   "southamerica-east1-a",
	}

	if zone, ok := zoneOverrides[p.region]; ok {
		return zone
	}

	// Default: append "-b" as it's commonly available
	return p.region + "-b"
}

// UpdateCluster updates an existing cluster
func (p *Provider) UpdateCluster(ctx context.Context, clusterID string, config *ClusterUpdateConfig) (*Cluster, error) {
	// GKE cluster updates are complex - for now just return the current cluster
	// Real implementation would use SetNodePoolSize or UpdateCluster
	return p.GetCluster(ctx, clusterID)
}

// DeleteCluster deletes a cluster
func (p *Provider) DeleteCluster(ctx context.Context, clusterID string) error {
	// First find the cluster to get its actual location
	cluster, err := p.FindClusterByName(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("failed to find cluster for deletion: %w", err)
	}
	if cluster == nil {
		return fmt.Errorf("cluster %s not found", clusterID)
	}

	// Build the correct path using the cluster's actual location
	clusterPath := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.projectID, cluster.Location, clusterID)

	_, err = p.containerService.Projects.Locations.Clusters.Delete(clusterPath).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete GKE cluster: %w", err)
	}
	return nil
}

// WaitForClusterReady waits for cluster to be ready
func (p *Provider) WaitForClusterReady(ctx context.Context, clusterID string) error {
	// Use the default zone path for clusters we created
	zone := p.getDefaultZone()
	clusterPath := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.projectID, zone, clusterID)

	for {
		cluster, err := p.containerService.Projects.Locations.Clusters.Get(clusterPath).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get cluster status: %w", err)
		}

		log.Printf("GKE cluster status: %s, waiting...", cluster.Status)

		if cluster.Status == "RUNNING" {
			break
		}

		if cluster.Status == "ERROR" || cluster.Status == "DEGRADED" {
			return fmt.Errorf("cluster creation failed with status: %s", cluster.Status)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}

	return nil
}

// GetClusterInfo gets cluster information for export
func (p *Provider) GetClusterInfo(ctx context.Context, name string) (*ClusterInfo, error) {
	cluster, err := p.FindClusterByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get GKE cluster info: %w", err)
	}
	if cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", name)
	}

	// Fetch the raw GKE cluster to extract node pool details.
	// Use the cluster's actual location (set by convertClusterWithLocation) so
	// both zonal and regional clusters are found correctly.
	location := p.region
	if cluster.Location != "" {
		location = cluster.Location
	}
	clusterPath := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.projectID, location, name)
	rawCluster, rawErr := p.containerService.Projects.Locations.Clusters.Get(clusterPath).Context(ctx).Do()

	var nodeGroups []types.NodeGroup
	kubeconfig := ""
	if rawErr == nil && rawCluster != nil {
		for _, np := range rawCluster.NodePools {
			if np == nil {
				continue
			}
			instanceType := ""
			if np.Config != nil {
				instanceType = np.Config.MachineType
			}
			count := int(np.InitialNodeCount)
			min, max := 0, 0
			if np.Autoscaling != nil && np.Autoscaling.Enabled {
				min = int(np.Autoscaling.MinNodeCount)
				max = int(np.Autoscaling.MaxNodeCount)
			}
			nodeGroups = append(nodeGroups, types.NodeGroup{
				Name:         np.Name,
				InstanceType: instanceType,
				Count:        count,
				MinCount:     min,
				MaxCount:     max,
			})
		}

		if rawCluster.Endpoint != "" && rawCluster.MasterAuth != nil && rawCluster.MasterAuth.ClusterCaCertificate != "" {
			kubeconfig = p.generateGKEKubeconfig(name, rawCluster.Endpoint, rawCluster.MasterAuth.ClusterCaCertificate)
		}
	}

	return &ClusterInfo{
		Name:       cluster.Name,
		IPAddress:  cluster.MasterIP,
		AccessPort: "443",
		Kubeconfig: kubeconfig,
		Status:     cluster.Status,
		ID:         cluster.ID,
		NodeGroups: nodeGroups,
	}, nil
}

// generateGKEKubeconfig generates a kubeconfig for a GKE cluster using the
// gke-gcloud-auth-plugin exec credential plugin (analogous to aws eks get-token).
func (p *Provider) generateGKEKubeconfig(clusterName, endpoint, caData string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://%s
    certificate-authority-data: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: %s
  name: %s
current-context: %s
users:
- name: %s
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gke-gcloud-auth-plugin
      installHint: Install gke-gcloud-auth-plugin for use with kubectl by following
        https://cloud.google.com/blog/products/containers-kubernetes/kubectl-auth-changes-in-gke
      provideClusterInfo: true
`, endpoint, caData, clusterName, clusterName, clusterName, clusterName, clusterName, clusterName)
}

// ListFirewalls lists all firewalls (not directly supported in GKE context)
func (p *Provider) ListFirewalls(ctx context.Context) ([]*Firewall, error) {
	// GKE manages firewall rules automatically
	return []*Firewall{}, nil
}

// CreateFirewall creates a firewall (GKE manages this automatically)
func (p *Provider) CreateFirewall(ctx context.Context, config *FirewallConfig) (*Firewall, error) {
	// GKE creates firewall rules automatically for clusters
	return &Firewall{
		ID:    config.Name,
		Name:  config.Name,
		Rules: config.Rules,
	}, nil
}

// DeleteFirewall deletes a firewall
func (p *Provider) DeleteFirewall(ctx context.Context, firewallID string) error {
	// GKE manages firewall rules automatically
	return nil
}

// FindFirewallByName finds a firewall by name
func (p *Provider) FindFirewallByName(ctx context.Context, name string) (*Firewall, error) {
	// GKE manages firewall rules automatically
	return nil, nil
}

// convertCluster converts a GKE cluster to provider cluster
func (p *Provider) convertCluster(gkeCluster *container.Cluster) *Cluster {
	return &Cluster{
		ID:        gkeCluster.Name,
		Name:      gkeCluster.Name,
		Status:    gkeCluster.Status,
		MasterIP:  gkeCluster.Endpoint,
		Location:  gkeCluster.Location,
		CreatedAt: time.Now(), // GKE doesn't expose creation time in the same way
	}
}

// convertClusterWithLocation converts a GKE cluster and extracts location from self-link
func (p *Provider) convertClusterWithLocation(gkeCluster *container.Cluster) *Cluster {
	return &Cluster{
		ID:        gkeCluster.Name,
		Name:      gkeCluster.Name,
		Status:    gkeCluster.Status,
		MasterIP:  gkeCluster.Endpoint,
		Location:  gkeCluster.Location,
		CreatedAt: time.Now(),
	}
}
