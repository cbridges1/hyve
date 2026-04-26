# Plan: Remove Provider Resource Aliases and Revert to Single Config Files

## Background

Provider config files were previously structured as per-account subdirectories
(`provider-configs/aws/<account>.yaml`) with VPC, IAM role, resource group, and
network alias entries synced from the cloud. This adds complexity without enough
value: the app should not be responsible for provisioning or syncing cloud
resources. Aliases are removed in favour of direct IDs/names in cluster and
template definitions, with the TUI providing live cloud lookups so users never
need to memorise IDs.

---

## Step 1 — Revert `internal/providerconfig/` to single file per provider

**`manager.go`**
- Remove `getProviderDir`, `getAccountConfigPath`, `ensureProviderDir`
- Remove `ConfigExists`
- Remove the `SaveAWSConfig` / `SaveAzureConfig` / `SaveGCPConfig` / `SaveCivoConfig` fan-out wrappers
- Restore `getConfigPath(provider string) string` → `provider-configs/<provider>.yaml`

**`aws.go`**
- Delete types: `AWSVPC`, `AWSEKSRole`, `AWSNodeRole`
- Delete all Add/Remove/Get/List/Has methods for VPCs, EKS roles, and node roles
- Delete `LoadAWSAccount`, `SaveAWSAccount`, `ListAWSAccounts`, and the glob-based `LoadAWSConfig`
- Restore simple `LoadAWSConfig` / `SaveAWSConfig` reading the single flat file
- `AWSAccount` retains only: `Name`, `AccountID`, `AccessKeyID`, `SecretAccessKey`, `SessionToken`, `Regions`

**`azure.go`**
- Delete type: `AzureResourceGroup`
- Delete all resource group Add/Remove/Get/List methods
- `AzureSubscription` retains only credentials and subscription ID
- Restore single-file load/save

**`gcp.go`**, **`civo.go`**
- Restore single-file load/save (no resource fields to remove)

---

## Step 2 — Delete `internal/reconcile/sync.go`

- Delete the file entirely
- Remove the `SyncProviderConfigFields` call and surrounding commit block from `ReconcileAll` in `internal/reconcile/manager.go`
- Remove the `SyncProviderConfigFields` call from `cmd/sync/cmd.go`

---

## Step 2a — Move `hyve sync` to `hyve cluster sync`

`hyve sync` now only discovers and imports unmanaged clusters — it has nothing to
do with provider config. Move it under the cluster command group to reflect this.

- Move `cmd/sync/` logic into `cmd/cluster/sync.go`
- Register it as `hyve cluster sync` (subcommand of the existing `cluster` command)
- Delete the top-level `cmd/sync/` directory
- Remove the top-level `sync` entry from the root command in `cmd/root.go` (or equivalent)
- Update the TUI to invoke `hyve cluster sync` instead of `hyve sync`
- Update docs: remove `cli/sync.mdx` standalone page; add a `cluster sync` section to `cli/cluster.mdx`

---

## Step 3 — Update strict-delete Azure sweep in `internal/reconcile/manager.go`

The strict-delete sweep currently iterates `sub.ResourceGroups` to determine
which Azure scopes to check for orphaned clusters. Replace this with a dynamic
`ResourceGroupsClient.NewListPager` call so all resource groups in each
subscription are swept without requiring them in the config file.

---

## Step 4 — Remove alias fields from cluster and template specs

**`internal/types/types.go`**
- Remove `AWSVPCName` (alias field)
- Remove `AWSEKSRole` and `AWSNodeRole` (alias fields)
- Keep `AWSVPCID`, `AWSEKSRoleName`, `AWSNodeRoleName`, and the runtime-only `AWSEKSRoleARN` / `AWSNodeRoleARN`
- `AzureResourceGroup string` already exists as a direct field — no change needed, confirm it is kept

**`internal/template/types.go`**
- Same AWS removals as above
- `AzureResourceGroup string` already exists as a direct field — no change needed, confirm it is kept

**`internal/reconcile/manager.go`**
- Remove the alias-lookup resolution blocks for VPC name → ID, EKS role alias → ARN, and node role alias → ARN
- Keep the `AWSEKSRoleName` + `AWSAccountID` → construct ARN path
- `AzureResourceGroup` is already passed through directly to provider options — no change needed

---

## Step 5 — Update `internal/reconcile/vars.go`

- Remove `pcMgr.AddAWSVPC` call (method no longer exists)
- Remove `pcMgr.AddAzureResourceGroup` call (method no longer exists)
- Remove the `HYVE_VPC_NAME` env var block entirely — it only existed to persist the name as an alias
- Keep `HYVE_VPC_ID` (sets `AWSVPCID` directly), `HYVE_EKS_ROLE_NAME`, and `HYVE_NODE_ROLE_NAME`

---

## Step 6 — Update CLI flags

**`cmd/cluster/cmd.go`**
- Rename `--vpc-name` to `--vpc-id` (users now provide the VPC ID directly)
- Add `--resource-group` flag for Azure cluster creation — currently missing entirely from cluster create (it only exists on `template execute`)

**`cmd/cluster/crud.go`**
- Remove the `GetAWSVPCID` alias resolution call — the value passed in is already the ID
- Accept `resourceGroup` parameter and write it to `clusterDef.Spec.AzureResourceGroup`

**`cmd/template/cmd.go`**
- Replace `awsVpcName` with `awsVpcId` in template creation flags
- `--resource-group` flag already exists on `template execute` — no change needed there

---

## Step 7 — Add cloud lookup helpers for TUI use

Add `internal/cloudlookup/aws.go` and `internal/cloudlookup/azure.go` with
stateless, read-only functions the TUI calls on demand. Nothing is persisted.

AWS (`aws.go`):
- `ListVPCs(ctx, creds, region) ([]VPCOption, error)` — `DescribeVpcs`, returns `{ID, Name, CIDR}` per non-default VPC
- `ListIAMRoles(ctx, creds, filter) ([]RoleOption, error)` — `ListRoles` with optional name filter, returns `{Name, ARN}`

Azure (`azure.go`):
- `ListResourceGroups(ctx, creds, subscriptionID) ([]ResourceGroupOption, error)` — `ResourceGroupsClient.NewListPager`, returns `{Name, Location}`

---

## Step 8 — Update TUI flows

Update `cmd/cluster/interactive.go` and `cmd/template/interactive.go` so that
any AWS cluster or template creation step that previously prompted for a VPC name
alias or role alias instead presents a three-option selection:

**VPC selection**
1. *Fetch from AWS* — calls `ListVPCs` with the account's credentials and region;
   presents a filterable list showing `Name (vpc-0abc123, 10.0.0.0/16)`; the
   selected VPC ID is written directly to `awsVpcId`
2. *Enter manually* — free-text prompt for a VPC ID (e.g. `vpc-0abc123`)
3. *Skip* — leave blank (valid if a `beforeCreate` hook sets `HYVE_VPC_ID`)

**EKS role / node role selection**
1. *Fetch from AWS* — calls `ListIAMRoles` with an optional filter (e.g. names
   containing "eks" or "k8s") to narrow the list; presents filterable `Name (ARN)`;
   the selected role name is written to `awsEksRoleName` / `awsNodeRoleName`
2. *Enter manually* — free-text prompt for a role name
3. *Skip* — leave blank (valid if a `beforeCreate` hook sets `HYVE_EKS_ROLE_NAME`)

**Azure resource group selection**
1. *Fetch from Azure* — calls `ListResourceGroups` with the subscription's credentials;
   presents a filterable list showing `Name (location)`; the selected name is written
   directly to `azureResourceGroup`
2. *Enter manually* — free-text prompt for a resource group name
3. *Skip* — leave blank (valid if a `beforeCreate` hook sets `HYVE_RESOURCE_GROUP_NAME`)

---

## Step 9 — Clean up tests

- **`internal/providerconfig/providerconfig_test.go`**: Remove tests for VPC, role, and resource group methods; remove per-account file structure tests; update remaining tests to use single-file paths
- **`internal/reconcile/vars_test.go`**: Remove `HYVE_VPC_NAME` alias persistence tests

---

## Step 10 — Update docs

- `cli/sync.mdx` — delete; content moves to a `cluster sync` section in `cli/cluster.mdx`
- `docs.json` — remove `cli/sync` nav entry; add `cluster sync` under the cluster reference
- `configuration.mdx` — revert storage layout tree to flat files; remove "Provider Config Sync" section
- All files showing `provider-configs/aws/<account>.yaml` — revert to `provider-configs/aws.yaml`
- Cluster and template field reference pages — remove `awsVpcName`, `awsEksRole`, `awsNodeRole` alias fields; document `awsVpcId` and the live-lookup TUI flow
- Interactive mode guide — document the three-option selection for VPCs, IAM roles, and Azure resource groups
