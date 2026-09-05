<p align="center">
  <img src="images/banner.svg" alt="Hyve Banner" width="800">
</p>

# Hyve

Hyve manages the full lifecycle of Kubernetes clusters — creation, configuration, reconciliation, and teardown — across any cloud provider. Define clusters as YAML and reconcile them with the `hyve` CLI (GitOps-native, Git as the state backend, no extra infrastructure to run it), or deploy `hyve` itself as a cluster-native controller + API for team/multi-tenant use — both modes share the same YAML and the same reconcile engine, so nothing about how a cluster is defined changes between them. Cloud operations are delegated to **modules**: versioned, self-contained packages that implement cluster operations via shell scripts or workflow YAMLs. No cloud SDKs are embedded in Hyve itself.

[![Documentation](https://img.shields.io/badge/docs-hyve--website-green)](https://cbridges1.github.io/hyve-website/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Features

- **GitOps Native** — All cluster state lives in Git. Every change is version-controlled, reviewed through pull requests, and rolled back with a revert commit.
- **Module System** — Modules are versioned packages for any cloud provider. Install once, reference from any template. No cloud SDKs bundled.
- **Lifecycle Hooks** — `beforeCreate`, `onCreate`, `afterCreate`, `onDelete`, `afterDelete` — run arbitrary workflows at each stage of a cluster's lifecycle, automatically.
- **Cluster Templates** — Define the shape of a cluster once; execute the template by name to stamp out clusters consistently.
- **Variable Injection** — Module params are injected as `HYVE_PARAM_*` env vars. Workflow outputs flow back as `HYVE_KEY=value` lines and are persisted for the next reconcile.
- **Any Provider** — First-party modules for Civo, AWS EKS, GCP GKE, and Azure AKS. Community modules for anything else. Your credentials stay in your environment.
- **Two Run Modes, One Engine** — `hyve reconcile` against a local/git directory, or deploy Hyve as a cluster-native controller + API (`deploy/helm/hyve`) for team/multi-tenant use — the same reconcile engine and the same YAML either way.

## Why Hyve?

**Git is the state backend.** No S3 bucket, no Terraform Cloud, no extra credentials — the full history of every cluster change is already in the repository.

**Continuous reconciliation, not plan-and-apply.** Commit to create, delete the file to destroy, update a field to reconcile the difference. The same `hyve reconcile` command handles all three cases.

**Lifecycle hooks are built in.** Five hook points cover the full cluster lifecycle:

| Hook           | Cluster Exists? | When It Runs |
|----------------|-----------------|--------------|
| `beforeCreate` | No              | Before provisioning — provision VPCs, IAM roles, etc. |
| `onCreate`     | Yes             | After the cluster is active, before `spec.resources` applies — deploy apps, configure monitoring |
| `afterCreate`  | Yes             | After the cluster is active and `spec.resources` has applied — anything depending on a resource-created object (e.g. a Secret referenced by a resource-managed Deployment) |
| `onDelete`     | Yes             | Before deletion — drain workloads, export backups |
| `afterDelete`  | No              | After deletion — destroy VPCs, release IPs, clean up roles |

**Modules, not embedded SDKs.** Old-style GitOps tools embed cloud SDKs. If your provider isn't supported, you're stuck. Hyve modules shell out to any CLI tool you already have configured — your credentials, your tools, your control.

## Documentation

Full documentation at **[cbridges1.github.io/hyve-website](https://cbridges1.github.io/hyve-website/)** — CLI reference, module authoring guide, and provider-specific walkthroughs.

## Installation

**Homebrew (macOS/Linux):**

```bash
brew install cbridges1/tap/hyve
```

**Binary download:**

Prebuilt binaries for macOS, Linux, and Windows (amd64/arm64) are attached to every [GitHub Release](https://github.com/cbridges1/hyve/releases).

```bash
curl -sL https://github.com/cbridges1/hyve/releases/latest/download/hyve_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz
sudo mv hyve /usr/local/bin/
```

**Docker:**

```bash
docker pull ghcr.io/cbridges1/hyve:latest
docker run --rm -v "$(pwd)":/repo ghcr.io/cbridges1/hyve:latest reconcile --path .
```

Requires Go 1.21+ and the system `git` binary in `PATH` for the options below.

**Using `go install`:**

```bash
go install github.com/cbridges1/hyve@latest
```

**From source (install to `$GOPATH/bin`):**

```bash
git clone https://github.com/cbridges1/hyve.git
cd hyve
go install .
```

Ensure `$(go env GOPATH)/bin` is in your `PATH`. This builds and installs the binary in one step with no manual copy required.

**From source (local binary):**

```bash
git clone https://github.com/cbridges1/hyve.git
cd hyve
go build -o hyve .
sudo mv hyve /usr/local/bin/
```

## Quick Start

```bash
# 1. Add a module
hyve module add github.com/hyve-modules/civo@v1.0.0

# 2. Point Hyve at a directory for state (a plain local directory works too —
#    git is entirely optional and, if used, is just your own 'git' CLI)
git clone https://github.com/company/hyve-state.git && cd hyve-state
hyve env create --path .

# 3. Create a template
hyve template create my-civo-template \
  --driver github.com/hyve-modules/civo \
  --driver-version v1.0.0 \
  --region PHX1 \
  --set node_size=g4s.kube.medium \
  --set node_count=3

# 4. Create a cluster from the template — writes a cluster YAML to your state directory
hyve cluster create my-cluster --template my-civo-template

# 5. Reconcile — provisions the cluster and runs any lifecycle hooks
hyve reconcile

# 6. Configure kubectl
hyve cluster auth my-cluster
kubectl get nodes

# 7. If your state directory is a git checkout, commit and push when you're ready —
#    Hyve writes files locally but never commits or pushes on its own
git add -A && git commit -m "add my-cluster" && git push
```

The resulting cluster YAML, written to your state directory:

```yaml
apiVersion: hyve.io/v1alpha1
kind: ClusterDefinition
metadata:
  name: my-cluster
spec:
  region: PHX1
  driver:
    source: github.com/hyve-modules/civo
    version: v1.0.0
  params:
    node_size: g4s.kube.medium
    node_count: "3"
  workflows:
    onCreate:
      - name: deploy-monitoring
    onDelete:
      - name: drain-workloads
```

This is real `ClusterDefinition` custom resource YAML — the same shape a Kubernetes cluster running hyve's controller uses, group `hyve.io/v1alpha1`. `kubectl apply -f clusters/my-cluster.yaml` works unmodified once that cluster's CRDs are installed; there's no separate "cluster mode" file format to convert to. Templates (`templates/*.yaml`, kind `Template`) and Workflows (`workflows/*.yaml`, kind `Workflow`) are the same story — every YAML file under your state directory is a real CR, usable locally or on-cluster without translation.

Cloud credentials are read directly from your environment — the same way the underlying CLI tools (`civo`, `aws`, `gcloud`, `az`) read them. Hyve never stores credentials.

## Environments: Local State vs. a Live Cluster

Hyve works two ways, sharing the exact same YAML:

- **Local mode** (above) — `hyve reconcile` reads `clusters/`, `templates/`, `workflows/` from the active **environment**'s directory and drives everything from your machine or CI. An environment (`hyve env`) is purely a named local directory, registered and switched with `hyve env create`/`hyve env use <name>`/`hyve env list` — nothing more.
- **Cluster mode** — deploy hyve's controller + API (`deploy/helm/hyve`, one Helm chart for both) onto a Kubernetes cluster and run `hyve login --api-url https://hyve-api.example.com`. Every `hyve cluster`/`template`/`workflow` command then talks to the API instead, which stores each resource as a real CR. The cluster's own controller reconciles `ClusterDefinition`s directly — no separate agent.

**Environments and login are two completely independent concepts.** `hyve env` only ever picks which local directory `reconcile` reads from (and/or which cluster API URL is "active" — see below); `hyve login` is a single, global, machine-wide credential — like `gh auth login` or `docker login` — that isn't scoped to whichever environment happens to be active. Switching environments never touches your login, and logging in/out never touches which environment is active:

```bash
hyve env create prod --path ~/repos/prod-config
hyve env create staging --path ~/repos/staging-config
hyve env use prod              # only switches which directory 'hyve reconcile' reads

hyve login --api-url https://hyve-api.example.com   # one login for the whole machine
hyve whoami                    # confirm who you're authenticated as, and where
hyve logout                    # revoke it
```

An environment can also just be a cluster API URL, with no local directory at all — `hyve env create` registers where to log in later, without authenticating on the spot or requiring you already be logged in:

```bash
hyve env create prod-cluster --api-url https://hyve-api.example.com
hyve env create staging-cluster --api-url https://hyve-api-staging.example.com
hyve env list                  # both show up as separate, first-class entries

hyve env use prod-cluster
hyve login                     # --api-url defaults to the active environment's, no need to repeat it
```

`--api-url` here only remembers the URL — it stores no credential and doesn't authenticate anything by itself. The actual login is still the one global session described above; registering a cluster environment and logging into it are two independently-timed steps.

`hyve login --api-url ...` also registers the environment for you automatically if that URL isn't already known — so a single `hyve login --api-url https://hyve-api.example.com` is enough on its own; a separate `hyve env create --api-url` step is only needed if you want to pre-register a cluster before authenticating against it. The auto-registered name is derived from the URL's host (deduplicated on collision), and it's only made the active environment if you had none registered yet — otherwise whatever local directory you're already working in stays active.

`hyve login` returns two credentials: a short-lived **access token** (30 minutes, used on every API call) and a long-lived **session token** (30 days, kept only to silently mint fresh access tokens via `POST /auth/refresh` — no password re-entry, which is what makes unattended use, e.g. a cron job, practical). The session itself is a real, revocable Kubernetes object (`HyveSession`, group `hyve.io`) on the cluster you logged into — `kubectl get hyvesessions -n hyve-system` lists every active login, and `hyve logout` (or deleting the object directly) revokes it immediately.

`hyve migrate` bulk-imports a directory into whichever cluster the active environment is logged into (workflows and templates first, then clusters, so lifecycle-hook references resolve correctly). Its source is always explicit — a positional path, or `--dir`/`--file` — defaulting to the current working directory, never implicitly the active environment's own directory (you might migrate a one-off directory into whatever cluster you're logged into). It's a dry run by default — pass `--write` to actually create resources; safe to re-run, since `--skip-existing` (on by default) treats an already-migrated resource as success.

`hyve apply -f <file>` creates a single resource, auto-detecting `kind` (ClusterDefinition/Template/Workflow) from the file — the single-file equivalent of `hyve cluster create --file`, `hyve template create --file`, or `hyve workflow create --file`, without needing to know which one matches a given file. Either way — `apply`, `migrate`, or the per-resource `--file` flags — the same file works: `kubectl apply -f` it directly, or hand it to the CLI.

### Multi-tenant installs

Multiple hyve installs (controller + API pairs) can safely share one Kubernetes cluster, each isolated to its own namespace — one install per tenant, rather than one shared controller watching many namespaces. Every `hyve.io` CRD is namespaced, and by default (`api.accessRoles.clusterScoped: false`) each install's admin/read-only roles are scoped to its own namespace only, so one tenant's caller can never read, modify, or gain cluster-admin over another tenant's objects or namespace.

To add a tenant:

```bash
kubectl create namespace <tenant-ns>
helm install hyve-<tenant> deploy/helm/hyve \
  --namespace <tenant-ns> \
  -f deploy/helm/hyve/values-tenant-example.yaml \
  --set namespace=<tenant-ns> \
  --set api.ingress.host=<tenant>.hyve.example.com

hyve cluster-config api create-user <username> --role admin --namespace <tenant-ns> | kubectl apply -f -
```

See `deploy/helm/hyve/values-tenant-example.yaml` for the full set of per-tenant overrides.

**CRDs are cluster-global, shared by every tenant install.** `helm install` only applies `deploy/helm/hyve/crds/` on a chart's first install in a cluster — `helm upgrade` never touches them (standard Helm behavior). So only the very first tenant's install actually creates them; a later CRD schema change needs a manual `kubectl apply -f deploy/helm/hyve/crds/` before any tenant runs `helm upgrade`, or that tenant's upgrade will run against a stale schema.

## Module System

Modules are directories containing operation files that Hyve executes during reconciliation:

| File | Operation |
|------|-----------|
| `status.sh` / `status.yaml` | Check if the cluster exists and return its state |
| `create.sh` / `create.yaml` | Provision the cluster |
| `delete.sh` / `delete.yaml` | Destroy the cluster |
| `auth.yaml` | Configure `~/.kube/config` for the cluster |
| `scale.sh` / `scale.yaml` | Adjust node count (optional) |

Operations emit outputs by printing `HYVE_KEY=value` lines to stdout. Hyve captures these and writes them back to `spec.driverOutputs` in the cluster YAML, making them available on every subsequent reconcile. `auth.sh`/`auth.yaml` follows the same contract but for kubeconfig: it prints `HYVE_KUBECONFIG_B64=<base64-encoded kubeconfig>`, which hyve decodes and writes locally — the script itself never touches the filesystem directly.

```bash
# Validate all modules in use are locked and cached
hyve module validate

# List installed modules
hyve module list

# Scaffold a new module
hyve module init my-provider
```

**Local mode** runs every module operation as an inline child process on your machine, using whatever cloud CLI tools (`civo`, `aws`, `gcloud`, `az`, ...) and credentials are already on your `PATH`/in your environment. **Cluster mode** instead dispatches each operation to a fresh, single-use Kubernetes `Job` — the image comes from the cluster's own `spec.runner.image` (inherited from its Template) or `HyveConfig.spec.defaultModuleImage` as a fallback — so the controller pod itself never needs those cloud CLIs installed. Either way, secrets set via `hyve env secrets set` (cluster mode) are fetched live on every reconcile and injected as env vars — no controller restart needed to pick up a changed or newly-set credential.

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
| `task cluster:local` | Create a local k3d cluster for hyve dev (run once) |
| `task install:local` | Build from source and install the controller + API onto it |
| `task test:concurrency` | Live smoke test for `--max-concurrent-reconciles` safety |
| `task test:secretsfrom` | Live smoke test for `runtime: client` workflows + `secretsFrom` |
| `task test:api` | Live smoke test for the API/auth layer (login, authz, CRUD) |

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how the codebase fits together, and [docs/TESTING.md](docs/TESTING.md) for the full testing guide, including the live cluster smoke tests above.
