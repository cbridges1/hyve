package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

// makeStateManager creates a temporary state directory and returns a Manager
// whose GetStateRoot() points to the root (parent of clusters/).
func makeStateManager(t *testing.T) (*state.Manager, string) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "provider-configs"), 0755))
	return state.NewManagerFromPath(stateDir), root
}

// ── AWS env var resolution ────────────────────────────────────────────────────

func TestResolveHookEnvVars_AWS_VPCIDSet(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_VPC_ID", "vpc-abc123")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "vpc-abc123", def.Spec.AWSVPCID)
}

func TestResolveHookEnvVars_AWS_VPCIDNotOverwritten(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_VPC_ID", "vpc-new")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws", AWSVPCID: "vpc-existing"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "vpc-existing", def.Spec.AWSVPCID, "existing VPC ID must not be overwritten")
}

func TestResolveHookEnvVars_AWS_EKSRoleNameSet(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_EKS_ROLE_NAME", "eks-control-plane-role")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "eks-control-plane-role", def.Spec.AWSEKSRoleName)
}

func TestResolveHookEnvVars_AWS_EKSRoleNameNotOverwritten(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_EKS_ROLE_NAME", "hook-role")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws", AWSEKSRoleName: "existing-role"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "existing-role", def.Spec.AWSEKSRoleName)
}

func TestResolveHookEnvVars_AWS_NodeRoleNameSet(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_NODE_ROLE_NAME", "eks-worker-role")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "eks-worker-role", def.Spec.AWSNodeRoleName)
}

func TestResolveHookEnvVars_AWS_NodeRoleNameNotOverwritten(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_NODE_ROLE_NAME", "hook-node-role")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws", AWSNodeRoleName: "existing-node-role"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "existing-node-role", def.Spec.AWSNodeRoleName)
}

func TestResolveHookEnvVars_AWS_AllVarsEmpty(t *testing.T) {
	mgr, _ := makeStateManager(t)

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "aws"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Empty(t, def.Spec.AWSVPCID)
	assert.Empty(t, def.Spec.AWSEKSRoleName)
	assert.Empty(t, def.Spec.AWSNodeRoleName)
}

// ── Azure env var resolution ──────────────────────────────────────────────────

func TestResolveHookEnvVars_Azure_ResourceGroupSet(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_RESOURCE_GROUP_NAME", "my-rg")
	t.Setenv("HYVE_RESOURCE_GROUP_LOCATION", "eastus")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "azure", AzureSubscription: "my-sub"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "my-rg", def.Spec.AzureResourceGroup)
}

func TestResolveHookEnvVars_Azure_ResourceGroupNotOverwritten(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_RESOURCE_GROUP_NAME", "hook-rg")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec: types.ClusterSpec{
			Provider:           "azure",
			AzureResourceGroup: "existing-rg",
		},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Equal(t, "existing-rg", def.Spec.AzureResourceGroup)
}

func TestResolveHookEnvVars_Azure_Noop_WhenEnvEmpty(t *testing.T) {
	mgr, _ := makeStateManager(t)

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "azure"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Empty(t, def.Spec.AzureResourceGroup)
}

// ── Non-AWS/Azure providers are no-ops ───────────────────────────────────────

func TestResolveHookEnvVars_Civo_Noop(t *testing.T) {
	mgr, _ := makeStateManager(t)
	t.Setenv("HYVE_VPC_ID", "should-not-matter")

	def := &types.ClusterDefinition{
		Metadata: types.ClusterMetadata{Name: "test-cluster"},
		Spec:     types.ClusterSpec{Provider: "civo"},
	}
	require.NoError(t, resolveHookEnvVars(context.Background(), mgr, def))
	assert.Empty(t, def.Spec.AWSVPCID)
}
