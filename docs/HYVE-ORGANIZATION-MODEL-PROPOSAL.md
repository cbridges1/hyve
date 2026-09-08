# Proposal: organizations with multiple environments

**Status:** draft, not implemented. Written to capture a design direction
worked out in conversation, for discussion before any code changes.
Separate from, but referenced by,
[`HYVE-AGENT-ARCHITECTURE-PROPOSAL.md`](./HYVE-AGENT-ARCHITECTURE-PROPOSAL.md) —
that doc's "Multi-tenancy scoping of the connection registry" section
assumes today's flat namespace-per-tenant model and will need a follow-up
pass once this one is settled (see "Downstream effect on the agent
proposal" at the end).

## Summary

Restructure hyve's tenancy model from today's flat "one `HyveEnvironment` =
one namespace = one tenant" into two levels:

- **Organization** — the hard multi-tenant isolation boundary, still one
  Kubernetes namespace, same cost profile as today's model (no
  multiplication). This is today's `HyveEnvironment` CRD, renamed.
- **Environment** — a named sub-scope *within* an organization's namespace
  (`dev`, `staging`, `production`, or whatever an org chooses to call
  them), distinguished by a label rather than a separate namespace, with
  per-environment RBAC enforced in hyve's own API authorization layer
  rather than native Kubernetes RBAC (which has no label-scoped
  primitive). A new, namespaced-within-the-org CRD.

Also, unrelated to the tenancy model but decided in the same conversation:
remove `cmd/api`/`cmd/controller` from the `hyve` CLI's own command tree —
ship them as standalone binaries instead of `hyve cluster-config api
run`/`hyve cluster-config controller run`, matching the pattern already
being established for `hyve-agent` (its own top-level binary, not nested
under the CLI).

## Why now

Today, `HyveEnvironment` (`internal/apis/hyve/v1alpha1/hyveenvironment_types.go`)
already conflates three things into one object: tenant identity, namespace,
and "environment." There's no way for one customer to have `acme-dev` and
`acme-prod` as related-but-separate spaces — every tenant gets exactly one
namespace, full stop. That's a real gap once "staging/dev/production" is a
stated goal.

## Decisions already made, with the reasoning

This section exists because the alternative (namespace-per-environment)
was seriously considered and rejected — worth recording why, not just the
conclusion.

**Namespace-per-organization, not namespace-per-environment.** The
straightforward reading of "organizations have multiple environments"
would give each environment its own namespace (`acme-dev`, `acme-staging`,
`acme-prod` as three real namespaces). That gives the strongest possible
isolation — a Kubernetes namespace boundary is real, not a convention — but
it multiplies namespace count by (orgs × environments), which is a direct,
material cost problem for a shared hosted control plane: more namespaces
means more etcd/API-server load, more RBAC objects, more of everything
`handleCreateEnvironment`'s three provisioning steps
(`ensureNamespace`/`ensureAccessRoleScaffolding`/`ensureHyveEnvironment`)
already do, N times over per org instead of once. Rejected specifically
*because* hyve intends to offer a hosted option — the cost scales with the
thing a hosted provider pays for directly.

**Environments are hyve-API-enforced, not Kubernetes-RBAC-enforced.**
Kubernetes RBAC has no label-scoped primitive — you cannot bind a Role to
"objects with label `environment: dev`" natively. Real per-environment
RBAC (grant a contractor `dev`-only access) is only achievable inside
hyve's own authorization code, checking a caller's environment-scoped
grant against an object's environment label before allowing the request —
the same place `TenantNamespace` already does its own check today, one
level finer. This is honest about what it does and doesn't give you (see
"Honest limitations" below) rather than pretending it's equivalent to a
real namespace boundary.

**Naming collisions across environments are avoided by decoupling the
user-facing name from the real Kubernetes object name**, not by any
namespace trick. Kubernetes' own uniqueness constraint operates on
`metadata.name` within a namespace — since environments now share one
namespace, two clusters both named `web` (one in `dev`, one in `prod`)
would otherwise collide at the Kubernetes level. Resolution:

- The real `metadata.name` is a deterministic, hyve-computed join:
  `<environment>-<name>` (`dev-web`, `prod-web`) — what's actually stored
  in Kubernetes.
- Every hyve-facing surface (CLI, API responses, the web console) only
  ever shows and accepts the **short name** (`web`) plus whichever
  environment it's scoped to — never the qualified internal name.
  Uniqueness is checked on the pair `(environment, short name)`, not the
  raw namespace-wide name.
- **The environment is always known from context, never reverse-parsed
  out of an arbitrary `metadata.name`.** Splitting `dev2-web` back into
  `(environment, name)` without already knowing which environment you're
  asking about is genuinely ambiguous — is it environment `dev2` name
  `web`, or environment `dev` name `2-web`? The join is one-directional
  (environment + name → real name), used only for construction and
  lookup, never for blind parsing. This sidesteps the ambiguity
  structurally instead of needing a reserved-separator/escaping scheme to
  prevent it.

**`cmd/api`/`cmd/controller` come out of the CLI's command tree.** Not
architecturally required to live there — `cmd/clusterconfig` is just cobra
wiring nesting two independent packages under the `hyve` binary's own
subcommand tree. Reasons to actually make the change, not just note it's
possible: the installed CLI binary (~79MB) bundles all of the API/
controller's own dependencies (controller-runtime, the heavier client-go
server-side pieces) that a normal `hyve cluster create` user never
touches; running server code inside the same binary every developer's
laptop installs is a mild but real attack-surface concern; and `cmd/agent`
is already being built as its own standalone binary, so leaving `cmd/api`/
`cmd/controller` nested under `hyve cluster-config` while `cmd/agent`
stands alone is an inconsistency worth cleaning up, not preserving.
`docs/ARCHITECTURE.md`'s own existing comment ("kept out of the everyday
`--help` surface... a different kind of command... from everything else")
already half-admits this; this follows it to its conclusion.

## Honest limitations

- **Anything that bypasses hyve's own API sees every environment in an
  org's namespace.** Raw `kubectl` access with a broad enough credential —
  notably, an org's own `hyve-access-admin` ServiceAccount, which is
  still bound to the built-in `cluster-admin` `ClusterRole` *within that
  namespace* per `api-access-roles.yaml`'s existing pattern — can read/
  write across every environment regardless of any hyve-level
  environment-scoped binding. The environment boundary is enforced by
  hyve's own code path, not Kubernetes' own admission/RBAC layer. This
  needs to be documented plainly wherever environments are explained to a
  user, not discovered the hard way.
- **A bug in hyve's own environment-check is a real cross-environment
  leak**, in a way a genuine namespace boundary can't be bypassed by a
  bug in application code. This is the direct cost of choosing the
  cheaper model — worth naming, not glossing over.
- **Migration cost for existing tenants.** Every existing `HyveEnvironment`
  object (there's exactly one real one as of this writing — `acme`) needs
  renaming to `HyveOrganization`, and needs at least one default
  `HyveEnvironment` (the new meaning) created within its namespace so
  existing `ClusterDefinition`s don't end up with no environment scope at
  all once the field becomes meaningful.

## Terminology and CRD changes

Renaming an existing, real CRD type is the one piece of this with the
most blast radius — worth being explicit about exactly what changes:

| Today | Becomes |
|---|---|
| `HyveEnvironment` (`internal/apis/hyve/v1alpha1/hyveenvironment_types.go`) — one per tenant, lives in `hyve-system`, `spec.namespace` names the tenant's own namespace | `HyveOrganization` — same object, same field, renamed. `POST /environments` becomes `POST /organizations`. |
| *(nothing — doesn't exist today)* | `HyveEnvironment` (new meaning) — namespaced *within* an org's own namespace (e.g., lives in `acme`, not `hyve-system`), one per named environment (`dev`/`staging`/`production`). `metadata.name` is the short environment name. |

New fields on the five environment-scoped resource types
(`ClusterDefinition`, `Template`, `Workflow`, `Resource`, `AccessMethod`):
a `hyve.io/environment: <short-name>` label, plus the real `metadata.name`
computed as `<environment>-<name>` per the naming section above.

`HyveAccessBindingSpec` (`internal/apis/hyve/v1alpha1/hyveaccessbinding_types.go`)
gains an optional `Environment string` field — empty means "every
environment in this org" (the superset case, matching today's behavior
exactly for anyone who never uses the new scoping), a set value means
"only this environment." `RequireRole`-equivalent authorization logic
gains an environment check alongside the existing role check, for every
handler that touches an environment-scoped resource type.

## CLI/API surface

- `--org <name>` keeps its existing meaning (`cmd/shared.ResolveOrgToNamespace`'s
  job is unchanged: org name → namespace).
- A new `--env <name>` selects the environment within that org — needed
  everywhere the five resource types are addressed (`hyve cluster create`,
  `hyve cluster show`, etc.). Needs a sensible default so single-environment
  orgs (the common case, especially early on) don't need to pass it every
  time — likely "the org's only environment if it has exactly one" or an
  explicit "default" environment created automatically alongside a new
  org, mirroring how `POST /organizations` already provisions the
  namespace/RBAC scaffolding as one step.
- The web console needs an environment switcher alongside (or folded
  into) the existing `EnvironmentSwitcher`/act-as dropdown built earlier
  this session — that component already establishes the right UI pattern
  (a persistent, sidebar-top selector), it just needs a second axis.

## Downstream effect on the agent proposal

`HYVE-AGENT-ARCHITECTURE-PROPOSAL.md`'s "Multi-tenancy scoping of the
connection registry" section currently keys the connection registry by
`(namespace, clusterName)` and resolves scoping via `TenantNamespace(r)`
alone. Once this proposal lands, both need a second axis: the registry
key becomes `(namespace, environment, clusterName)` (matching the same
naming-collision reasoning above — two clusters named `web` in different
environments need distinct agent connections too), and the proxy handler's
existence check needs to resolve environment the same way it resolves
`TenantNamespace` today. Flagging this now rather than leaving the agent
doc silently stale; the actual edit is a follow-up once this proposal's
shape is confirmed, not part of this doc.

## Open questions

- Does creating an organization always provision a default environment (so
  a brand-new org is immediately usable without a separate "create my
  first environment" step), or is environment creation always a distinct,
  explicit action?
- What happens to `HyveAccessBinding`s with no `Environment` set once
  finer-grained bindings start being created for the same org — does an
  org-wide binding and an environment-scoped binding for the same identity
  need an explicit precedence rule, or are they additive (union of
  permissions)?
- Migration mechanics for the `HyveEnvironment` → `HyveOrganization` rename
  — an in-place CRD rename isn't possible in Kubernetes any more than a
  scope change is (same constraint this session's earlier multi-tenancy
  plan already hit for `HyveAccessBinding`'s scope change) — this likely
  needs the same export/delete-old-CRD/reapply-under-new-name migration
  shape.
