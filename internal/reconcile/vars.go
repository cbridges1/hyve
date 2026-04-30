package reconcile

import (
	"context"
	"log"
	"os"

	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

// resolveHookEnvVars reads HYVE_* environment variables exported by a beforeCreate
// hook and writes any non-empty values back to the appropriate cluster definition fields.
// This allows hooks to set up resources and export their IDs/names so the reconciler
// can reference them during cluster creation.
func resolveHookEnvVars(_ context.Context, _ *state.Manager, clusterDef *types.ClusterDefinition) error {
	name := clusterDef.Metadata.Name

	switch clusterDef.Spec.Provider {
	case "aws":
		if v := os.Getenv("HYVE_VPC_ID"); v != "" && clusterDef.Spec.AWSVPCID == "" {
			clusterDef.Spec.AWSVPCID = v
			log.Printf("[%s] Read HYVE_VPC_ID=%s from hook", name, v)
		}
		if v := os.Getenv("HYVE_EKS_ROLE_NAME"); v != "" && clusterDef.Spec.AWSEKSRoleName == "" {
			clusterDef.Spec.AWSEKSRoleName = v
			log.Printf("[%s] Read HYVE_EKS_ROLE_NAME=%s from hook", name, v)
		}
		if v := os.Getenv("HYVE_NODE_ROLE_NAME"); v != "" && clusterDef.Spec.AWSNodeRoleName == "" {
			clusterDef.Spec.AWSNodeRoleName = v
			log.Printf("[%s] Read HYVE_NODE_ROLE_NAME=%s from hook", name, v)
		}
		// Full ARNs take priority over names so the reconciler never needs to construct
		// them from a role name + account ID (which would require the account ID to be known).
		if v := os.Getenv("HYVE_EKS_ROLE_ARN"); v != "" && clusterDef.Spec.AWSEKSRoleARN == "" {
			clusterDef.Spec.AWSEKSRoleARN = v
			log.Printf("[%s] Read HYVE_EKS_ROLE_ARN=%s from hook", name, v)
		}
		if v := os.Getenv("HYVE_NODE_ROLE_ARN"); v != "" && clusterDef.Spec.AWSNodeRoleARN == "" {
			clusterDef.Spec.AWSNodeRoleARN = v
			log.Printf("[%s] Read HYVE_NODE_ROLE_ARN=%s from hook", name, v)
		}

	case "azure":
		if v := os.Getenv("HYVE_RESOURCE_GROUP_NAME"); v != "" && clusterDef.Spec.AzureResourceGroup == "" {
			clusterDef.Spec.AzureResourceGroup = v
			log.Printf("[%s] Read HYVE_RESOURCE_GROUP_NAME=%s from hook", name, v)
		}
	}

	return nil
}
