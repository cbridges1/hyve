<p align="center">
  <img src="images/banner.svg" alt="Hyve Banner" width="800">
</p>

# Hyve

Hyve manages the full lifecycle of Kubernetes clusters (create, configure, reconcile, tear down) on any cloud. Clusters are plain YAML. Run `hyve reconcile` against a directory, or deploy Hyve as a cluster-native controller + API with a web console. Both modes use the same YAML and the same engine. Cloud operations live in **modules**: versioned git repos of shell scripts or workflow YAMLs. Hyve embeds no cloud SDKs.

[![Documentation](https://img.shields.io/badge/docs-hyve--website-green)](https://cbridges1.github.io/hyve-website/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Features

- **Files are the state.** Desired and recorded state are plain files, so git gives you history, review, and rollback. Hyve never commits or pushes.
- **Continuous reconciliation.** Add a definition to create, change a field to update, set `spec.delete: true` to destroy.
- **Any provider.** A module wraps any CLI (`civo`, `aws`, `gcloud`, ...) using your own credentials. Scaffold one with `hyve module init`.
- **Templates and lifecycle hooks.** Stamp out clusters from a template, and run workflows at `beforeCreate`, `onCreate`, `afterCreate`, `onDelete`, and `afterDelete`.

## Install

```bash
brew install cbridges1/tap/hyve
# or
go install github.com/cbridges1/hyve@latest   # Go 1.26+, git on PATH; web console is a placeholder
# or
docker run --rm -v "$(pwd)":/repo ghcr.io/cbridges1/hyve:latest reconcile --path .
```

Prebuilt binaries for macOS, Linux, and Windows are on every [GitHub Release](https://github.com/cbridges1/hyve/releases).

## Quick start

```bash
hyve module add github.com/your-org/hyve-civo-module   # placeholder module
hyve context create --path .                            # any directory; git optional

hyve template create civo --driver github.com/your-org/hyve-civo-module \
  --driver-version latest --region PHX1 --set node_count=3
hyve cluster create my-cluster --template civo          # writes clusters/my-cluster.yaml

hyve reconcile                                          # provisions it and runs hooks
hyve cluster auth my-cluster && kubectl get nodes
```

Every file Hyve writes is a real `hyve.io/v1alpha1` custom resource (`ClusterDefinition`, `Template`, `Workflow`). You can `kubectl apply` them to a cluster running Hyve's controller unchanged.

## Cluster mode

Deploy the controller + API with Helm, then point the CLI at it:

```bash
helm install hyve oci://ghcr.io/cbridges1/charts/hyve --version <version> \
  --namespace hyve-system --create-namespace \
  --set api.publicBaseURL=https://hyve.example.com \
  --set api.bootstrapAdmin.username=admin \
  --set api.bootstrapAdmin.passwordSecret.name=hyve-admin   # Secret with a "password" key

hyve context login --api-url https://hyve.example.com              # superadmin (control plane)
hyve context login --api-url https://hyve.example.com --org acme   # a user in organization "acme"
hyve context whoami
```

Login prompts for username and password; pass `--username` to skip the first prompt.

- The chart creates a `hyve-api` ClusterIP Service and no Ingress. Expose it with TLS your own way.
- `api.db.*` picks SQLite (default, one replica) or Postgres (required for scaling or reconciling clusters). `api.smtp.*` seeds outbound email. See `deploy/helm/hyve/values.yaml`.
- Module operations run as Kubernetes Jobs using the cluster's `spec.runner.image` or `HyveConfig.spec.defaultModuleImage`.

**Contexts** (`hyve context create/use/list`) point the CLI at a local directory or an API URL. Your login is separate: one machine-wide session that switching contexts never touches.

**Environments** (`hyve environment create/use`) are named scopes within an organization, such as `default` or `staging`. Override one per command with `--env`.

**Multi-tenancy:** one install serves many `Organization`s (`hyve organization create`). Each can run on its own **reconciling cluster** (`hyve reconciling-cluster add/use`). Enable `api.requireReconcilingCluster` to keep tenants off the home cluster. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

To move local state into a cluster, run `hyve migrate <dir> --write`. Leaving off `--write` gives a dry run.

> Helm only installs CRDs on first install. After a CRD change, run `kubectl apply -f deploy/helm/hyve/crds/` before `helm upgrade`.

## Modules

| File | Operation |
|------|-----------|
| `status.sh` / `.yaml` | Report whether the cluster exists and its state |
| `create.sh` / `.yaml` | Provision the cluster |
| `delete.sh` / `.yaml` | Destroy the cluster |
| `auth.yaml` | Print `HYVE_KUBECONFIG_B64=<kubeconfig>` |
| `scale.sh` / `.yaml` | Apply param changes (optional) |

Params arrive as `HYVE_PARAM_*` env vars. Operations print `HYVE_KEY=value` lines, which are saved as `driverOutputs` and passed to later runs. See the [module authoring guide](https://cbridges1.github.io/hyve-website/docs/guides/module-authoring).

## Development

```bash
task build          # build the binary
task check          # go vet + tests
task cluster:local && task install:local   # local k3d dev install
```

Run `task --list` for the rest. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and [docs/TESTING.md](docs/TESTING.md). Full docs are at **[cbridges1.github.io/hyve-website](https://cbridges1.github.io/hyve-website/)**.
