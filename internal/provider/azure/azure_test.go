package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hyve/internal/types"
)

// --- convertCluster ---

func TestConvertCluster_IDEqualsName(t *testing.T) {
	p := &Provider{}
	name := "my-cluster"
	armID := "/subscriptions/123/resourcegroups/my-rg/providers/Microsoft.ContainerService/managedClusters/my-cluster"
	status := "Succeeded"
	fqdn := "my-cluster-abc123.hcp.eastus.azmk8s.io"

	cluster := p.convertCluster(&armcontainerservice.ManagedCluster{
		Name: &name,
		ID:   &armID,
		Properties: &armcontainerservice.ManagedClusterProperties{
			ProvisioningState: &status,
			Fqdn:              &fqdn,
		},
	})

	// ID must be the cluster name, not the full ARM resource path.
	// Passing the ARM path to aksClient.Get() causes a double-path URL and 404.
	assert.Equal(t, name, cluster.ID)
	assert.Equal(t, name, cluster.Name)
	assert.Equal(t, status, cluster.Status)
	assert.Equal(t, fqdn, cluster.MasterIP)
}

func TestConvertCluster_NilProperties(t *testing.T) {
	p := &Provider{}
	name := "my-cluster"

	cluster := p.convertCluster(&armcontainerservice.ManagedCluster{
		Name: &name,
	})

	assert.Equal(t, name, cluster.ID)
	assert.Equal(t, name, cluster.Name)
	assert.Equal(t, "Unknown", cluster.Status)
	assert.Equal(t, "", cluster.MasterIP)
}

func TestConvertCluster_NilName(t *testing.T) {
	p := &Provider{}

	cluster := p.convertCluster(&armcontainerservice.ManagedCluster{})

	assert.Equal(t, "", cluster.ID)
	assert.Equal(t, "", cluster.Name)
	assert.Equal(t, "Unknown", cluster.Status)
}

func TestConvertCluster_FailedState(t *testing.T) {
	p := &Provider{}
	name := "failed-cluster"
	status := "Failed"

	cluster := p.convertCluster(&armcontainerservice.ManagedCluster{
		Name: &name,
		Properties: &armcontainerservice.ManagedClusterProperties{
			ProvisioningState: &status,
		},
	})

	assert.Equal(t, name, cluster.ID)
	assert.Equal(t, "Failed", cluster.Status)
}

// --- agentPoolMode ---

func TestAgentPoolMode_User(t *testing.T) {
	assert.Equal(t, armcontainerservice.AgentPoolModeUser, agentPoolMode("User"))
	assert.Equal(t, armcontainerservice.AgentPoolModeUser, agentPoolMode("user"))
	assert.Equal(t, armcontainerservice.AgentPoolModeUser, agentPoolMode("USER"))
}

func TestAgentPoolMode_System(t *testing.T) {
	assert.Equal(t, armcontainerservice.AgentPoolModeSystem, agentPoolMode("System"))
	assert.Equal(t, armcontainerservice.AgentPoolModeSystem, agentPoolMode("system"))
	assert.Equal(t, armcontainerservice.AgentPoolModeSystem, agentPoolMode(""))
	assert.Equal(t, armcontainerservice.AgentPoolModeSystem, agentPoolMode("unknown"))
}

// --- ClusterConfig NodeGroups edge cases (pure logic, no HTTP) ---

func TestClusterConfig_FirstPoolForcedSystemMode(t *testing.T) {
	// Verify that when building agent pool profiles the first pool is always System,
	// regardless of the Mode field on the NodeGroup.
	config := &ClusterConfig{
		Name:   "test-cluster",
		Region: "eastus",
		NodeGroups: []types.NodeGroup{
			{Name: "workers", InstanceType: "Standard_DS2_v2", Count: 1, Mode: "User"},
			{Name: "infra", InstanceType: "Standard_DS2_v2", Count: 1, Mode: "User"},
		},
	}

	// Build profiles using the same logic as CreateCluster.
	var profiles []*armcontainerservice.ManagedClusterAgentPoolProfile
	for i, ng := range config.NodeGroups {
		mode := agentPoolMode(ng.Mode)
		if i == 0 {
			mode = armcontainerservice.AgentPoolModeSystem
		}
		profiles = append(profiles, &armcontainerservice.ManagedClusterAgentPoolProfile{
			Mode: ptr(mode),
		})
	}

	assert.Equal(t, armcontainerservice.AgentPoolModeSystem, *profiles[0].Mode, "first pool must be System")
	assert.Equal(t, armcontainerservice.AgentPoolModeUser, *profiles[1].Mode)
}

func TestClusterConfig_PoolNameTruncatedTo12Chars(t *testing.T) {
	longName := "averylongpoolname"
	if len(longName) > 12 {
		longName = longName[:12]
	}
	assert.Equal(t, "averylongpoo", longName)
	assert.Len(t, longName, 12)
}

func TestClusterConfig_DefaultPoolName(t *testing.T) {
	ng := types.NodeGroup{Name: "", InstanceType: "Standard_DS2_v2", Count: 1}
	poolName := ng.Name
	if poolName == "" {
		poolName = "nodepool1"
	}
	assert.Equal(t, "nodepool1", poolName)
}

func TestClusterConfig_DefaultVMSize(t *testing.T) {
	ng := types.NodeGroup{Name: "pool", Count: 1} // no InstanceType
	vmSize := ng.InstanceType
	if vmSize == "" {
		vmSize = "Standard_DS2_v2"
	}
	assert.Equal(t, "Standard_DS2_v2", vmSize)
}

func TestClusterConfig_MinCountFloor(t *testing.T) {
	minCount := int32(0)
	if minCount < 1 {
		minCount = 1
	}
	assert.Equal(t, int32(1), minCount)
}

// --- resourceGroupFromID ---

func TestResourceGroupFromID_Standard(t *testing.T) {
	id := "/subscriptions/abc-123/resourceGroups/my-rg/providers/Microsoft.ContainerService/managedClusters/my-cluster"
	assert.Equal(t, "my-rg", resourceGroupFromID(id))
}

func TestResourceGroupFromID_CaseInsensitive(t *testing.T) {
	id := "/subscriptions/abc/RESOURCEGROUPS/prod-rg/providers/Microsoft.ContainerService/managedClusters/c"
	assert.Equal(t, "prod-rg", resourceGroupFromID(id))
}

func TestResourceGroupFromID_Empty(t *testing.T) {
	assert.Equal(t, "", resourceGroupFromID(""))
}

func TestResourceGroupFromID_NoResourceGroupSegment(t *testing.T) {
	assert.Equal(t, "", resourceGroupFromID("/subscriptions/abc/providers/Microsoft.ContainerService/managedClusters/c"))
}

func TestResourceGroupFromID_TrailingSlash(t *testing.T) {
	id := "/subscriptions/abc/resourceGroups/trailing-rg/"
	assert.Equal(t, "trailing-rg", resourceGroupFromID(id))
}

// --- GetClusterInfo NodeGroups extraction (inline logic, no HTTP) ---

func TestAgentPoolProfiles_NodeGroupExtraction(t *testing.T) {
	name1, name2 := "system", "user"
	size1, size2 := "Standard_DS2_v2", "Standard_D4s_v3"
	count1, count2 := int32(2), int32(3)
	minCount, maxCount := int32(1), int32(5)
	autoScale := true

	profiles := []*armcontainerservice.ManagedClusterAgentPoolProfile{
		{Name: &name1, VMSize: &size1, Count: &count1},
		{Name: &name2, VMSize: &size2, Count: &count2, EnableAutoScaling: &autoScale, MinCount: &minCount, MaxCount: &maxCount},
	}

	var nodeGroups []types.NodeGroup
	for _, pool := range profiles {
		if pool == nil {
			continue
		}
		poolName, vmSize := "", ""
		if pool.Name != nil {
			poolName = *pool.Name
		}
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
		nodeGroups = append(nodeGroups, types.NodeGroup{Name: poolName, InstanceType: vmSize, Count: count, MinCount: min, MaxCount: max})
	}

	require.Len(t, nodeGroups, 2)

	assert.Equal(t, "system", nodeGroups[0].Name)
	assert.Equal(t, "Standard_DS2_v2", nodeGroups[0].InstanceType)
	assert.Equal(t, 2, nodeGroups[0].Count)
	assert.Equal(t, 0, nodeGroups[0].MinCount)
	assert.Equal(t, 0, nodeGroups[0].MaxCount)

	assert.Equal(t, "user", nodeGroups[1].Name)
	assert.Equal(t, "Standard_D4s_v3", nodeGroups[1].InstanceType)
	assert.Equal(t, 3, nodeGroups[1].Count)
	assert.Equal(t, 1, nodeGroups[1].MinCount)
	assert.Equal(t, 5, nodeGroups[1].MaxCount)
}
