package aws

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestProvider returns a Provider with no real AWS clients, suitable for
// testing pure logic methods that do not call the AWS API.
func newTestProvider(region string) *Provider {
	return &Provider{
		eksClient: nil,
		ec2Client: nil,
		region:    region,
	}
}

// ── Name / Region ─────────────────────────────────────────────────────────────

func TestName(t *testing.T) {
	p := newTestProvider("us-east-1")
	assert.Equal(t, "aws", p.Name())
}

func TestRegion(t *testing.T) {
	p := newTestProvider("eu-west-2")
	assert.Equal(t, "eu-west-2", p.Region())
}

// ── validAWSRegions ───────────────────────────────────────────────────────────

func TestValidAWSRegions_KnownRegions(t *testing.T) {
	known := []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"eu-west-1", "eu-central-1", "ap-southeast-1",
		"ap-northeast-1", "sa-east-1",
	}
	for _, r := range known {
		assert.True(t, validAWSRegions[r], "expected %s to be a valid AWS region", r)
	}
}

func TestValidAWSRegions_UnknownRegion(t *testing.T) {
	assert.False(t, validAWSRegions["PHX1"])
	assert.False(t, validAWSRegions["LON1"])
	assert.False(t, validAWSRegions[""])
}

// ── convertCluster ────────────────────────────────────────────────────────────

func TestConvertCluster_AllFields(t *testing.T) {
	p := newTestProvider("us-east-1")
	ts := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)

	eksCluster := &ekstypes.Cluster{
		Name:      aws.String("prod"),
		Status:    ekstypes.ClusterStatusActive,
		Endpoint:  aws.String("https://api.example.com"),
		CreatedAt: &ts,
	}

	got := p.convertCluster(eksCluster)

	require.NotNil(t, got)
	assert.Equal(t, "prod", got.ID)
	assert.Equal(t, "prod", got.Name)
	assert.Equal(t, "ACTIVE", got.Status)
	assert.Equal(t, "https://api.example.com", got.MasterIP)
	assert.Equal(t, ts, got.CreatedAt)
}

func TestConvertCluster_NilPointerFields(t *testing.T) {
	p := newTestProvider("us-east-1")

	// Name, Endpoint, and CreatedAt are all nil
	eksCluster := &ekstypes.Cluster{
		Status: ekstypes.ClusterStatusCreating,
	}

	got := p.convertCluster(eksCluster)

	require.NotNil(t, got)
	assert.Empty(t, got.ID)
	assert.Empty(t, got.Name)
	assert.Equal(t, "CREATING", got.Status)
	assert.Empty(t, got.MasterIP)
	assert.True(t, got.CreatedAt.IsZero())
}

func TestConvertCluster_FailedStatus(t *testing.T) {
	p := newTestProvider("us-east-1")
	eksCluster := &ekstypes.Cluster{
		Name:   aws.String("broken"),
		Status: ekstypes.ClusterStatusFailed,
	}

	got := p.convertCluster(eksCluster)

	assert.Equal(t, "FAILED", got.Status)
}

// ── generateEKSKubeconfig ─────────────────────────────────────────────────────

func TestGenerateEKSKubeconfig_ContainsRequiredFields(t *testing.T) {
	p := newTestProvider("us-west-2")
	kubeconfig := p.generateEKSKubeconfig("my-cluster", "https://api.example.com", "base64cadata==")

	assert.Contains(t, kubeconfig, "apiVersion: v1")
	assert.Contains(t, kubeconfig, "kind: Config")
	assert.Contains(t, kubeconfig, "my-cluster")
	assert.Contains(t, kubeconfig, "https://api.example.com")
	assert.Contains(t, kubeconfig, "base64cadata==")
	assert.Contains(t, kubeconfig, "us-west-2")
	assert.Contains(t, kubeconfig, "aws")
	assert.Contains(t, kubeconfig, "eks")
	assert.Contains(t, kubeconfig, "get-token")
}

func TestGenerateEKSKubeconfig_ClusterNameUsedAsContext(t *testing.T) {
	p := newTestProvider("eu-central-1")
	kubeconfig := p.generateEKSKubeconfig("staging", "https://staging.example.com", "caXYZ")

	// The cluster name should appear as context and user entries
	assert.Contains(t, kubeconfig, "current-context: staging")
	assert.Contains(t, kubeconfig, "name: staging")
}

func TestGenerateEKSKubeconfig_RegionIncluded(t *testing.T) {
	p := newTestProvider("ap-southeast-1")
	kubeconfig := p.generateEKSKubeconfig("cluster", "https://endpoint", "ca")
	assert.Contains(t, kubeconfig, "ap-southeast-1")
}

// ── generateSubnetCIDRs ───────────────────────────────────────────────────────

func TestGenerateSubnetCIDRs_ValidCIDR(t *testing.T) {
	cidrs, err := generateSubnetCIDRs("10.0.0.0/16", 3)
	require.NoError(t, err)
	require.Len(t, cidrs, 3)
	assert.Equal(t, "10.0.1.0/24", cidrs[0])
	assert.Equal(t, "10.0.2.0/24", cidrs[1])
	assert.Equal(t, "10.0.3.0/24", cidrs[2])
}

func TestGenerateSubnetCIDRs_SingleSubnet(t *testing.T) {
	cidrs, err := generateSubnetCIDRs("172.16.0.0/16", 1)
	require.NoError(t, err)
	require.Len(t, cidrs, 1)
	assert.Equal(t, "172.16.1.0/24", cidrs[0])
}

func TestGenerateSubnetCIDRs_ZeroCount(t *testing.T) {
	cidrs, err := generateSubnetCIDRs("10.0.0.0/16", 0)
	require.NoError(t, err)
	assert.Empty(t, cidrs)
}

func TestGenerateSubnetCIDRs_InvalidCIDR_NoSlash(t *testing.T) {
	_, err := generateSubnetCIDRs("10.0.0.0", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid CIDR format")
}

func TestGenerateSubnetCIDRs_InvalidCIDR_BadIP(t *testing.T) {
	_, err := generateSubnetCIDRs("notanip/16", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid IP format")
}

// ── isClusterNotFoundError ────────────────────────────────────────────────────

func TestIsClusterNotFoundError_Nil(t *testing.T) {
	assert.False(t, isClusterNotFoundError(nil))
}

func TestIsClusterNotFoundError_ResourceNotFoundException(t *testing.T) {
	err := errors.New("ResourceNotFoundException: cluster not found")
	assert.True(t, isClusterNotFoundError(err))
}

func TestIsClusterNotFoundError_NoClusterFound(t *testing.T) {
	err := errors.New("No cluster found for the given name")
	assert.True(t, isClusterNotFoundError(err))
}

func TestIsClusterNotFoundError_ClusterNotFound(t *testing.T) {
	err := errors.New("cluster my-cluster not found")
	assert.True(t, isClusterNotFoundError(err))
}

func TestIsClusterNotFoundError_UnrelatedError(t *testing.T) {
	err := errors.New("access denied: insufficient permissions")
	assert.False(t, isClusterNotFoundError(err))
}

// ── isSecurityGroupDuplicateError ─────────────────────────────────────────────

func TestIsSecurityGroupDuplicateError_Nil(t *testing.T) {
	assert.False(t, isSecurityGroupDuplicateError(nil))
}

func TestIsSecurityGroupDuplicateError_DuplicateError(t *testing.T) {
	err := errors.New("InvalidGroup.Duplicate: security group already exists")
	assert.True(t, isSecurityGroupDuplicateError(err))
}

func TestIsSecurityGroupDuplicateError_UnrelatedError(t *testing.T) {
	err := errors.New("InvalidGroup.NotFound: security group not found")
	assert.False(t, isSecurityGroupDuplicateError(err))
}

func TestIsSecurityGroupDuplicateError_PartialMatch(t *testing.T) {
	// Must contain the exact substring "InvalidGroup.Duplicate"
	err := errors.New("some InvalidGroup.Duplicate occurrence in a long message")
	assert.True(t, isSecurityGroupDuplicateError(err))
}

// Sanity-check that the helper uses strings.Contains, not equality
func TestIsSecurityGroupDuplicateError_StringsContains(t *testing.T) {
	duplicate := "InvalidGroup.Duplicate"
	err := errors.New(strings.ToUpper(duplicate)) // "INVALIDGROUP.DUPLICATE" – should NOT match
	assert.False(t, isSecurityGroupDuplicateError(err))
}
