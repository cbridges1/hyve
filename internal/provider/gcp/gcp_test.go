package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	container "google.golang.org/api/container/v1"
)

// newTestProvider returns a Provider with no real GCP service, suitable for
// testing pure logic methods that do not call the container API.
func newTestProvider(projectID, region string) *Provider {
	return &Provider{
		containerService: nil,
		computeService:   nil,
		projectID:        projectID,
		region:           region,
	}
}

// ── Name / Region ─────────────────────────────────────────────────────────────

func TestName(t *testing.T) {
	p := newTestProvider("my-project", "us-central1")
	assert.Equal(t, "gcp", p.Name())
}

func TestRegion(t *testing.T) {
	p := newTestProvider("my-project", "europe-west1")
	assert.Equal(t, "europe-west1", p.Region())
}

// ── getDefaultZone ────────────────────────────────────────────────────────────

// getDefaultZone queries the Compute API and falls back to "<region>-a" when
// the API is unavailable (e.g. in tests with a nil compute service).

func TestGetDefaultZone_FallbackWhenAPIUnavailable(t *testing.T) {
	regions := []string{"us-central1", "us-east1", "europe-west1", "asia-east1", "custom-region1"}
	ctx := context.Background()

	for _, region := range regions {
		t.Run(region, func(t *testing.T) {
			p := newTestProvider("proj", region)
			// nil computeService → API call fails → fallback to region+"-a"
			zone, err := p.getDefaultZone(ctx)
			require.NoError(t, err)
			assert.Equal(t, region+"-a", zone)
		})
	}
}

// ── clusterPath ───────────────────────────────────────────────────────────────

func TestClusterPath(t *testing.T) {
	ctx := context.Background()
	p := newTestProvider("my-proj", "us-central1")
	// nil computeService → fallback zone is "us-central1-a"
	got, err := p.clusterPath(ctx, "my-cluster")
	require.NoError(t, err)
	assert.Equal(t, "projects/my-proj/locations/us-central1-a/clusters/my-cluster", got)
}

// ── clusterPathRegional ───────────────────────────────────────────────────────

func TestClusterPathRegional(t *testing.T) {
	p := newTestProvider("my-proj", "europe-west2")
	got := p.clusterPathRegional("staging")
	assert.Equal(t, "projects/my-proj/locations/europe-west2/clusters/staging", got)
}

// ── parentPath ────────────────────────────────────────────────────────────────

func TestParentPath(t *testing.T) {
	p := newTestProvider("acme", "asia-northeast1")
	assert.Equal(t, "projects/acme/locations/asia-northeast1", p.parentPath())
}

// ── convertCluster ────────────────────────────────────────────────────────────

func TestConvertCluster_FieldMapping(t *testing.T) {
	p := newTestProvider("proj", "us-east1")
	gke := &container.Cluster{
		Name:     "prod",
		Status:   "RUNNING",
		Endpoint: "1.2.3.4",
		Location: "us-east1-b",
	}

	got := p.convertCluster(gke)

	require.NotNil(t, got)
	assert.Equal(t, "prod", got.ID)
	assert.Equal(t, "prod", got.Name)
	assert.Equal(t, "RUNNING", got.Status)
	assert.Equal(t, "1.2.3.4", got.MasterIP)
	assert.Equal(t, "us-east1-b", got.Location)
}

func TestConvertCluster_EmptyFields(t *testing.T) {
	p := newTestProvider("proj", "us-east1")
	gke := &container.Cluster{}

	got := p.convertCluster(gke)

	require.NotNil(t, got)
	assert.Empty(t, got.ID)
	assert.Empty(t, got.Name)
	assert.Empty(t, got.Status)
	assert.Empty(t, got.MasterIP)
	assert.Empty(t, got.Location)
}

// ── convertClusterWithLocation ────────────────────────────────────────────────

func TestConvertClusterWithLocation_FieldMapping(t *testing.T) {
	p := newTestProvider("proj", "us-central1")
	gke := &container.Cluster{
		Name:     "staging",
		Status:   "PROVISIONING",
		Endpoint: "5.6.7.8",
		Location: "us-central1-a",
	}

	got := p.convertClusterWithLocation(gke)

	require.NotNil(t, got)
	assert.Equal(t, "staging", got.Name)
	assert.Equal(t, "us-central1-a", got.Location)
	assert.Equal(t, "5.6.7.8", got.MasterIP)
}
