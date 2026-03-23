package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/google/uuid"

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

// Provider implements the provider interfaces for Azure
type Provider struct {
	aksClient         *armcontainerservice.ManagedClustersClient
	subscriptionID    string
	resourceGroupName string
	region            string
}

// NewProvider creates a new Azure provider.
// When tenantID, clientID, and clientSecret are all non-empty, a service principal credential
// is used (suitable for CI/CD pipelines). Otherwise the DefaultAzureCredential chain is used,
// which covers az login, managed identity, and the standard AZURE_* environment variables.
func NewProvider(subscriptionID, resourceGroupName, region, tenantID, clientID, clientSecret string) (*Provider, error) {
	var clientFactory *armcontainerservice.ClientFactory
	var err error

	if tenantID != "" && clientID != "" && clientSecret != "" {
		spCred, spErr := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
		if spErr != nil {
			return nil, fmt.Errorf("failed to create Azure service principal credentials: %w", spErr)
		}
		clientFactory, err = armcontainerservice.NewClientFactory(subscriptionID, spCred, nil)
	} else {
		defaultCred, defErr := azidentity.NewDefaultAzureCredential(nil)
		if defErr != nil {
			return nil, fmt.Errorf("failed to create Azure credentials: %w", defErr)
		}
		clientFactory, err = armcontainerservice.NewClientFactory(subscriptionID, defaultCred, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client factory: %w", err)
	}

	return &Provider{
		aksClient:         clientFactory.NewManagedClustersClient(),
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroupName,
		region:            region,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "azure"
}

// Region returns the provider region
func (p *Provider) Region() string {
	return p.region
}

// ListClusters lists all clusters. When no resource group is configured it
// falls back to listing all AKS clusters in the subscription so that import
// and other discovery flows work without requiring a resource group.
func (p *Provider) ListClusters(ctx context.Context) ([]*Cluster, error) {
	type pageResult struct {
		Value []*armcontainerservice.ManagedCluster
	}

	var clusters []*Cluster

	if p.resourceGroupName != "" {
		pager := p.aksClient.NewListByResourceGroupPager(p.resourceGroupName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list AKS clusters: %w", err)
			}
			for _, cluster := range page.Value {
				clusters = append(clusters, p.convertCluster(cluster))
			}
		}
	} else {
		// No resource group — list all clusters in the subscription.
		pager := p.aksClient.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list AKS clusters: %w", err)
			}
			for _, cluster := range page.Value {
				clusters = append(clusters, p.convertCluster(cluster))
			}
		}
	}

	return clusters, nil
}

// GetCluster gets a cluster by ID (name in AKS)
func (p *Provider) GetCluster(ctx context.Context, clusterID string) (*Cluster, error) {
	resp, err := p.aksClient.Get(ctx, p.resourceGroupName, clusterID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS cluster: %w", err)
	}

	return p.convertCluster(&resp.ManagedCluster), nil
}

// FindClusterByName finds a cluster by name. It first tries a direct Get using
// the configured resource group (fast path). On any failure it falls through to
// a subscription-wide scan, which also handles the case where resourceGroupName
// is empty or points to the wrong resource group. When the cluster is found via
// the subscription-wide scan, p.resourceGroupName is updated from the cluster's
// ARM ID so that DeleteCluster / GetClusterInfo work without extra parameters.
func (p *Provider) FindClusterByName(ctx context.Context, name string) (*Cluster, error) {
	if p.resourceGroupName != "" {
		resp, err := p.aksClient.Get(ctx, p.resourceGroupName, name, nil)
		if err == nil {
			return p.convertCluster(&resp.ManagedCluster), nil
		}
		// Fast path failed — fall through to subscription-wide scan below.
	}

	// Scan all clusters in the subscription.
	pager := p.aksClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, nil
		}
		for _, c := range page.Value {
			if c.Name != nil && *c.Name == name {
				// Extract the resource group from the ARM resource ID so
				// DeleteCluster and GetClusterInfo work after this call.
				if c.ID != nil {
					p.resourceGroupName = resourceGroupFromID(*c.ID)
				}
				return p.convertCluster(c), nil
			}
		}
	}
	return nil, nil
}

// resourceGroupFromID parses the resource group name from an Azure ARM resource ID.
// Example ID: /subscriptions/{sub}/resourceGroups/{rg}/providers/.../clusters/{name}
func resourceGroupFromID(id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// agentPoolMode converts a mode string to the AKS enum value
func agentPoolMode(mode string) armcontainerservice.AgentPoolMode {
	if strings.EqualFold(mode, "User") {
		return armcontainerservice.AgentPoolModeUser
	}
	return armcontainerservice.AgentPoolModeSystem
}

// CreateCluster creates a new cluster
func (p *Provider) CreateCluster(ctx context.Context, config *ClusterConfig) (*Cluster, error) {
	log.Printf("Creating AKS cluster %s in region %s", config.Name, p.region)

	var agentPoolProfiles []*armcontainerservice.ManagedClusterAgentPoolProfile

	if len(config.NodeGroups) > 0 {
		// Multi-pool cluster from NodeGroups
		for i, ng := range config.NodeGroups {
			poolName := ng.Name
			if poolName == "" {
				poolName = fmt.Sprintf("nodepool%d", i+1)
			}
			// AKS pool names must be lowercase alphanumeric, max 12 chars
			if len(poolName) > 12 {
				poolName = poolName[:12]
			}
			vmSize := ng.InstanceType
			if vmSize == "" {
				vmSize = "Standard_DS2_v2"
			}
			count := int32(ng.Count)
			if count < 1 {
				count = 1
			}
			mode := agentPoolMode(ng.Mode)
			// First pool must be System mode
			if i == 0 {
				mode = armcontainerservice.AgentPoolModeSystem
			}
			profile := &armcontainerservice.ManagedClusterAgentPoolProfile{
				Name:   strPtr(poolName),
				Count:  &count,
				VMSize: &vmSize,
				Mode:   ptr(mode),
			}
			if ng.MinCount > 0 || ng.MaxCount > 0 {
				minCount := int32(ng.MinCount)
				if minCount < 1 {
					minCount = 1
				}
				maxCount := int32(ng.MaxCount)
				if maxCount < count {
					maxCount = count + 2
				}
				enableAutoScale := true
				profile.EnableAutoScaling = &enableAutoScale
				profile.MinCount = &minCount
				profile.MaxCount = &maxCount
			}
			if ng.DiskSize > 0 {
				diskSize := int32(ng.DiskSize)
				profile.OSDiskSizeGB = &diskSize
			}
			agentPoolProfiles = append(agentPoolProfiles, profile)
		}
		log.Printf("Creating AKS cluster with %d agent pool(s)", len(agentPoolProfiles))
	} else {
		// Legacy single pool from Nodes slice
		vmSize := "Standard_DS2_v2"
		nodeCount := int32(len(config.Nodes))
		if nodeCount == 0 {
			nodeCount = 1
		}
		if len(config.Nodes) > 0 {
			vmSize = config.Nodes[0]
		}
		agentPoolProfiles = []*armcontainerservice.ManagedClusterAgentPoolProfile{
			{
				Name:   strPtr("nodepool1"),
				Count:  &nodeCount,
				VMSize: &vmSize,
				Mode:   ptr(armcontainerservice.AgentPoolModeSystem),
			},
		}
	}

	parameters := armcontainerservice.ManagedCluster{
		Location: &p.region,
		Identity: &armcontainerservice.ManagedClusterIdentity{
			Type: ptr(armcontainerservice.ResourceIdentityTypeSystemAssigned),
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			DNSPrefix:         &config.Name,
			AgentPoolProfiles: agentPoolProfiles,
		},
	}

	poller, err := p.aksClient.BeginCreateOrUpdate(ctx, p.resourceGroupName, config.Name, parameters, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS cluster: %w", err)
	}

	log.Printf("AKS cluster creation started: %s", config.Name)

	// Don't wait for completion here - just return the initial state
	_ = poller // poller can be used to wait for completion if needed
	return &Cluster{
		ID:     config.Name,
		Name:   config.Name,
		Status: "Creating",
	}, nil
}

// UpdateCluster updates an existing cluster
func (p *Provider) UpdateCluster(ctx context.Context, clusterID string, config *ClusterUpdateConfig) (*Cluster, error) {
	// AKS cluster updates are complex - return current cluster
	return p.GetCluster(ctx, clusterID)
}

// DeleteCluster deletes a cluster
func (p *Provider) DeleteCluster(ctx context.Context, clusterID string) error {
	poller, err := p.aksClient.BeginDelete(ctx, p.resourceGroupName, clusterID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete AKS cluster: %w", err)
	}

	// Wait for deletion to complete
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for AKS cluster deletion: %w", err)
	}

	return nil
}

// WaitForClusterReady waits for cluster to be ready
func (p *Provider) WaitForClusterReady(ctx context.Context, clusterID string) error {
	for {
		resp, err := p.aksClient.Get(ctx, p.resourceGroupName, clusterID, nil)
		if err != nil {
			return fmt.Errorf("failed to get cluster status: %w", err)
		}

		state := "Unknown"
		if resp.Properties != nil && resp.Properties.ProvisioningState != nil {
			state = *resp.Properties.ProvisioningState
		}

		log.Printf("AKS cluster provisioning state: %s, waiting...", state)

		if state == "Succeeded" {
			break
		}

		if state == "Failed" {
			return fmt.Errorf("cluster creation failed")
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
	// Ensure resourceGroupName is resolved; FindClusterByName does a
	// subscription-wide scan and sets p.resourceGroupName when necessary.
	if p.resourceGroupName == "" {
		if _, err := p.FindClusterByName(ctx, name); err != nil || p.resourceGroupName == "" {
			return nil, fmt.Errorf("cluster %s not found in subscription", name)
		}
	}
	resp, err := p.aksClient.Get(ctx, p.resourceGroupName, name, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS cluster info: %w", err)
	}

	cluster := resp.ManagedCluster
	fqdn := ""
	if cluster.Properties != nil && cluster.Properties.Fqdn != nil {
		fqdn = *cluster.Properties.Fqdn
	}

	status := "Unknown"
	if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
		status = *cluster.Properties.ProvisioningState
	}

	clusterName := ""
	if cluster.Name != nil {
		clusterName = *cluster.Name
	}

	// Fetch admin kubeconfig
	kubeconfig := ""
	credResp, err := p.aksClient.ListClusterAdminCredentials(ctx, p.resourceGroupName, name, nil)
	if err != nil {
		log.Printf("Warning: failed to fetch admin credentials for cluster %s: %v", name, err)
	} else if len(credResp.Kubeconfigs) > 0 && credResp.Kubeconfigs[0].Value != nil {
		kubeconfig = string(credResp.Kubeconfigs[0].Value)
	}

	var nodeGroups []types.NodeGroup
	if cluster.Properties != nil {
		for _, pool := range cluster.Properties.AgentPoolProfiles {
			if pool == nil {
				continue
			}
			name := ""
			if pool.Name != nil {
				name = *pool.Name
			}
			vmSize := ""
			if pool.VMSize != nil {
				vmSize = *pool.VMSize
			}
			count, min, max := 0, 0, 0
			if pool.Count != nil {
				count = int(*pool.Count)
			}
			if pool.MinCount != nil {
				min = int(*pool.MinCount)
			}
			if pool.MaxCount != nil {
				max = int(*pool.MaxCount)
			}
			nodeGroups = append(nodeGroups, types.NodeGroup{
				Name:         name,
				InstanceType: vmSize,
				Count:        count,
				MinCount:     min,
				MaxCount:     max,
			})
		}
	}

	return &ClusterInfo{
		Name:       clusterName,
		IPAddress:  fqdn,
		AccessPort: "443",
		Kubeconfig: kubeconfig,
		Status:     status,
		ID:         clusterName,
		NodeGroups: nodeGroups,
	}, nil
}

// ListFirewalls lists all firewalls (NSGs in Azure)
func (p *Provider) ListFirewalls(ctx context.Context) ([]*Firewall, error) {
	// AKS manages NSGs automatically
	return []*Firewall{}, nil
}

// CreateFirewall creates a firewall (NSG in Azure)
func (p *Provider) CreateFirewall(ctx context.Context, config *FirewallConfig) (*Firewall, error) {
	// AKS creates NSGs automatically for clusters
	return &Firewall{
		ID:    config.Name,
		Name:  config.Name,
		Rules: config.Rules,
	}, nil
}

// DeleteFirewall deletes a firewall
func (p *Provider) DeleteFirewall(ctx context.Context, firewallID string) error {
	// AKS manages NSGs automatically
	return nil
}

// FindFirewallByName finds a firewall by name
func (p *Provider) FindFirewallByName(ctx context.Context, name string) (*Firewall, error) {
	// AKS manages NSGs automatically
	return nil, nil
}

// convertCluster converts an AKS cluster to provider cluster
func (p *Provider) convertCluster(aksCluster *armcontainerservice.ManagedCluster) *Cluster {
	name := ""
	if aksCluster.Name != nil {
		name = *aksCluster.Name
	}

	status := "Unknown"
	fqdn := ""
	if aksCluster.Properties != nil {
		if aksCluster.Properties.ProvisioningState != nil {
			status = *aksCluster.Properties.ProvisioningState
		}
		if aksCluster.Properties.Fqdn != nil {
			fqdn = *aksCluster.Properties.Fqdn
		}
	}

	return &Cluster{
		ID:        name,
		Name:      name,
		Status:    status,
		MasterIP:  fqdn,
		CreatedAt: time.Now(),
	}
}

func newResourceGroupsClient(subscriptionID, tenantID, clientID, clientSecret string) (*armresources.ResourceGroupsClient, error) {
	if tenantID != "" && clientID != "" && clientSecret != "" {
		spCred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure service principal credentials: %w", err)
		}
		return armresources.NewResourceGroupsClient(subscriptionID, spCred, nil)
	}

	defaultCred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure default credentials: %w", err)
	}
	return armresources.NewResourceGroupsClient(subscriptionID, defaultCred, nil)
}

// CreateResourceGroup creates an Azure resource group in the given subscription.
// When service principal credentials are provided it also assigns the Contributor
// role to that principal on the resource group so that CI/CD operations
// (cluster create / update / delete) can succeed without manual RBAC setup.
func CreateResourceGroup(ctx context.Context, subscriptionID, resourceGroupName, location, tenantID, clientID, clientSecret string) error {
	client, err := newResourceGroupsClient(subscriptionID, tenantID, clientID, clientSecret)
	if err != nil {
		return err
	}

	_, err = client.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Location: &location,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to create resource group '%s': %w", resourceGroupName, err)
	}

	log.Printf("Azure resource group '%s' created in '%s'", resourceGroupName, location)

	// Assign Contributor role to the service principal so it can manage AKS clusters.
	// Only applicable in service-principal (CI/CD) mode.
	if tenantID != "" && clientID != "" && clientSecret != "" {
		if err := assignContributorRole(ctx, subscriptionID, resourceGroupName, tenantID, clientID, clientSecret); err != nil {
			log.Printf("⚠️  Warning: Failed to assign Contributor role on resource group '%s': %v", resourceGroupName, err)
			log.Printf("   Grant the role manually: az role assignment create --assignee %s --role Contributor --scope /subscriptions/%s/resourceGroups/%s", clientID, subscriptionID, resourceGroupName)
		}
	}

	return nil
}

// assignContributorRole assigns the built-in Contributor role to the service
// principal on the given resource group scope.
func assignContributorRole(ctx context.Context, subscriptionID, resourceGroupName, tenantID, clientID, clientSecret string) error {
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	// Obtain an access token and extract the service principal Object ID from
	// the JWT oid claim — the role assignment API requires the Object ID, not
	// the Application (client) ID.
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("failed to obtain access token: %w", err)
	}

	objectID, err := extractOIDFromJWT(tok.Token)
	if err != nil {
		return fmt.Errorf("failed to extract service principal object ID from token: %w", err)
	}

	roleClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroupName)
	// Well-known built-in Contributor role definition ID.
	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c", subscriptionID)

	// Use a deterministic UUID (v5) so repeated calls are idempotent.
	assignmentName := uuid.NewSHA1(uuid.NameSpaceURL, []byte(scope+objectID)).String()

	spType := armauthorization.PrincipalTypeServicePrincipal
	_, err = roleClient.Create(ctx, scope, assignmentName, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: &roleDefinitionID,
			PrincipalID:      &objectID,
			PrincipalType:    &spType,
		},
	}, nil)
	if err != nil {
		// RoleAssignmentExists is not an error — idempotent.
		if strings.Contains(err.Error(), "RoleAssignmentExists") {
			log.Printf("Contributor role already assigned to service principal on resource group '%s'", resourceGroupName)
			return nil
		}
		return fmt.Errorf("failed to create role assignment: %w", err)
	}

	log.Printf("✅ Contributor role assigned to service principal on resource group '%s'", resourceGroupName)
	return nil
}

// extractOIDFromJWT decodes the payload of a JWT and returns the oid claim.
func extractOIDFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to base64-decode JWT payload: %w", err)
	}

	var claims struct {
		OID string `json:"oid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	if claims.OID == "" {
		return "", fmt.Errorf("oid claim missing from token")
	}

	return claims.OID, nil
}

// DeleteResourceGroup deletes an Azure resource group from the given subscription.
func DeleteResourceGroup(ctx context.Context, subscriptionID, resourceGroupName, tenantID, clientID, clientSecret string) error {
	client, err := newResourceGroupsClient(subscriptionID, tenantID, clientID, clientSecret)
	if err != nil {
		return err
	}

	poller, err := client.BeginDelete(ctx, resourceGroupName, nil)
	if err != nil {
		return fmt.Errorf("failed to delete resource group '%s': %w", resourceGroupName, err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("failed to wait for resource group deletion: %w", err)
	}

	log.Printf("Azure resource group '%s' deleted", resourceGroupName)
	return nil
}

// Helper functions
func strPtr(s string) *string {
	return &s
}

func ptr[T any](v T) *T {
	return &v
}
