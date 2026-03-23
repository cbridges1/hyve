package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/cbridges1/hyve/internal/provider/azure"
	"github.com/cbridges1/hyve/internal/provider/gcp"
)

// --- convertAzureCluster status normalisation ---

func TestConvertAzureCluster_SucceededBecomesActive(t *testing.T) {
	c := &azure.Cluster{ID: "my-cluster", Name: "my-cluster", Status: "Succeeded"}
	got := convertAzureCluster(c)
	assert.Equal(t, "ACTIVE", got.Status,
		"AKS ProvisioningState 'Succeeded' must be normalised to 'ACTIVE' so sync status checks pass")
}

func TestConvertAzureCluster_UpdatingPreserved(t *testing.T) {
	c := &azure.Cluster{ID: "c", Name: "c", Status: "Updating"}
	got := convertAzureCluster(c)
	assert.Equal(t, "Updating", got.Status, "non-Succeeded states should be preserved as-is")
}

func TestConvertAzureCluster_FailedPreserved(t *testing.T) {
	c := &azure.Cluster{ID: "c", Name: "c", Status: "Failed"}
	got := convertAzureCluster(c)
	assert.Equal(t, "Failed", got.Status)
}

func TestConvertAzureCluster_FieldsPassedThrough(t *testing.T) {
	c := &azure.Cluster{
		ID:       "my-cluster",
		Name:     "my-cluster",
		Status:   "Succeeded",
		MasterIP: "10.0.0.1",
	}
	got := convertAzureCluster(c)
	assert.Equal(t, "my-cluster", got.ID)
	assert.Equal(t, "my-cluster", got.Name)
	assert.Equal(t, "10.0.0.1", got.MasterIP)
}

// --- convertGCPCluster status normalisation ---

func TestConvertGCPCluster_RunningBecomesActive(t *testing.T) {
	c := &gcp.Cluster{ID: "my-cluster", Name: "my-cluster", Status: "RUNNING"}
	got := convertGCPCluster(c)
	assert.Equal(t, "ACTIVE", got.Status,
		"GKE status 'RUNNING' must be normalised to 'ACTIVE' so sync status checks pass")
}

func TestConvertGCPCluster_ProvisioningPreserved(t *testing.T) {
	c := &gcp.Cluster{ID: "c", Name: "c", Status: "PROVISIONING"}
	got := convertGCPCluster(c)
	assert.Equal(t, "PROVISIONING", got.Status, "non-RUNNING states should be preserved as-is")
}

func TestConvertGCPCluster_ErrorPreserved(t *testing.T) {
	c := &gcp.Cluster{ID: "c", Name: "c", Status: "ERROR"}
	got := convertGCPCluster(c)
	assert.Equal(t, "ERROR", got.Status)
}

func TestConvertGCPCluster_FieldsPassedThrough(t *testing.T) {
	ts := time.Now()
	c := &gcp.Cluster{
		ID:        "my-cluster",
		Name:      "my-cluster",
		Status:    "RUNNING",
		MasterIP:  "10.0.0.2",
		Location:  "us-central1",
		CreatedAt: ts,
	}
	got := convertGCPCluster(c)
	assert.Equal(t, "my-cluster", got.ID)
	assert.Equal(t, "my-cluster", got.Name)
	assert.Equal(t, "ACTIVE", got.Status)
	assert.Equal(t, "10.0.0.2", got.MasterIP)
	assert.Equal(t, ts, got.CreatedAt)
}
