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

	case "azure":
		if v := os.Getenv("HYVE_RESOURCE_GROUP_NAME"); v != "" && clusterDef.Spec.AzureResourceGroup == "" {
			clusterDef.Spec.AzureResourceGroup = v
			log.Printf("[%s] Read HYVE_RESOURCE_GROUP_NAME=%s from hook", name, v)
		}
	}

	return nil
}
