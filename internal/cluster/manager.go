package cluster

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/types"
)

// Manager handles cluster operations using a generic provider
type Manager struct {
	provider provider.ClusterProvider
}

// NewManager creates a new cluster manager
func NewManager(p provider.Provider) *Manager {
	return &Manager{
		provider: p,
	}
}

// DetermineAction decides what action to take based on desired vs actual state.
func (m *Manager) DetermineAction(ctx context.Context, desired types.ClusterDefinition) types.ReconcileAction {
	if desired.Spec.Delete {
		log.Printf("Cluster %s is marked for deletion (spec.delete: true)", desired.Metadata.Name)
		return types.ActionDelete
	}

	cluster, err := m.provider.FindClusterByName(ctx, desired.Metadata.Name)
	if err != nil {
		log.Printf("Error checking for existing cluster %s: %v", desired.Metadata.Name, err)
		return types.ActionCreate
	}

	if cluster == nil {
		log.Printf("Cluster %s not found, will create", desired.Metadata.Name)
		return types.ActionCreate
	}

	log.Printf("Found existing cluster %s with ID %s", desired.Metadata.Name, cluster.ID)

	if cluster.Status == "ACTIVE" {
		log.Printf("Cluster %s is up to date", desired.Metadata.Name)
		return types.ActionNone
	}

	if cluster.Status == "FAILED" {
		log.Printf("Cluster %s is in failed state, will recreate", desired.Metadata.Name)
		return types.ActionCreate
	}

	log.Printf("Cluster %s is in %s state, no action needed", desired.Metadata.Name, cluster.Status)
	return types.ActionNone
}

// FindByName finds a cluster by name
func (m *Manager) FindByName(ctx context.Context, name string) (*provider.Cluster, error) {
	return m.provider.FindClusterByName(ctx, name)
}

// Create creates a new cluster
func (m *Manager) Create(ctx context.Context, clusterDef types.ClusterDefinition) (*provider.Cluster, error) {
	config := &provider.ClusterConfig{
		Name:        clusterDef.Metadata.Name,
		Region:      clusterDef.Metadata.Region,
		Nodes:       clusterDef.Spec.Nodes,
		NodeGroups:  clusterDef.Spec.NodeGroups,
		ClusterType: clusterDef.Spec.ClusterType,
		// AWS-specific configuration
		AWSRoleARN:     clusterDef.Spec.AWSEKSRoleARN,
		AWSNodeRoleARN: clusterDef.Spec.AWSNodeRoleARN,
		AWSVPCID:       clusterDef.Spec.AWSVPCID,
	}

	return m.provider.CreateCluster(ctx, config)
}

// Update updates an existing cluster
func (m *Manager) Update(ctx context.Context, clusterDef types.ClusterDefinition) error {
	cluster, err := m.provider.FindClusterByName(ctx, clusterDef.Metadata.Name)
	if err != nil {
		return err
	}

	if cluster == nil {
		return fmt.Errorf("cluster %s not found for update", clusterDef.Metadata.Name)
	}

	config := &provider.ClusterUpdateConfig{
		Name:       clusterDef.Metadata.Name,
		Nodes:      clusterDef.Spec.Nodes,
		NodeGroups: clusterDef.Spec.NodeGroups,
	}

	_, err = m.provider.UpdateCluster(ctx, cluster.ID, config)
	return err
}

// Delete deletes a cluster
func (m *Manager) Delete(ctx context.Context, clusterID string) error {
	return m.provider.DeleteCluster(ctx, clusterID)
}

// WaitForReady waits for a cluster to be ready
func (m *Manager) WaitForReady(ctx context.Context, clusterID string) error {
	return m.provider.WaitForClusterReady(ctx, clusterID)
}

// FindOrphaned finds clusters that exist in the cloud but are not in the desired state.
// Only clusters whose names match the managed prefixes (hyve-, civo-deploy-) are considered.
func (m *Manager) FindOrphaned(ctx context.Context, desiredClusters []types.ClusterDefinition) ([]*provider.Cluster, error) {
	allClusters, err := m.provider.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	desiredNames := make(map[string]bool)
	for _, desired := range desiredClusters {
		desiredNames[desired.Metadata.Name] = true
	}

	var orphaned []*provider.Cluster
	for _, cluster := range allClusters {
		if m.ShouldManage(*cluster) && !desiredNames[cluster.Name] {
			orphaned = append(orphaned, cluster)
		}
	}

	return orphaned, nil
}

// StrictDeleteOrphans lists every cluster from the provider and deletes any whose name
// is not present in desiredClusters. Unlike FindOrphaned, no name-prefix filter is applied:
// every cloud cluster not represented in the repository state is removed.
func (m *Manager) StrictDeleteOrphans(ctx context.Context, desiredClusters []types.ClusterDefinition) error {
	allClusters, err := m.provider.ListClusters(ctx)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	desiredNames := make(map[string]bool)
	for _, d := range desiredClusters {
		desiredNames[d.Metadata.Name] = true
	}

	for _, c := range allClusters {
		if desiredNames[c.Name] {
			continue
		}
		log.Printf("strict-delete: deleting orphaned cluster %s (ID: %s)", c.Name, c.ID)
		if err := m.provider.DeleteCluster(ctx, c.ID); err != nil {
			log.Printf("strict-delete: failed to delete cluster %s: %v", c.Name, err)
		} else {
			log.Printf("strict-delete: deleted cluster %s", c.Name)
		}
	}

	return nil
}

// ShouldManage determines if we should manage this cluster
func (m *Manager) ShouldManage(cluster provider.Cluster) bool {
	managedPrefixes := []string{"hyve-", "civo-deploy-"}

	for _, prefix := range managedPrefixes {
		if strings.HasPrefix(cluster.Name, prefix) {
			return true
		}
	}

	return false
}

// GetClusterInfo gets cluster information for export
func (m *Manager) GetClusterInfo(ctx context.Context, clusterName string) (*provider.ClusterInfo, error) {
	return m.provider.GetClusterInfo(ctx, clusterName)
}

// CleanupOrphaned removes orphaned clusters
func (m *Manager) CleanupOrphaned(ctx context.Context, orphanedClusters []*provider.Cluster) error {
	for _, cluster := range orphanedClusters {
		log.Printf("Cleaning up orphaned cluster: %s (ID: %s)", cluster.Name, cluster.ID)

		err := m.provider.DeleteCluster(ctx, cluster.ID)
		if err != nil {
			log.Printf("Failed to delete orphaned cluster %s: %v", cluster.Name, err)
			continue
		}

		log.Printf("Successfully deleted orphaned cluster: %s", cluster.Name)
	}

	return nil
}
