package reconcile

import (
	"context"
	"log"
	"os"

	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

// resolveHookEnvVars reads HYVE_* environment variables exported by a beforeCreate
// hook and writes any non-empty values back to the appropriate provider config YAML
// fields. This allows hooks to set up resources and export their names so the
// reconciler can reference them during cluster creation.
func resolveHookEnvVars(ctx context.Context, stateMgr *state.Manager, clusterDef *types.ClusterDefinition) error {
	pcMgr := providerconfig.NewManager(stateMgr.GetStateRoot())
	name := clusterDef.Metadata.Name

	switch clusterDef.Spec.Provider {
	case "aws":
		accountName := clusterDef.Spec.AWSAccount

		if v := os.Getenv("HYVE_VPC_ID"); v != "" && clusterDef.Spec.AWSVPCID == "" {
			clusterDef.Spec.AWSVPCID = v
			log.Printf("[%s] Read HYVE_VPC_ID=%s from hook", name, v)
		}
		if v := os.Getenv("HYVE_VPC_NAME"); v != "" && clusterDef.Spec.AWSVPCName == "" {
			clusterDef.Spec.AWSVPCName = v
			log.Printf("[%s] Read HYVE_VPC_NAME=%s from hook", name, v)
			if vpcID := os.Getenv("HYVE_VPC_ID"); vpcID != "" && accountName != "" {
				if err := pcMgr.AddAWSVPC(accountName, v, vpcID); err != nil {
					log.Printf("[%s] Warning: failed to write VPC to provider config: %v", name, err)
				}
			}
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
			subName := clusterDef.Spec.AzureSubscription
			location := os.Getenv("HYVE_RESOURCE_GROUP_LOCATION")
			if subName != "" && location != "" {
				if err := pcMgr.AddAzureResourceGroup(subName, v, location); err != nil {
					log.Printf("[%s] Warning: failed to write resource group to provider config: %v", name, err)
				}
			}
		}
	}

	return nil
}
