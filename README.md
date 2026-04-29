<p align="center">
  <img src="images/banner.svg" alt="Hyve Banner" width="800">
</p>

# Hyve

A GitOps-first Kubernetes cluster management CLI. Define clusters as YAML, commit the change, and Hyve reconciles the desired state against your cloud provider — locally or through a CI/CD pipeline.

Supports **Civo, AWS (EKS), GCP (GKE), and Azure (AKS)** with multi-account credential routing, lifecycle hook workflows, automated kubeconfig management, and strict-delete enforcement.

[![Documentation](https://img.shields.io/badge/docs-hyve.mintlify.app-green)](https://hyve.mintlify.app)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Features

- **GitOps Native** — All cluster state managed through Git repositories
- **Multi-Cloud** — Civo, AWS EKS, GCP GKE, and Azure AKS behind a single YAML schema
- **Lifecycle Hooks** — Run workflows before/after cluster creation and deletion
- **Cluster Templates** — Reusable cluster patterns with embedded workflow hooks and expiry schedules
- **Kubeconfig Management** — Encrypted storage, auto-refresh, and `kubectl` context merging

## Why Hyve?

General-purpose IaC tools (Terraform, Pulumi, Crossplane) are built to manage any cloud resource. That generality is a good fit for VPCs, IAM roles, and databases — resources that are provisioned once and live for years. It becomes friction when the resource is a Kubernetes cluster that a team actively creates, destroys, and recreates on a regular cadence.

**Git is the state backend.** No S3 bucket, no cloud account, no extra credentials — the full history of every cluster change is already in the repository.

**Continuous reconciliation, not plan-and-apply.** Commit to create, delete the file to destroy, update a field to change. The same `hyve reconcile` command handles all three cases.

**Lifecycle hooks are built in.** Four hook points cover the full cluster lifecycle. The `beforeCreate` and `afterDelete` hooks run without a live cluster; `onCreate` and `onDestroy` receive an injected kubeconfig automatically.

| Hook | Cluster Exists? | Kubeconfig? | When It Runs |
|------|-----------------|-------------|--------------|
| `beforeCreate` | No | No | Before the cluster is provisioned — provision VPC, IAM roles, etc. |
| `onCreate` | Yes | Yes | After the cluster is ready — deploy apps, configure monitoring |
| `onDestroy` | Yes | Yes | Before deletion — drain workloads, export backups |
| `afterDelete` | No | No | After deletion — destroy VPC, release IP ranges, clean up roles |

**Consistent multi-cloud interface.** The same YAML schema and CLI commands work across all four supported providers.

## Documentation

Full documentation at **[hyve.mintlify.app](https://hyve.mintlify.app)** — CLI reference, guides, and provider configuration.

## Installation

Requires Go 1.21+ and the system `git` binary in `PATH`.

**Using `go install`:**

```bash
go install github.com/cbridges1/hyve@latest
```

**From source:**

```bash
git clone https://github.com/cbridges1/hyve.git
cd hyve
go build -o hyve .
sudo mv hyve /usr/local/bin/
```

## Quick Start (Azure AKS)

```bash
# 1. Point Hyve at a Git repository for state management
hyve git add production --repo-url https://github.com/company/hyve-state.git

# 2. Register an Azure subscription (reads credentials from azure.yaml in your state repo)
hyve config azure subscription add my-subscription

# 3. Create a cluster interactively — picks region and node size from the Azure API
hyve cluster create

# Or create directly via flags
hyve cluster create my-aks-cluster \
  --provider azure \
  --subscription my-subscription \
  --resource-group my-resource-group \
  --region eastus \
  --nodes Standard_D4s_v3

# 4. Reconcile — provisions the cluster and runs any lifecycle hooks
hyve reconcile
```

The resulting cluster YAML (committed to your state repo) looks like:

```yaml
apiVersion: v1
kind: Cluster
metadata:
  name: my-aks-cluster
  region: eastus
spec:
  provider: azure
  azureSubscription: my-subscription
  azureResourceGroup: my-resource-group
  nodes:
    - Standard_D4s_v3
  workflows:
    beforeCreate:
      - provision-network
    onCreate:
      - deploy-monitoring
      - configure-ingress
    onDestroy:
      - drain-workloads
    afterDelete:
      - cleanup-network
```

Provider credentials are stored in `provider-configs/azure.yaml` in your state repository. Values can be literal strings or `${ENV_VAR}` references expanded at reconcile time:

```yaml
subscriptions:
  - name: my-subscription
    subscriptionId: ${AZURE_SUBSCRIPTION_ID}
    tenantId: ${AZURE_TENANT_ID}
    clientId: ${AZURE_CLIENT_ID}
    clientSecret: ${AZURE_CLIENT_SECRET}
```

Git authentication uses the `HYVE_GIT_TOKEN` environment variable. The system `git` binary handles all repository operations.

## Development

[Task](https://taskfile.dev) is used to simplify common operations:

| Command | Description |
|---------|-------------|
| `task build` | Build the `hyve` binary |
| `task run -- [args]` | Build and run with arguments |
| `task dev -- [args]` | Run directly with `go run` |
| `task test` | Run all tests |
| `task test:verbose` | Run all tests with verbose output |
| `task test:race` | Run all tests with race detector |
| `task test:cover` | Run all tests with coverage report |
| `task test:report` | Run tests and generate JSON report |
| `task vet` | Run `go vet` |
| `task check` | Run vet and tests |
| `task tidy` | Tidy go modules |
| `task clean` | Remove binary and report artifacts |
