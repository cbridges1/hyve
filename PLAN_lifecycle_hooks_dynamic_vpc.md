# Plan: Lifecycle Hooks & Provider Config Passive Sync

## Overview

1. **`beforeCreate` hook** — run workflows before a cluster is provisioned
2. **`afterDelete` hook** — run workflows after a cluster is destroyed
3. **Provider config passive sync** — reconcile provider config YAML fields against the actual cloud state on every reconcile run: add fields for resources that exist in the cloud but are absent from the YAML, and remove fields for resources that no longer exist

**Design notes:**
- Hyve does not provision or destroy any supporting infrastructure (VPCs, IAM roles, resource groups, subnets, etc.). All such resources must be provisioned externally — via IaC tooling or `beforeCreate`/`afterDelete` workflows — before the cluster is created and cleaned up after it is deleted.
- ARNs are never stored in config or cluster specs. Role ARNs are resolved internally at runtime via name lookup.
- If resource fields (`awsVpcName`, `awsVpcId`, `awsEksRoleName`, `awsNodeRoleName`, `azureResourceGroup`) are absent from the cluster YAML, the reconciler assumes the `beforeCreate` hook will export their details as the corresponding environment variables (`HYVE_VPC_NAME`, `HYVE_VPC_ID`, `HYVE_EKS_ROLE_NAME`, `HYVE_NODE_ROLE_NAME`, `HYVE_RESOURCE_GROUP_NAME`). After `beforeCreate` completes, the reconciler reads those env vars, writes the resolved values back to the appropriate provider config YAML, and then proceeds to create the cluster.
- Provider config files reference pre-existing resources by name. Hyve never creates or deletes these resources — it only keeps the YAML fields in sync with what actually exists in the cloud (adding missing entries, removing stale ones) on every reconcile run.

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

One file per account/project/subscription. Each file references externally-provisioned resources (VPCs, resource groups, IAM roles) by name. Hyve never creates or deletes these resources — on every reconcile it queries the cloud provider and reconciles the YAML fields to match: adding entries for resources that exist but are absent from the file, and removing entries for resources that no longer exist.

---

## Hook Order

**Every reconcile run (independent of hooks):**
```
query cloud provider for all resources referenced in provider config files
    ↓
add YAML fields for resources that exist in cloud but are absent from the file
remove YAML fields for resources that no longer exist in the cloud
    ↓
commit + push if any changes were made
```

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
remove cluster YAML, commit, push
```

The passive sync (add missing fields, remove stale fields) runs as part of every reconcile and is not triggered by or dependent on any hook. It will naturally reflect the state after `afterDelete` completes on the next reconcile cycle.

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

### New commands

- `hyve cluster show [cluster-name]` — print the full cluster definition YAML and a summary of live fields (provider, region, node groups, workflows, pause state, expiry). Reads from the repo definition; does not make cloud API calls.

### Renamed commands

- `hyve cluster add` → `hyve cluster create`

### New flags — `hyve cluster create` / `hyve cluster modify`

```
--before-create stringArray         Workflows to run before cluster creation
--after-delete  stringArray         Workflows to run after cluster deletion
--eks-role-name string              IAM role name for the EKS control plane
--node-role-name string             IAM role name for EKS node groups
```

### Removed flags

```
--eks-role-arn      (replaced by --eks-role-name)
--node-role-arn     (replaced by --node-role-name)
```

### New command — `hyve sync`

```
hyve sync [flags]

--provider string    Limit to a specific provider (civo, aws, gcp, azure)
--account  string    Limit to a specific account/subscription/project alias
--dry-run            Print what would be imported without writing anything
```

Performs a full discovery pass against all configured cloud accounts and presents findings interactively. Covers two areas:

**Clusters** — queries each provider account for running clusters and compares against existing repo definitions. Unmanaged clusters (present in the cloud, absent from the repo) are listed and the user selects which to import. For each selected cluster, hyve fetches the cluster's current configuration from the cloud (region, node groups, instance types) and writes a `ClusterDefinition` YAML to the repo.

**Provider config resources** — queries each account for VPCs, IAM roles, and resource groups and reconciles the provider config YAML files immediately (same logic as the automatic passive sync, but triggered on demand). No interactive step — all discovered resources are written.

Both steps commit and push if any changes were made.

`hyve sync` replaces `hyve cluster import`. Import required knowing the cluster name in advance and operated on one cluster at a time; `hyve sync` auto-discovers all unmanaged clusters and handles resources in the same pass.

**Interactive flow:**

```
Scanning civo / org: my-org ...
Scanning aws / account: prod ...
Scanning azure / subscription: prod-sub ...

Unmanaged clusters found (3):

  [civo / PHX1]  old-staging
  [aws / us-east-1]  legacy-eks
  [azure / eastus]  scratch-aks

Select clusters to import (space to select, enter to confirm):
  > [ ] old-staging
    [ ] legacy-eks
    [ ] scratch-aks

✔ Imported: legacy-eks
  Written: clusters/legacy-eks.yaml

Provider config sync: 2 resources added, 1 removed
Changes committed and pushed.
```

### Removed commands

- `hyve cluster import` — replaced by `hyve sync`
- `hyve cluster release` — no longer meaningful; Git is always authoritative and a released cluster would simply be re-discovered on the next sync or reconcile
- `hyve config aws vpc create` — Hyve no longer provisions VPCs
- `hyve config aws vpc delete` — Hyve no longer destroys VPCs
- `hyve config aws eks-role create` — Hyve no longer provisions IAM roles
- `hyve config aws eks-role delete` — Hyve no longer destroys IAM roles
- `hyve config aws node-role create` — Hyve no longer provisions IAM roles
- `hyve config aws node-role delete` — Hyve no longer destroys IAM roles
- `hyve config azure resource-group create` — Hyve no longer provisions resource groups
- `hyve config azure resource-group delete` — Hyve no longer destroys resource groups

---

## Workflow Scheduling & On-Demand Execution

`hyve workflow run` uses whatever kubeconfig is currently active — the same kubeconfig `kubectl` would use. No cluster registration is required. Any cluster (k3d, EKS, AKS, GKE, unsupported providers, local clusters) works out of the box as long as the correct context is set.

For hyve-managed clusters the reconciler is the single execution path — it resolves the kubeconfig from the cluster definition automatically. For all other clusters the user sets their kubeconfig context themselves (e.g. `k3d kubeconfig merge`, `aws eks update-kubeconfig`, `kubectl config use-context`) and then runs `hyve workflow run <workflow>` directly.

### `pendingWorkflows` — Git-audited queue

Add `pendingWorkflows []PendingWorkflow` to `ClusterSpec`. The reconciler processes this list on every reconcile cycle:

- Entry with **no `runAt`** — execute immediately on the next reconcile.
- Entry with a **`runAt` timestamp** — execute when the current time is at or after that timestamp.

After executing an entry the reconciler removes it from the list and commits the cleared YAML.

```yaml
spec:
  pendingWorkflows:
    - workflow: rotate-certs              # no runAt → runs on next reconcile
    - workflow: sync-secrets
      runAt: "2026-06-01T03:00:00Z"      # scheduled → runs when due
```

Flow:
```
entry written to pendingWorkflows in cluster YAML
    ↓
reconciler runs (triggered or scheduled)
    ↓
for each entry: check runAt — if absent or past, execute workflow
    ↓
remove executed entries, commit + push
```

### `workflowSchedules` — recurring, cron-driven

Add `workflowSchedules` to `ClusterSpec` to map workflow names to cron expressions. On every reconcile run the reconciler evaluates each schedule; when one is due it appends a `PendingWorkflow` entry to `pendingWorkflows`, which is then processed in the same cycle.

```yaml
spec:
  workflowSchedules:
    - workflow: rotate-certs
      schedule: "0 2 * * 0"   # Sundays at 02:00
    - workflow: sync-secrets
      schedule: "0 3 * * *"   # Daily at 03:00
```

Timing granularity is determined by the reconcile cadence (e.g. hourly). This is the same mechanism that drives `expiresAt` cluster deletion.

### `hyve workflow run` — on-demand trigger

`hyve workflow run <workflow> --cluster <cluster>` appends a `PendingWorkflow` entry with no `runAt` to the cluster YAML (commit + push) and then triggers the reconciler directly — in both local and CI/CD mode.

For workflows run without `--cluster` (or against a cluster not managed by hyve), `hyve workflow run <workflow>` executes directly against the active kubeconfig context with no Git write.

```
# Managed cluster — goes through reconciler
hyve workflow run rotate-certs --cluster prod-eks
    ↓
appends { workflow: rotate-certs } to pendingWorkflows, commits + pushes
    ↓
triggers reconciler; reconciler executes and clears the entry, commits + pushes

# Any cluster — uses active kubeconfig directly
hyve workflow run bootstrap
    ↓
executes workflow against active kubeconfig context
```

### Removed — `hyve kubeconfig add-external` and local store

The `add-external`, `list-external`, and `remove-external` kubeconfig subcommands and the associated local SQLite store (`_local` sentinel, `NewLocalManager`) are removed. Users manage kubeconfig contexts themselves using standard tooling; hyve consumes whatever context is active.

### Comparison

| | `pendingWorkflows` (no `runAt`) | `pendingWorkflows` (with `runAt`) | `workflowSchedules` | `hyve workflow run` (no `--cluster`) |
|---|---|---|---|---|
| Trigger | Git commit or `hyve workflow run --cluster` | Git commit | Automatic (cron) | Manual |
| Timing | Immediate (next reconcile) | When `runAt` is reached | Next reconcile after schedule is due | Immediate |
| Git audit trail | Yes | Yes | Yes — via `pendingWorkflows` | No |
| Use case | Ad-hoc managed cluster runs | One-off future runs | Recurring maintenance | Any cluster, quick runs |

### Type Changes

Add to `ClusterSpec`:

```go
PendingWorkflows  []PendingWorkflow     `yaml:"pendingWorkflows,omitempty"`
WorkflowSchedules []WorkflowSchedule   `yaml:"workflowSchedules,omitempty"`
```

New types:

```go
type PendingWorkflow struct {
    Workflow string `yaml:"workflow"`
    RunAt    string `yaml:"runAt,omitempty"` // RFC 3339; absent = run immediately
}

type WorkflowSchedule struct {
    Workflow string `yaml:"workflow"`
    Schedule string `yaml:"schedule"` // 5-field cron expression
}
```

### File Changes

| File | Change |
|---|---|
| `internal/types/types.go` | Add `PendingWorkflows`, `WorkflowSchedules`, `PendingWorkflow`, `WorkflowSchedule` to `ClusterSpec` |
| `internal/reconcile/manager.go` | On every reconcile: evaluate `workflowSchedules` and append due entries to `pendingWorkflows`; execute all immediately-due entries; clear and commit |
| `internal/kubeconfig/manager.go` | Remove `LocalRepoName`, `NewLocalManager`, and migration code for local store |
| `cmd/workflow/cmd.go` | `hyve workflow run --cluster` → reconciler path; `hyve workflow run` (no cluster) → execute directly against active kubeconfig |
| `cmd/kubeconfig/cmd.go` | Remove `add-external`, `list-external`, `remove-external` subcommands and their handler functions |

---

## Documentation Changes — `hyve-docs`

### New pages

| File | Content |
|---|---|
| `cli/sync.mdx` | Full reference for `hyve sync` — flags, interactive flow, what gets written |

### Updated pages

| File | Change |
|---|---|
| `cli/cluster.mdx` | Rename `hyve cluster add` → `hyve cluster create` throughout; add `hyve cluster show` section; remove `hyve cluster import` and `hyve cluster release` sections; document `pendingWorkflows` and `workflowSchedules` spec fields |
| `cli/sync.mdx` | Full reference for `hyve sync` — flags, interactive flow, what gets written |
| `cli/workflow.mdx` | Document `hyve workflow run --cluster` (reconciler path) and `hyve workflow run` without `--cluster` (direct execution against active kubeconfig); remove any `add-external` references |
| `cli/overview.mdx` | Update command listing: `add` → `create`; add `show`; remove `import`, `release`; add `sync` |
| `cli/config.mdx` | Remove AWS VPC create/delete, role create/delete, and Azure resource group create/delete subcommand entries |
| `concepts/clusters.mdx` | Update all `hyve cluster add` examples to `hyve cluster create` |
| `guides/cluster-management.mdx` | Update all `cluster add` examples to `cluster create`; replace import/release guidance with `hyve sync` |
| `guides/cicd.mdx` | Update any `cluster add` references to `cluster create` |
| `guides/access-control.mdx` | Update any `cluster add` references to `cluster create` |
| `guides/kubeconfig-management.mdx` | Update any `cluster add` references to `cluster create` |
| `concepts/gitops.mdx` | Update any `cluster add` references to `cluster create` |
| `concepts/repositories.mdx` | Update any `cluster add` references to `cluster create` |
| `quickstart.mdx` | Update any `cluster add` references to `cluster create` |
| `index.mdx` | Update any `cluster add` references to `cluster create` |
| `configuration.mdx` | Update any `cluster add` references to `cluster create` |

---

## File Change Summary

| File | Change |
|---|---|
| `internal/types/types.go` | Add `BeforeCreate`, `AfterDelete` to `WorkflowsSpec`; add `AWSVPCId`, `AWSEKSRoleName`, `AWSNodeRoleName`, `PendingWorkflows`, `WorkflowSchedules` to `ClusterSpec`; add `PendingWorkflow`, `WorkflowSchedule` types; remove ARN fields |
| `internal/cluster/provider.go` | Add role name → ARN lookup; remove all resource provisioning methods (`CreateVPC`, `DeleteVPC`, `CreateRole`, `DeleteRole`, `CreateResourceGroup`, `DeleteResourceGroup`) |
| `internal/cluster/aws/provider.go` | Implement role name lookup; remove VPC and role provisioning implementations |
| `internal/cluster/azure/provider.go` | Remove resource group provisioning implementations |
| `internal/cluster/civo/provider.go` | No changes |
| `internal/cluster/gcp/provider.go` | No changes |
| `internal/reconcile/manager.go` | Add `beforeCreate` hook execution and env var write-back before `ActionCreate`; add `afterDelete` hook execution after `ActionDelete`; call passive sync on every reconcile run |
| `internal/reconcile/sync.go` | New: `syncProviderConfigFields`, queries cloud for all resources referenced per account/subscription/project and reconciles YAML fields (add missing, remove stale); called once per reconcile regardless of action |
| `internal/reconcile/vars.go` | New: `resolveSpecVars`, reads `HYVE_*` env vars and writes to provider config YAML |
| `internal/template/types.go` | Add `BeforeCreate`, `AfterDelete` to `TemplateWorkflowsSpec`; add `AWSVPCId`, `AWSEKSRoleName`, `AWSNodeRoleName` to `TemplateSpec`; remove ARN fields |
| `cmd/cluster/cmd.go` | Rename `add` command to `create`; add `show` command; add `--before-create`, `--after-delete`, `--eks-role-name`, `--node-role-name` flags; remove ARN flags |
| `cmd/cluster/crud.go` | Pass new fields through create/modify; add `showCluster` function; update list display |
| `cmd/cluster/interactive.go` | Add `show` to interactive cluster menu; add TUI steps for new fields; remove `release` and `import` interactive flows |
| `cmd/cluster/release.go` | Delete — `release` command removed |
| `cmd/cluster/import.go` | Delete — replaced by `hyve sync` |
| `cmd/config/aws.go` | Remove VPC create/delete, role create/delete subcommands |
| `cmd/config/azure.go` | Remove resource group create/delete subcommands |
| `cmd/sync/cmd.go` | New: `hyve sync` command; `--provider`, `--account`, `--dry-run` flags |
| `cmd/sync/discover.go` | New: per-provider cluster discovery; constructs `ClusterDefinition` from live cloud state |
| `cmd/template/cmd.go` | Mirror new flags |
| `cmd/template/interactive.go` | Mirror TUI additions |

---

## Summary of Changes

### What's being added

- **Lifecycle hooks** — clusters can now declare workflows to run at four points in their lifecycle: `beforeCreate`, `onCreated`, `onDestroy`, and `afterDelete`. The reconciler executes them automatically and exports any `HYVE_*` env vars from `beforeCreate` back into the provider config YAML before creating the cluster.
- **Passive provider config sync** — on every reconcile run, hyve queries each cloud account and reconciles the provider config YAML: adding fields for resources that exist but aren't listed, and removing fields for resources that no longer exist. This runs unconditionally, not just when a hook fires.
- **Workflow scheduling** — clusters can declare `workflowSchedules` (cron-driven, recurring) and `pendingWorkflows` (a queue of one-off runs). The reconciler processes both on every cycle. Entries without a `runAt` execute immediately; entries with a `runAt` timestamp execute when due.
- **`hyve workflow run`** — triggers an immediate workflow run. With `--cluster` it commits a `pendingWorkflows` entry and triggers the reconciler. Without `--cluster` it executes directly against the active kubeconfig, so any cluster works with no registration required.
- **`hyve sync`** — new command that discovers unmanaged clusters and orphaned provider config resources across all configured cloud accounts and imports them into the repository interactively.
- **`hyve cluster show`** — new command that prints the full definition and a live summary for a cluster.
- **Resource name fields** — `awsVpcId`, `awsEksRoleName`, `awsNodeRoleName` replace the old ARN fields on `ClusterSpec`. Role ARNs are resolved at runtime by name lookup.

### What's being removed

- **ARN fields** — `awsEksRoleArn` and `awsNodeRoleArn` replaced by name-based fields; ARNs resolved internally.
- **Infrastructure provisioning commands** — hyve no longer creates or deletes VPCs, IAM roles, or Azure resource groups. All supporting infrastructure is provisioned externally (IaC or `beforeCreate` hooks). The `hyve config aws vpc`, `hyve config aws eks-role`, `hyve config aws node-role`, and `hyve config azure resource-group` subcommands are removed.
- **`hyve cluster import`** — replaced by `hyve sync`, which discovers all unmanaged clusters in one pass instead of requiring the cluster name up front.
- **`hyve cluster release`** — removed. Git is always authoritative; a released cluster would simply be re-discovered on the next sync.
- **`hyve kubeconfig add-external` / `list-external` / `remove-external`** — removed. Hyve uses whatever kubeconfig context is active; users manage contexts with standard tooling (`k3d kubeconfig merge`, `aws eks update-kubeconfig`, etc.).
- **Local kubeconfig store** — the SQLite-backed local store and `_local` sentinel are removed along with the `add-external` commands.

### What's being renamed

- **`hyve cluster add` → `hyve cluster create`** — more intuitive and consistent with standard CLI conventions.
