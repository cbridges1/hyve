# Plan: Lifecycle Hooks & Provider Config Passive Sync

## Overview

1. **`beforeCreate` hook** — run workflows before a cluster is provisioned
2. **`afterDelete` hook** — run workflows after a cluster is destroyed
3. **Provider config passive sync** — remove fields from provider config YAML files if the referenced resource no longer exists in the cloud

**Design notes:**
- ARNs are never stored in config or cluster specs. Role ARNs are resolved internally at runtime via name lookup.
- If resource fields (`awsVpcName`, `awsVpcId`, `awsEksRoleName`, `awsNodeRoleName`, `azureResourceGroup`) are absent from the cluster YAML, the reconciler assumes the `beforeCreate` hook will export their details as the corresponding environment variables (`HYVE_VPC_NAME`, `HYVE_VPC_ID`, `HYVE_EKS_ROLE_NAME`, `HYVE_NODE_ROLE_NAME`, `HYVE_RESOURCE_GROUP_NAME`). After `beforeCreate` completes, the reconciler reads those env vars, writes the resolved values back to the appropriate provider config YAML, and then proceeds to create the cluster.
- Provider config files reference pre-existing resources by name. Hyve never creates or deletes these resources — it only removes stale field entries when the referenced resource no longer exists in the cloud.

---

## Provider Config File Structure

```
provider-configs/
  aws/
    prod-account.yaml
    dev-account.yaml
  gcp/
    analytics-project.yaml
  azure/
    prod-subscription.yaml
  civo/
    default.yaml
```

One file per account/project/subscription. Each file references pre-existing resources (VPCs, resource groups, IAM roles) by name. Hyve never creates or deletes these resources — it only removes individual field entries from the YAML when the referenced resource no longer exists in the cloud.

---

## Hook Order

**Create:**
```
[beforeCreate]
    ↓ (hook exports HYVE_* env vars for any resources it has set up)
write exported resource values to provider config YAML, commit, push
    ↓
resolve env var references in cluster spec
    ↓
create cluster
    ↓
[onCreated]
```

**Delete:**
```
[onDestroy]
    ↓
delete cluster
    ↓
[afterDelete]
    ↓
check provider config YAML fields against cloud — remove any that no longer exist, commit, push
    ↓
remove cluster YAML, commit, push
```

---

## Exported Environment Variables

These variables are exported by the `beforeCreate` hook and read by the reconciler after the hook completes. The reconciler writes any resolved values back to the provider config YAML before proceeding to create the cluster.

| Variable | Description |
|---|---|
| `HYVE_VPC_ID` | AWS VPC ID |
| `HYVE_VPC_NAME` | VPC Name tag |
| `HYVE_VPC_CIDR` | VPC CIDR block |
| `HYVE_SUBNET_IDS` | Comma-separated list of all subnet IDs |
| `HYVE_PRIVATE_SUBNET_IDS` | Comma-separated private subnet IDs |
| `HYVE_PUBLIC_SUBNET_IDS` | Comma-separated public subnet IDs |
| `HYVE_EKS_ROLE_NAME` | IAM role name for the EKS control plane |
| `HYVE_NODE_ROLE_NAME` | IAM role name for EKS node groups |
| `HYVE_RESOURCE_GROUP_NAME` | Azure resource group name |
| `HYVE_RESOURCE_GROUP_ID` | Full Azure resource ID for the group |
| `HYVE_RESOURCE_GROUP_LOCATION` | Azure region/location |

---

## Type Changes — `internal/types/types.go`

Extend `WorkflowsSpec`:

```go
type WorkflowsSpec struct {
    BeforeCreate []string `yaml:"beforeCreate,omitempty"`
    OnCreated    []string `yaml:"onCreated,omitempty"`
    OnDestroy    []string `yaml:"onDestroy,omitempty"`
    AfterDelete  []string `yaml:"afterDelete,omitempty"`
}
```

Add to `ClusterSpec`:

```go
AWSVPCId       string `yaml:"awsVpcId,omitempty"`
AWSEKSRoleName string `yaml:"awsEksRoleName,omitempty"`
AWSNodeRoleName string `yaml:"awsNodeRoleName,omitempty"`
```

Remove `AWSEKSRoleArn` and `AWSNodeRoleArn` from `ClusterSpec`.

---

## Cluster YAML Examples

### EKS — fully implicit

No VPC or role fields in YAML. The `beforeCreate` hook is expected to set up any required resources and export the corresponding `HYVE_*` env vars; the reconciler reads them and writes the resolved values to the provider config YAML before creating the cluster.

```yaml
apiVersion: v1
kind: Cluster
metadata:
  name: prod-eks
spec:
  provider: aws
  awsAccount: prod
  region: us-east-1
  clusterType: eks
  nodeGroups:
    - name: workers
      instanceType: t3.medium
      count: 3
  workflows:
    beforeCreate:
      - create-iam-roles
    onCreated:
      - deploy-platform-addons
    onDestroy:
      - drain-workloads
    afterDelete:
      - destroy-iam-roles
```

### EKS — explicit VPC ID and role names

```yaml
apiVersion: v1
kind: Cluster
metadata:
  name: staging-eks
spec:
  provider: aws
  awsAccount: prod
  region: us-east-1
  clusterType: eks
  awsVpcId: vpc-0abc123456789
  awsEksRoleName: my-eks-role
  awsNodeRoleName: my-node-role
  nodeGroups:
    - name: workers
      instanceType: t3.small
      count: 2
```

### AKS — resource group supplied via hook

The `beforeCreate` hook exports `HYVE_RESOURCE_GROUP_NAME`; the reconciler writes it to the provider config YAML before creating the cluster.

```yaml
apiVersion: v1
kind: Cluster
metadata:
  name: prod-aks
spec:
  provider: azure
  azureSubscription: prod-subscription
  region: eastus
  clusterType: aks
  nodeGroups:
    - name: workers
      instanceType: Standard_D2s_v3
      count: 3
  workflows:
    beforeCreate:
      - create-resource-group
    afterDelete:
      - destroy-resource-group
```

### Template with hook-based role creation

```yaml
apiVersion: v1
kind: Template
metadata:
  name: eks-ephemeral
spec:
  provider: aws
  region: us-east-1
  clusterType: eks
  nodeGroups:
    - name: workers
      instanceType: t3.medium
      count: 2
  workflows:
    beforeCreate:
      - create-iam-roles
    onCreated:
      - bootstrap-cluster
    onDestroy:
      - cleanup-namespaces
    afterDelete:
      - destroy-iam-roles
```

---

## CLI Changes

### New flags — `hyve cluster add` / `hyve cluster modify`

```
--before-create stringArray         Workflows to run before cluster creation
--after-delete  stringArray         Workflows to run after cluster deletion
--eks-role-name string              IAM role name for the EKS control plane
--node-role-name string             IAM role name for EKS node groups
```

`--eks-role-arn` and `--node-role-arn` are removed.

---

## File Change Summary

| File | Change |
|---|---|
| `internal/types/types.go` | Add `BeforeCreate`, `AfterDelete` to `WorkflowsSpec`; add `AWSVPCId`, `AWSEKSRoleName`, `AWSNodeRoleName` to `ClusterSpec`; remove ARN fields |
| `internal/cluster/provider.go` | Add role name → ARN lookup |
| `internal/cluster/aws/provider.go` | Implement role name lookup |
| `internal/cluster/azure/provider.go` | No new provider methods required |
| `internal/cluster/civo/provider.go` | No new provider methods required |
| `internal/cluster/gcp/provider.go` | No new provider methods required |
| `internal/reconcile/manager.go` | Add `beforeCreate` hook execution and env var write-back before `ActionCreate`; add `afterDelete` hook execution and passive sync after `ActionDelete` |
| `internal/reconcile/vars.go` | New: `resolveSpecVars`, reads `HYVE_*` env vars and writes to provider config YAML |
| `internal/template/types.go` | Add `BeforeCreate`, `AfterDelete` to `TemplateWorkflowsSpec`; add `AWSVPCId`, `AWSEKSRoleName`, `AWSNodeRoleName` to `TemplateSpec`; remove ARN fields |
| `cmd/cluster/cmd.go` | Add `--before-create`, `--after-delete`, `--eks-role-name`, `--node-role-name` flags; remove ARN flags |
| `cmd/cluster/crud.go` | Pass new fields through add/modify; update list display |
| `cmd/cluster/interactive.go` | Add TUI steps for new fields |
| `cmd/template/cmd.go` | Mirror new flags |
| `cmd/template/interactive.go` | Mirror TUI additions |
