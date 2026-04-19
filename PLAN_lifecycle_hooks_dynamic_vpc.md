# Plan: Lifecycle Hooks & Dynamic VPC for EKS

## Overview

This plan covers three related features:

1. **`beforeCreate` hook** — run workflows before a cluster is provisioned
2. **`afterDelete` hook** — run workflows after a cluster is destroyed
3. **Dynamic VPC** — automatically create and destroy an AWS VPC around the EKS cluster lifecycle, with VPC details exported as environment variables available to `beforeCreate` and subsequent hooks

---

## 1. New Lifecycle Hooks

### 1.1 Current Hook Order

```
[onDestroy]  →  delete cluster  →  remove YAML file
create cluster  →  [onCreated]
```

### 1.2 New Hook Order

```
create VPC (if dynamic)
    ↓
[beforeCreate]   ← receives HYVE_VPC_* env vars
    ↓
resolve ${VAR} references in spec
    ↓
create cluster
    ↓
[onCreated]
    ↓
    · · · cluster runs · · ·
    ↓
[onDestroy]
    ↓
delete cluster
    ↓
[afterDelete]
    ↓
delete VPC (if dynamic)
```

### 1.3 Type Changes — `internal/types/types.go`

Extend `WorkflowsSpec`:

```go
type WorkflowsSpec struct {
    BeforeCreate []string `yaml:"beforeCreate,omitempty"` // NEW: run before cluster is created
    OnCreated    []string `yaml:"onCreated,omitempty"`
    OnDestroy    []string `yaml:"onDestroy,omitempty"`
    AfterDelete  []string `yaml:"afterDelete,omitempty"`  // NEW: run after cluster is deleted
}
```

Add dynamic VPC spec to `ClusterSpec`:

```go
type DynamicVPCSpec struct {
    Enabled bool   `yaml:"enabled"`
    CIDR    string `yaml:"cidr,omitempty"`   // defaults to 10.0.0.0/16
    Name    string `yaml:"name,omitempty"`   // auto-generated as <cluster-name>-vpc if blank
}

// Inside ClusterSpec:
DynamicVPC DynamicVPCSpec `yaml:"dynamicVpc,omitempty"`
```

---

## 2. Dynamic VPC

### 2.1 Concept

When `spec.dynamicVpc.enabled: true` is set on an EKS cluster, Hyve:

1. Creates an AWS VPC (and associated subnets, internet gateway, route tables) **before** any hooks or cluster provisioning
2. Exports the resulting identifiers as environment variables available to `beforeCreate` workflows
3. Stores the VPC ID in the cluster YAML (written back to the state repo) so it survives across reconcile runs
4. Destroys the VPC **after** `afterDelete` workflows complete when the cluster is deleted

### 2.2 Exported Environment Variables

All variables below are set in the process environment before `beforeCreate` workflows run and are also available to `onCreated`, `onDestroy`, and `afterDelete`.

| Variable | Description |
|---|---|
| `HYVE_VPC_ID` | AWS VPC ID (`vpc-xxxxxxxxxxxxxxxxx`) |
| `HYVE_VPC_ARN` | AWS VPC ARN (`arn:aws:ec2:region:account:vpc/vpc-xxx`) |
| `HYVE_VPC_NAME` | Name tag of the created VPC |
| `HYVE_VPC_CIDR` | CIDR block of the VPC (e.g. `10.0.0.0/16`) |
| `HYVE_SUBNET_IDS` | Comma-separated list of created subnet IDs |
| `HYVE_PRIVATE_SUBNET_IDS` | Comma-separated private subnet IDs |
| `HYVE_PUBLIC_SUBNET_IDS` | Comma-separated public subnet IDs |

### 2.3 Provider Interface — `internal/cluster/provider.go`

Add VPC lifecycle methods to the provider interface:

```go
type Provider interface {
    // ... existing methods ...

    // CreateVPC creates a VPC and returns its details.
    // Only implemented by the AWS provider; other providers return ErrNotSupported.
    CreateVPC(ctx context.Context, spec DynamicVPCSpec, clusterName, region string) (*VPCInfo, error)

    // DeleteVPC destroys a VPC and all associated resources (subnets, IGW, route tables).
    DeleteVPC(ctx context.Context, vpcID, region string) error
}

type VPCInfo struct {
    ID             string
    ARN            string
    Name           string
    CIDR           string
    SubnetIDs      []string
    PrivateSubnets []string
    PublicSubnets  []string
}
```

### 2.4 VPC State Persistence

After the VPC is created, write its ID back to the cluster YAML so it can be referenced and cleaned up on future reconcile runs:

```go
// Inside ClusterSpec — populated automatically, not set by the user:
DynamicVPCID string `yaml:"dynamicVpcId,omitempty"`
```

The reconciler commits this field back to the state repository before proceeding to `beforeCreate`.

---

## 3. Variable References in Cluster Specs

### 3.1 Motivation

`beforeCreate` workflows can create IAM roles, subnets, and other resources using the exported VPC env vars. The ARNs produced by those workflows need to feed back into the cluster spec (e.g. `awsEksRoleArn`, `awsNodeRoleArn`, `awsVpcId`) before the cluster is actually provisioned.

### 3.2 `${VAR}` Placeholder Syntax

Any string field in `ClusterSpec` may contain an environment variable reference of the form `${VAR_NAME}`. The reconciler resolves these references **after** `beforeCreate` workflows complete, using the current process environment (which already includes `HYVE_VPC_*` and any variables exported by `beforeCreate` steps).

Fields that support `${VAR}` references:

| Field | Example |
|---|---|
| `awsVpcId` | `${HYVE_VPC_ID}` |
| `awsVpcName` | `$dynamic` (special sentinel — see §3.3) |
| `awsEksRoleArn` | `${MY_EKS_ROLE_ARN}` |
| `awsNodeRoleArn` | `${MY_NODE_ROLE_ARN}` |
| `awsAccountId` | `${AWS_ACCOUNT_ID}` |

Resolution is handled by a new helper in `internal/reconcile`:

```go
// resolveSpecVars expands ${VAR} references in string fields of the spec
// using os.Getenv. Fields with unresolved references are left unchanged and
// logged as warnings.
func resolveSpecVars(spec *types.ClusterSpec) error
```

### 3.3 `$dynamic` Sentinel for VPC Name

When `spec.awsVpcName` is set to the literal string `$dynamic`, the reconciler treats it as "use the dynamically created VPC". It is equivalent to setting `spec.dynamicVpc.enabled: true` with no explicit name — the two can be used interchangeably. After the VPC is created, `awsVpcId` is populated with `HYVE_VPC_ID` automatically; the `$dynamic` sentinel is never persisted to the state YAML.

### 3.4 Resolution Order

```
1. Create VPC (if dynamicVpc.enabled or awsVpcName == "$dynamic")
2. Set HYVE_VPC_* env vars
3. Run beforeCreate workflows
   └─ workflows may set additional env vars (e.g. MY_EKS_ROLE_ARN)
4. Call resolveSpecVars(spec)
   └─ expands ${HYVE_VPC_ID}, ${MY_EKS_ROLE_ARN}, etc.
5. Validate that required fields (awsVpcId, awsEksRoleArn, awsNodeRoleArn) are non-empty
6. Create cluster
```

---

## 4. Reconciler Changes — `internal/reconcile/manager.go`

### 4.1 Updated `reconcileCluster` flow for `ActionCreate`

```go
case ActionCreate:
    // Step 1: Dynamic VPC
    if spec.DynamicVPC.Enabled || spec.AWSVPCName == "$dynamic" {
        vpcInfo, err := prov.CreateVPC(ctx, spec.DynamicVPC, clusterName, spec.Region)
        if err != nil {
            return fmt.Errorf("creating dynamic VPC: %w", err)
        }
        // Persist VPC ID to YAML and push
        next.Spec.DynamicVPCID = vpcInfo.ID
        if err := r.writeAndPushClusterDef(ctx, next); err != nil {
            return err
        }
        // Export env vars
        exportVPCEnv(vpcInfo)
    }

    // Step 2: beforeCreate workflows
    for _, wf := range spec.Workflows.BeforeCreate {
        if err := r.runWorkflow(ctx, wf, clusterName); err != nil {
            return fmt.Errorf("beforeCreate workflow %q: %w", wf, err)
        }
    }

    // Step 3: Resolve ${VAR} placeholders
    if err := resolveSpecVars(&next.Spec); err != nil {
        return err
    }

    // Step 4: Create cluster
    if err := clusterMgr.Create(ctx, next.Spec); err != nil {
        return err
    }

    // Step 5: onCreated workflows
    for _, wf := range spec.Workflows.OnCreated {
        if err := r.runWorkflow(ctx, wf, clusterName); err != nil {
            log.Printf("[%s] onCreated workflow %q failed: %v", clusterName, wf, err)
        }
    }
```

### 4.2 Updated `reconcileCluster` flow for `ActionDelete`

```go
case ActionDelete:
    // Step 1: onDestroy workflows
    for _, wf := range spec.Workflows.OnDestroy {
        if err := r.runWorkflow(ctx, wf, clusterName); err != nil {
            log.Printf("[%s] onDestroy workflow %q failed: %v", clusterName, wf, err)
        }
    }

    // Step 2: Delete cluster
    if err := clusterMgr.Delete(ctx, clusterName); err != nil {
        return err
    }

    // Step 3: afterDelete workflows
    for _, wf := range spec.Workflows.AfterDelete {
        if err := r.runWorkflow(ctx, wf, clusterName); err != nil {
            log.Printf("[%s] afterDelete workflow %q failed: %v", clusterName, wf, err)
        }
    }

    // Step 4: Destroy dynamic VPC (after afterDelete so workflows can still reference it)
    if spec.DynamicVPCID != "" {
        if err := prov.DeleteVPC(ctx, spec.DynamicVPCID, spec.Region); err != nil {
            log.Printf("[%s] warning: failed to delete dynamic VPC %s: %v",
                clusterName, spec.DynamicVPCID, err)
        }
    }

    // Step 5: Remove YAML file, commit, push
    r.removeClusterFile(ctx, clusterName)
```

---

## 5. CLI Changes

### 5.1 `hyve cluster add` — `cmd/cluster/cmd.go`

New flags:

```
--before-create stringArray   Workflow name(s) to run before cluster creation (repeatable)
--after-delete  stringArray   Workflow name(s) to run after cluster deletion (repeatable)
--dynamic-vpc                 (EKS only) Automatically create and destroy a VPC for this cluster
--dynamic-vpc-cidr string     CIDR block for the dynamic VPC (default: 10.0.0.0/16)
```

When `--dynamic-vpc` is set, `--vpc-name` becomes optional. If both are omitted with `--dynamic-vpc`, `awsVpcName` is set to `$dynamic`.

Variable references are accepted literally in `--eks-role-name` and `--node-role-name`, e.g.:

```bash
hyve cluster add prod-eks \
  --provider aws \
  --account-name prod \
  --dynamic-vpc \
  --eks-role-name '${MY_EKS_ROLE_ARN}' \
  --node-role-name '${MY_NODE_ROLE_ARN}' \
  --before-create setup-iam-roles \
  --on-created deploy-addons \
  --after-delete cleanup-iam-roles
```

### 5.2 `hyve cluster modify` — `cmd/cluster/cmd.go`

New flags (same pattern as `add`):

```
--before-create stringArray
--after-delete  stringArray
--dynamic-vpc / --no-dynamic-vpc
--dynamic-vpc-cidr string
```

### 5.3 `hyve template execute` — `cmd/template/cmd.go`

New flags:

```
--before-create stringArray   Override beforeCreate workflows from the template
--after-delete  stringArray   Override afterDelete workflows from the template
--dynamic-vpc                 Enable dynamic VPC for this execution (EKS only)
--dynamic-vpc-cidr string
```

### 5.4 `hyve cluster add` and `hyve template execute` — Validation

When `--dynamic-vpc` is set (or `awsVpcName == "$dynamic"`):
- Skip the `--vpc-name` required check for AWS provider
- Warn if `--eks-role-name` / `--node-role-name` are not set and not referenced via `${VAR}` — these must be resolvable by the time `resolveSpecVars` is called

---

## 6. Template Type Changes — `internal/template/template.go`

Extend `TemplateSpec` to mirror `WorkflowsSpec`:

```go
type TemplateWorkflowsSpec struct {
    BeforeCreate []string `yaml:"beforeCreate,omitempty"`
    OnCreated    []string `yaml:"onCreated,omitempty"`
    OnDestroy    []string `yaml:"onDestroy,omitempty"`
    AfterDelete  []string `yaml:"afterDelete,omitempty"`
}
```

Add dynamic VPC to `TemplateSpec`:

```go
type TemplateSpec struct {
    // ... existing fields ...
    DynamicVPC DynamicVPCSpec `yaml:"dynamicVpc,omitempty"`
}
```

`ExecuteTemplate` must propagate `DynamicVPC` and `BeforeCreate`/`AfterDelete` to the generated `ClusterDefinition`.

---

## 7. TUI Changes

### 7.1 `interactiveClusterAdd` — `cmd/cluster/interactive.go`

After the existing workflow multi-select step, add:

- Multi-select for `beforeCreate` workflows
- Multi-select for `afterDelete` workflows
- Confirm: "Automatically create and destroy a VPC for this cluster?" (EKS only)
- If yes, optional text input for CIDR (default `10.0.0.0/16`)

### 7.2 `interactiveClusterModify` — `cmd/cluster/interactive.go`

Add comparable fields to the modify flow.

### 7.3 `interactiveTemplateCreate` / `interactiveTemplateExecute` — `cmd/template/interactive.go`

Mirror the same additions for templates.

---

## 8. Cluster YAML Examples

### 8.1 EKS with Dynamic VPC and IAM Role Creation via `beforeCreate`

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
  dynamicVpc:
    enabled: true
    cidr: 10.0.0.0/16
  awsVpcId: ${HYVE_VPC_ID}           # resolved after dynamic VPC is created
  awsEksRoleArn: ${MY_EKS_ROLE_ARN}  # resolved after beforeCreate runs
  awsNodeRoleArn: ${MY_NODE_ROLE_ARN}
  nodeGroups:
    - name: workers
      instanceType: t3.medium
      count: 3
      minCount: 1
      maxCount: 6
  workflows:
    beforeCreate:
      - create-iam-roles    # runs with HYVE_VPC_* vars; exports MY_EKS_ROLE_ARN, MY_NODE_ROLE_ARN
    onCreated:
      - deploy-platform-addons
    onDestroy:
      - drain-workloads
    afterDelete:
      - destroy-iam-roles   # IAM roles depend on cluster being gone first
                            # VPC is destroyed automatically after this hook completes
```

### 8.2 Minimal EKS with `$dynamic` Shorthand

```yaml
apiVersion: v1
kind: Cluster
metadata:
  name: dev-eks
spec:
  provider: aws
  awsAccount: dev
  region: us-east-1
  clusterType: eks
  awsVpcName: $dynamic   # triggers VPC creation; awsVpcId is auto-populated
  awsEksRole: my-eks-role
  awsNodeRole: my-node-role
  nodeGroups:
    - name: workers
      instanceType: t3.small
      count: 2
```

### 8.3 Template with Dynamic VPC

```yaml
apiVersion: v1
kind: Template
metadata:
  name: eks-ephemeral
  description: Short-lived EKS cluster with auto-managed VPC and IAM
spec:
  provider: aws
  region: us-east-1
  clusterType: eks
  dynamicVpc:
    enabled: true
    cidr: 10.0.0.0/16
  awsVpcId: ${HYVE_VPC_ID}
  awsEksRoleArn: ${MY_EKS_ROLE_ARN}
  awsNodeRoleArn: ${MY_NODE_ROLE_ARN}
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

## 9. AWS Provider Implementation Notes

### 9.1 `CreateVPC`

The AWS provider's `CreateVPC` implementation should:

1. Create the VPC with the given CIDR
2. Enable DNS hostnames and DNS resolution
3. Create an internet gateway and attach it to the VPC
4. Create public and private subnets across at least 2 availability zones
5. Create route tables and associate subnets
6. Tag all resources with `hyve-cluster: <clusterName>` and `hyve-managed: true` for safe cleanup

### 9.2 `DeleteVPC`

`DeleteVPC` must tear down resources in dependency order:

1. Detach and delete internet gateway
2. Delete subnets
3. Delete route tables (except the main route table)
4. Delete the VPC

Use the `hyve-managed: true` tag as an additional guard to avoid accidentally deleting VPCs not created by Hyve.

### 9.3 Non-AWS Providers

`CreateVPC` and `DeleteVPC` should return a sentinel error (`ErrNotSupported`) for non-AWS providers. The reconciler logs a warning and skips VPC lifecycle if the provider does not support it.

---

## 10. File Change Summary

| File | Change |
|---|---|
| `internal/types/types.go` | Add `BeforeCreate`, `AfterDelete` to `WorkflowsSpec`; add `DynamicVPCSpec`, `DynamicVPC`, `DynamicVPCID` to `ClusterSpec` |
| `internal/cluster/provider.go` | Add `CreateVPC`, `DeleteVPC` to the `Provider` interface; add `VPCInfo` type |
| `internal/cluster/aws/provider.go` | Implement `CreateVPC` and `DeleteVPC` |
| `internal/cluster/civo/provider.go` | Stub `CreateVPC`/`DeleteVPC` returning `ErrNotSupported` |
| `internal/cluster/gcp/provider.go` | Same stub |
| `internal/cluster/azure/provider.go` | Same stub |
| `internal/reconcile/manager.go` | Insert VPC creation, `beforeCreate`, `resolveSpecVars` before `ActionCreate`; insert `afterDelete`, VPC deletion after `ActionDelete` |
| `internal/reconcile/vars.go` | New file: `resolveSpecVars`, `exportVPCEnv` helpers |
| `internal/template/template.go` | Add `BeforeCreate`, `AfterDelete` to `TemplateWorkflowsSpec`; add `DynamicVPC` to `TemplateSpec` |
| `cmd/cluster/cmd.go` | Add `--before-create`, `--after-delete`, `--dynamic-vpc`, `--dynamic-vpc-cidr` flags to `addCmd` and `modifyCmd` |
| `cmd/cluster/crud.go` | Pass new fields through `addClusterFromCLI`; handle flags in `modifyClusterFromCLI`; display in `listClusters` |
| `cmd/cluster/interactive.go` | Add TUI steps for `beforeCreate`, `afterDelete`, dynamic VPC in `interactiveClusterAdd` and `interactiveClusterModify` |
| `cmd/template/cmd.go` | Add `--before-create`, `--after-delete`, `--dynamic-vpc`, `--dynamic-vpc-cidr` to `templateExecuteCmd` |
| `cmd/template/interactive.go` | Mirror TUI additions for template create and execute |
