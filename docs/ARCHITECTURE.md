# Architecture

This document explains how hyve's codebase fits together: the two deployment
modes it supports, the packages under `internal/`, and how a `ClusterDefinition`
actually gets provisioned. It's aimed at people modifying hyve itself, not at
module authors or end users — see the [module system](../README.md#module-system)
section of the README for that.

## Two modes, one engine

Hyve runs the same reconcile logic from two different entry points:

- **Local (CLI) mode** — `hyve reconcile` reads `ClusterDefinition`/`Template`/
  `Workflow` YAML files from a local directory (an "environment," see `hyve env`)
  and drives everything from the invoking machine or CI runner.
- **Cluster mode** — `deploy/helm/hyve` installs two long-running components,
  a controller and an API, onto a Kubernetes cluster. The same resources exist
  as real CRDs (`ClusterDefinition`, `Template`, `Workflow`, `HyveConfig`,
  `HyveSession`, `HyveAccessBinding`) rather than files, and the controller
  reconciles them continuously instead of on a CLI invocation.

Both modes funnel through the exact same `internal/reconcile.Reconciler` —
"same engine, different source of truth," not two implementations of hyve's
logic. What differs between modes is only:

1. **Where desired state comes from** — `internal/state` (files) vs.
   `internal/controller.CRDStateProvider` (live CRDs), both satisfying
   `internal/reconcile.StateProvider`.
2. **How module/workflow operations execute** — inline `os/exec` child
   processes locally, vs. dispatched to fresh, single-use Kubernetes `Job`s
   in cluster mode (see [Module and workflow execution](#module-and-workflow-execution)).

`Reconciler`'s `StepRunner`/`ModuleRunner` fields are `nil` unless explicitly
set — the CLI never sets them, so local mode is architecturally incapable of
depending on anything cluster-specific. `cmd/controller/run.go` is the only
caller that wires them to their cluster-mode implementations.

## Package map (`internal/`)

| Package | Responsibility |
|---|---|
| `types` | Shared in-memory shapes (`ClusterDefinition`, `Template`, `Workflow`, ...) that both file-based and CRD-based state ultimately produce. |
| `apis/hyve/v1alpha1` | The real Kubernetes CRD Go types (`ClusterDefinition`, `HyveConfig`, `HyveSession`, `HyveAccessBinding`, ...) — `+kubebuilder` markers here generate `deploy/helm/hyve/crds/*.yaml` via `controller-gen`. |
| `crdconv` | Converts between `apis/hyve/v1alpha1`'s CRD types and `types`' shared shapes. Sits below both in the import graph so neither `controller` nor `state` has to hand-roll its own conversion. |
| `state` | Local-file `StateProvider`: reads/writes `clusters/`, `templates/`, `workflows/` YAML under an environment's directory. |
| `controller` | The controller-runtime reconcile loop (`ClusterDefinitionReconciler`) — owns finalizers, status/conditions, and CRD-specific plumbing that the source-of-truth-agnostic `StateProvider` deliberately doesn't know about. Wraps `CRDStateProvider` (a `StateProvider` backed by live CRDs) around the shared `reconcile.Reconciler`. |
| `reconcile` | The engine itself: given a `StateProvider`, drives cluster create/update/delete, runs lifecycle-hook workflows at the right points, and persists `driverOutputs` back. Mode-agnostic — knows nothing about files or CRDs directly. |
| `module` | Resolves (`git clone`/cache, per `hyve.lock`), validates, and executes module operations (`status`/`create`/`delete`/`auth`/`scale`). `Executor.Runner`, when set, dispatches to `JobRunner` instead of running inline. |
| `workflow` | Resolves and executes lifecycle-hook and standalone workflows. `KubernetesJobStepRunner` is workflow's equivalent of `module.JobRunner`. |
| `k8sjob` | The one-shot `batch/v1.Job` lifecycle primitive shared by `module.JobRunner` and `workflow.KubernetesJobStepRunner` — create a Job with a given image/script/env, wait, capture combined stdout+stderr, report exit code, delete regardless of outcome. Extracted once because both callers need the identical operation. |
| `repository` | The environment registry (`hyve env`) — named entries of `{ID, Name, RepoURL, LocalPath, APIURL, IsCurrent, ...}`. `LocalPath` and `APIURL` are independent, optionally-both-set fields: a local directory, a cluster API URL to `hyve login` against later, or both. Stores no credential of any kind — `APIURL` is only ever a remembered target, never proof of authentication. |
| `session` | The CLI's single, machine-wide cluster-mode login (`hyve login`/`whoami`/`logout`) — deliberately independent of `repository`. See [Session and auth model](#session-and-auth-model). |
| `database` | SQLite-backed storage underneath `repository` and `session` (two separate tables; `repositories` and a singleton `session` row), local to the machine running the CLI — never touched by cluster mode's controller/API. |
| `api` | The HTTP API + auth layer cluster mode exposes (`cmd/api`) — a thin, authorized front door onto the CRDs the controller already reconciles, not a second implementation of hyve's logic. Plain `kubectl` against the CRDs always works without it. |
| `secretsfrom` | Resolves a workflow's or module operation's `spec.secretsFrom` references (a Kubernetes Secret on some already-managed cluster) into env vars. Deliberately has no dependency on `module` or `workflow`, so both can share it without creating an import cycle. |
| `kubeconfig` | Per-cluster kubeconfig file path/write helpers used by `module.Executor`'s `auth` handling, in both modes. |
| `resourceref`, `workflowref` | Small reference-resolution helpers (`spec.resources`, workflow name lookups) shared across packages. |
| `template` | `Template` CR rendering — expands a Template + params into a concrete `ClusterDefinition`. |

## `cmd/` layout

- Top-level one-shot commands (`reconcile`, `login`/`logout`, `whoami`,
  `apply`, `migrate`) plus resource-group subcommands (`cluster`, `template`,
  `workflow`, `module`, `env`) — the everyday CLI surface, listed at
  `hyve --help`.
- `cmd/clusterconfig` groups the two long-running, Helm-deployed processes
  (`cmd/api`, `cmd/controller`) under `hyve cluster-config ...` — a different
  kind of command (a server that runs inside a pod) from everything else,
  kept out of the everyday `--help` surface.
- `cmd/shared` holds cross-cutting CLI concerns: the API client
  (`apiclient.go`), the local/cluster mode branch every resource command
  makes (`UseClusterMode`), session loading + silent refresh (`session.go`),
  and `hyve env secrets` loading (`envsecrets.go`).

## Session and auth model

Cluster-mode login is modeled after [better-auth](https://www.better-auth.com/)'s
approach rather than classic independent OAuth2 access+refresh tokens: **one
stateful, revocable session is the source of truth**, with a short-lived
signed token as a fast-path cache in front of it — not two independent
credentials.

- **Access token** — 30 minutes (`api.AccessTokenTTL`), stateless, HMAC-signed
  (a custom non-JWT format — deliberately, to avoid a JWT library dependency
  for a case this simple), verified locally by the API with no Kubernetes
  round trip on the hot path.
- **Session token** — 30 days (`api.SessionTTL`), the credential presented to
  `POST /auth/refresh` to silently mint a new access token, no password
  needed. Shape: `"<HyveSession object name>.<raw secret>"`. Backed by a real
  `HyveSession` custom resource in the cluster — `kubectl get hyvesessions`
  lists every active login. Only `hex(SHA-256(secret))` is stored on the
  object (`spec.tokenHash`); the raw secret itself is never persisted, so
  read access to the CR alone can never reconstruct a working credential.
  Not rotated on refresh — it stays valid until its own expiry or an
  explicit `hyve logout` (which deletes the `HyveSession`, revoking it
  immediately; the still-cached access token keeps working for at most its
  own short TTL after that).

Precedent for storing this kind of state as a CRD rather than a new database
table: Dex's `--storage kubernetes` backend and Rancher's own
`management.cattle.io/v3` `Token` resource both keep their auth state as
cluster-native objects. Hyve-api has no SQL database of its own — Kubernetes
is already the persistence layer everywhere else in this codebase, so this
follows the same pattern rather than introducing a new kind of storage.

`cmd/shared.EnsureValidSession` is the one place every CLI command goes
through: if the cached access token is still valid, use it; if it's expired
but the session isn't, silently refresh; if the session itself has expired,
return an error the caller decides how to handle (`UseClusterMode` treats it
as fatal — refusing to silently fall back to local file operations against a
cluster-mode environment — while `LoadEnvironmentSecrets` treats it as safely
ignorable, since background secret-loading must never abort a command).

Environments (`hyve env`) and this session are stored, and selected,
completely independently — see the README's
[Environments](../README.md#environments-local-state-vs-a-live-cluster)
section for why that separation matters. An environment's `APIURL` (when
set) is purely a remembered target for `hyve login --api-url` to default
from — registering a cluster environment and authenticating against it are
deliberately two separate, independently-timed actions, not one combined
step the way the original (pre-fix) design conflated them.

`cmd.ensureClusterEnvironmentRegistered` (`cmd/login.go`) auto-registers an
environment for `apiURL` after a successful login if no existing one
already has it — matching on `APIURL` across the registry first, so
logging in twice against the same URL never creates a duplicate. It only
writes the URL, the same as an explicit `hyve env create --api-url` would —
never touches `internal/session`'s storage, and only changes which
environment is *current* when the registry was completely empty beforehand
(first-ever login on a fresh machine); an existing active local directory
is left alone. This is what makes `hyve env list` reflect every cluster
you've ever logged into without a separate registration step, while still
keeping the credential itself (which environment is a URL vs. who's
authenticated to it) in two independent places.

## Multi-tenant installs

Multiple hyve installs (controller + API pairs) can share one cluster, each
scoped to its own namespace — see the README's
[Multi-tenant installs](../README.md#multi-tenant-installs) section for the
operator-facing walkthrough. Two things make this actually safe rather than
just namespace-flavored:

- `HyveAccessBinding` is namespaced (not cluster-scoped, as it originally
  was) — `FindBindingBySubject` (`internal/api/identity.go`) always lists
  within `Server.Namespace`, so one install can never see or resolve another
  install's identities, and the backing RBAC is a `Role`, not a `ClusterRole`
  (`deploy/helm/hyve/templates/api-rbac.yaml`).
- The default `admin`/`read-only` roles bind to the built-in
  `cluster-admin`/`view` `ClusterRole`s via a namespaced `RoleBinding`, not a
  `ClusterRoleBinding` (`api.accessRoles.clusterScoped: false`, the chart
  default) — so a kubeconfig minted through `PrimaryClusterProvider`
  (`internal/api/access.go`, served via `/proxy`) only grants admin/view over
  that install's own namespace, never the whole shared cluster.

**CRD scope is a one-time, breaking migration for any cluster still running
the pre-multi-tenancy, cluster-scoped `HyveAccessBinding`.** Kubernetes
can't change a CRD's scope in place: existing bindings must be exported, the
old CRD deleted (which deletes every instance), the new namespaced CRD
applied, then each binding re-applied with `namespace:` set. Do this before
any tenant install relies on `HyveAccessBinding` namespacing for isolation.

## Module and workflow execution

Both module operations (`create`/`status`/`delete`/`auth`/`scale`) and
lifecycle-hook/standalone workflow steps follow the identical dispatch
pattern:

- **Local mode**: run inline as an `os/exec` child process on the machine
  running `hyve reconcile`, inheriting its environment and whatever cloud
  CLIs are on `PATH`.
- **Cluster mode**: dispatched to a fresh, single-use `batch/v1.Job` via
  `internal/k8sjob.Run` — build the Job with the resolved image/script/env,
  wait for completion, capture combined stdout+stderr, report the exit code,
  delete the Job regardless of outcome. The controller pod itself never
  needs cloud CLIs installed.

Image resolution is a two-tier fallback in both `module.Executor` and
`workflow.Executor`: an explicit per-cluster/per-step image first (e.g.
`ClusterDefinition.spec.runner.image`, inherited from a Template at creation
time), falling back to `HyveConfig.spec.defaultModuleImage` /
`defaultWorkflowImage` read once at controller startup. A module's own
`module.yaml` can *recommend* an image (`spec.runner.image`, resolved and
locked into `hyve.lock`) but doesn't get to unilaterally choose one — the
same module may need different images across different deployments.

`auth.sh`/`auth.yaml` has one contract regardless of mode: it prints
`HYVE_KUBECONFIG_B64=<base64 kubeconfig>` to stdout rather than writing a
kubeconfig file directly. `Executor` decodes and writes it locally — this is
what makes the same script work whether it ran inline (same filesystem) or
inside an ephemeral Job pod (no shared filesystem with the caller at all).

`hyve env secrets` values (cluster mode) are stored in a single shared
`hyve-cli-secrets` Kubernetes Secret and fetched live, once per reconcile —
never cached in the controller's process environment — so a changed or
newly-set secret takes effect on the very next reconcile, no controller
restart required. This reaches both module-Job env and workflow-Job env
through the same `env []string` already threaded through
`reconcile.Reconciler`; `GITHUB_TOKEN` specifically is passed as an explicit
function parameter through module resolution (`resolveGit`/`ResolveRef`)
rather than `os.Setenv`, since concurrent reconciles of different clusters
share one process and a mutated global env var would race.

## A reconcile, end to end (cluster mode)

1. `ClusterDefinitionReconciler.Reconcile` fires (a CR changed, or the
   5-minute `resyncInterval` elapsed).
2. `CRDStateProvider` fetches the `ClusterDefinition` and related `Template`/
   `Workflow` CRs via the controller-runtime client; `crdconv` converts them
   to `internal/types` shapes.
3. `fetchCLISecrets` does a live, uncached read of `hyve-cli-secrets`
   (`mgr.GetAPIReader()` — see the RBAC note in
   `deploy/helm/hyve/templates/controller-rbac.yaml` on why this read is
   deliberately uncached).
4. `reconcile.Reconciler` runs the module's `status` operation to check
   current state, then `create`/`delete`/no-op as needed, running
   `beforeCreate`/`onCreate`/`afterCreate`/`onDelete`/`afterDelete` workflows
   at the appropriate points — each module/workflow execution dispatched as
   its own `k8sjob.Run`-backed Job.
5. Outputs (`HYVE_KEY=value` lines from module stdout) and workflow outputs
   are written back to `spec.driverOutputs`/status; finalizer bookkeeping
   and conditions are updated by the `controller` package layer.

The exact same steps 2–5 happen for `hyve reconcile` in local mode, with
`state.LocalStateProvider` and inline `os/exec` in place of steps 2 and 4's
CRD/Job-specific mechanics.
