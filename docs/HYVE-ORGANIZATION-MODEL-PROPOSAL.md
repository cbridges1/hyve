# Proposal: organizations with multiple environments

**Status:** draft, not implemented. Written to capture a design direction
worked out in conversation, for discussion before any code changes.
Separate from, but referenced by,
[`HYVE-AGENT-ARCHITECTURE-PROPOSAL.md`](./HYVE-AGENT-ARCHITECTURE-PROPOSAL.md) —
that doc's "Multi-tenancy scoping of the connection registry" section
assumes today's flat namespace-per-tenant model and will need a follow-up
pass once this one is settled (see "Downstream effect on the agent
proposal" at the end).

**Revision (2026-09-08):** the original draft put the new `HyveEnvironment`
and the environment-scoped half of `HyveAccessBinding` in etcd, as CRDs,
alongside everything else. That's been superseded by the persistence split
described in "Persistence split: Postgres for environment and RBAC data"
below — environments and access bindings move to Postgres instead of
etcd. The reasoning that led to namespace-per-organization (not
namespace-per-environment) is unchanged and still the load-bearing
decision; this revision only changes *where the environment/RBAC ledger
lives*, not the namespace model itself. The terminology table, CLI/API
surface section, and open questions below have been updated to match;
sections not called out as revised still reflect the original draft.

**Revision, continued (same day):** the organization record moves to
Postgres too, not just environments/bindings — the first pass of this
revision kept `HyveOrganization` as a CRD on the reasoning that "the
controller resolves namespaces during reconciliation and must not gain a
Postgres dependency." That reasoning doesn't actually hold up: the
controller reconciles `ClusterDefinition`/`Workflow`/etc. objects that are
already sitting in a specific namespace — it never needs to look up "what
org owns this namespace" to do that, so it was never truly coupled to
`HyveOrganization` in the first place. The real argument for moving
organizations to Postgres is referential integrity: environments already
carry a foreign key to their parent organization, and a foreign key
pointing from a Postgres row into an etcd CRD can't be enforced by
Postgres, can't share a transaction with the environment/binding writes it
sits next to, and forces anything needing both (a dashboard listing orgs
with their environments) to query two systems and stitch the join by
hand. See "Persistence split" and "Honest limitations" below for what
this does and doesn't change — notably, the raw Kubernetes `Namespace`
object itself is unaffected and stays fully K8s-native either way; only
the *business record* of the organization (name, plan/metadata, the
namespace-name mapping) moves.

## Summary

Restructure hyve's tenancy model from today's flat "one `HyveEnvironment` =
one namespace = one tenant" into two levels:

- **Organization** — the hard multi-tenant isolation boundary, still one
  Kubernetes namespace, same cost profile as today's model (no
  multiplication). The namespace itself stays a real, K8s-native
  `Namespace` object — that isolation primitive can't move anywhere else.
  The *business record* of the organization (name, plan/metadata, the
  org-id ↔ namespace-name mapping) is a Postgres row, not a CRD — the
  `HyveEnvironment` CRD this replaces is retired rather than renamed. See
  the second revision note above.
- **Environment** — a named sub-scope *within* an organization's namespace
  (`dev`, `staging`, `production`, or whatever an org chooses to call
  them), distinguished by a label on the underlying resources rather than
  a separate namespace, with per-environment RBAC enforced in hyve's own
  API authorization layer rather than native Kubernetes RBAC (which has no
  label-scoped primitive). As of this revision, an environment is a
  **Postgres row**, not a CRD — see below.

Also, unrelated to the tenancy model but decided in the same conversation:
remove `cmd/api`/`cmd/controller` from the `hyve` CLI's own command tree —
ship them as standalone binaries instead of `hyve cluster-config api
run`/`hyve cluster-config controller run`, matching the pattern already
being established for `hyve-agent` (its own top-level binary, not nested
under the CLI). This revision extends that split further: the two
binaries are also meant to be deployed and scaled independently, not just
built independently — see "Deployment topology" below.

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

**Environment and RBAC data live in Postgres, not etcd.** *(Added in this
revision.)* See the dedicated section below — this is substantial enough
to warrant its own writeup rather than a bullet here.

## Persistence split: Postgres for environment and RBAC data

*(Added in this revision.)*

The original draft made `HyveEnvironment` (new meaning) and the
environment-scoped half of `HyveAccessBinding` CRDs, living in etcd
alongside the actual provisioning objects. On reflection, nothing about
*reconciliation* ever needs to know about environments or RBAC —
the controller reconciles a `ClusterDefinition` toward real infrastructure
regardless of who created it or what environment it's tagged with.
Authorization is purely a request-time gate in the API server, which the
original draft already located there ("the same place `TenantNamespace`
already does its own check today"). Since the enforcement point was
always the API layer and never the controller, that layer can be backed
by Postgres instead of etcd-backed CRDs at zero cost to the controller,
which stays exactly as pure and Kubernetes-only as it is today.

**What moves to Postgres:**

- Organizations — name, plan/metadata, and the org-id ↔ namespace-name
  mapping. `ResolveOrgToNamespace` becomes a single Postgres lookup
  instead of a Kubernetes API round-trip (or a client-side informer cache
  of `HyveOrganization` objects) on every request — a meaningful latency
  win given this resolution happens on essentially every API call.
- Environments (name, description, per-org uniqueness, any quotas/limits
  an org sets per environment) — a child row with a real, enforceable
  foreign key onto its parent organization row.
- `HyveAccessBinding`-equivalent grants — identity, role, and the optional
  environment scope described in the original draft's "Terminology and
  CRD changes" section, now rows instead of a CRD.
- The precedence question the original draft left open (an org-wide grant
  and an environment-scoped grant for the same identity — union or
  override?) becomes a straightforward query/join instead of a
  hand-rolled comparison across two CRD-derived lists.
- Because organizations, environments, and bindings are now one schema,
  "create org + default environment + initial admin grant" can be one
  atomic Postgres transaction instead of a CRD write plus a separate,
  non-transactional Postgres write.

**What stays Kubernetes-native:**

- The raw `Namespace` object per organization — this is the actual
  isolation primitive and was never a data record to begin with; it
  doesn't move regardless of where the organization's business metadata
  lives.
- The five environment-scoped resource types (`ClusterDefinition`,
  `Template`, `Workflow`, `Resource`, `AccessMethod`) — these are the
  actual infrastructure-shaped objects the controller reconciles, and the
  only Kubernetes objects the controller ever reads. They keep the
  `hyve.io/environment: <short-name>` label from the original draft,
  purely as a tag for filtering/display/reconciliation-time lookups — the
  label's presence doesn't require a corresponding `HyveEnvironment`
  object to exist in etcd any more; Postgres is now the sole source of
  truth for which environment names are valid and who can use them,
  checked by the API server before it ever writes the label onto an
  object.
- Notably, there is no `HyveOrganization` CRD left at all in this
  revision — the controller's only relationship to "organizations" is
  reconciling resources that happen to live in a given namespace; it
  never needs org identity, plan, or metadata to do that, so it was never
  really coupled to the CRD version of that object in the first place.

**Why this is a strict improvement on the cost problem that drove the
namespace-per-organization decision above:** even the label-based CRD
approach still added one etcd object per environment and per binding, so
an org that made heavy use of environments/grants still grew hyve's own
etcd object count and watch load. Moving that ledger — organizations
included — to Postgres decouples "how much RBAC/environment richness a
customer wants" from "how much load that puts on hyve's own Kubernetes
control plane" — arbitrarily many organizations, environments, or
bindings now cost nothing in etcd, only Postgres rows, which is a
fundamentally cheaper, more scalable, and more queryable place for
relationally-shaped business data to live than etcd ever was.

**What this does *not* change:** the isolation gap in "Honest limitations"
below is unaffected by this revision. Moving the RBAC ledger to Postgres
makes it cheaper to operate and easier to query — it adds no
Kubernetes-native boundary. Raw `kubectl` access to an org's namespace
(notably the org's own `hyve-access-admin` ServiceAccount,
cluster-admin-scoped within that namespace) still sees every
environment's resources regardless of what Postgres says, exactly as
before. Do not describe this revision as closing that gap when
documenting it for users.

**What this newly costs, now that organizations are included:** with
`HyveOrganization` as a CRD, deleting an organization got Kubernetes'
native finalizer/garbage-collection machinery for cascade-delete close to
free — the CRD's own deletion could be held open until a controller
confirmed the namespace and everything in it was actually gone. With the
organization as a Postgres row, that ordering has to be hand-rolled:
mark the row `pending_deletion`, orchestrate namespace teardown through
the API server, confirm the namespace has actually finished terminating,
then delete the row. This is a small, deletion-specific state machine,
not a general reconciler — but it's real orchestration logic hyve now
owns that it didn't have to write before. It's the same category of
partial-failure risk `handleCreateEnvironment`'s three-step provisioning
sequence already accepts on the creation side today, just newly present
on the deletion side too — worth building deliberately (see "Open
questions"), not assumed away.

## Deployment topology

*(Added in this revision.)*

The API server and controller are meant to be deployed as independently
scaled processes, not just built as independent binaries (the original
draft's `cmd/api`/`cmd/controller`-out-of-the-CLI decision covers building
them separately; this extends it to how they run in production):

- **API server** — stateless, Postgres-backed for everything in the
  "moves to Postgres" list above, horizontally scalable behind normal
  autoscaling. Its read/write-heavy CRUD workload (auth checks, RBAC
  lookups, environment queries, dashboard traffic) has ordinary
  stateless-app scaling characteristics once it's not bottlenecked by
  Kubernetes API server list/watch semantics for that data.
- **Controller** — unchanged from today: leader-elected, low replica
  count, talks only to the Kubernetes API, never touches Postgres. It has
  no relationship to organizations as business entities at all now — its
  entire job is reconciling the five environment-scoped resource types
  wherever they live. Its scaling characteristics (reconciliation
  throughput, work queue depth) are unrelated to the API server's, which
  is the actual operational argument for running them as separate
  deployments rather than one process wearing two hats.

**Self-hosted installs:** a hosted, multi-tenant deployment of the API
server needs real Postgres (backups, HA, the usual). A self-hosted,
single-tenant install doesn't need to pay that operational cost — the CLI
already embeds `modernc.org/sqlite` (`internal/database`) for its own
local state, so the same schema running against SQLite instead of
Postgres is a reasonable default for self-hosted, with Postgres reserved
for the deployment topology that actually needs concurrent multi-writer
access and horizontal API-server scaling.

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
  user, not discovered the hard way. *(Unchanged by the Postgres
  revision above — see that section's closing paragraph.)*
- **A bug in hyve's own environment-check is a real cross-environment
  leak**, in a way a genuine namespace boundary can't be bypassed by a
  bug in application code. This is the direct cost of choosing the
  cheaper model — worth naming, not glossing over.
- **Migration cost for existing tenants.** Every existing `HyveEnvironment`
  object (there's exactly one real one as of this writing — `acme`) gets
  retired, not renamed: its data becomes a Postgres `organizations` row
  (referencing the namespace that already exists), plus at least one
  default environment row so existing `ClusterDefinition`s don't end up
  with no environment scope at all once the field becomes meaningful.
- **Postgres is a new stateful dependency for the server side.** Confirmed
  the API/controller have no database dependency today — this is the
  first one. It needs a backup/HA story before it's load-bearing for the
  hosted offering, the same precondition already applied elsewhere before
  adding new critical stateful stores.
- **Organization deletion loses Kubernetes' native cascade-delete
  guarantee.** Now that the organization record lives in Postgres rather
  than as a CRD with a finalizer, hyve owns the ordering of "delete the
  org row → tear down the namespace → confirm it's gone" itself. A crash
  or bug partway through that sequence can leave a namespace orphaned
  (deleted row, namespace still around) or a Postgres row pointing at a
  namespace that's half-terminated, in a way Kubernetes' own garbage
  collection couldn't leave things if the org were still a CRD.

## Terminology and CRD changes

Renaming an existing, real CRD type is the one piece of this with the
most blast radius — worth being explicit about exactly what changes:

| Today | Becomes |
|---|---|
| `HyveEnvironment` (`internal/apis/hyve/v1alpha1/hyveenvironment_types.go`) — one per tenant, lives in `hyve-system`, `spec.namespace` names the tenant's own namespace | Organization — a Postgres row, not a CRD (revised twice now: the original draft renamed this CRD to `HyveOrganization`; this revision retires the CRD entirely). Holds name, plan/metadata, and the org-id ↔ namespace-name mapping. `POST /environments` becomes `POST /organizations`, now backed by Postgres. The underlying `Namespace` object itself is still created and still real K8s infrastructure — only the CRD wrapper around it is gone. |
| *(nothing — doesn't exist today)* | Environment (new meaning) — a Postgres row scoped to an org via a real foreign key, not a CRD (revised from the original draft, which made this a namespaced-within-the-org CRD). Holds the short environment name (`dev`/`staging`/`production`) and whatever quotas/metadata an org sets. |

New fields on the five environment-scoped resource types
(`ClusterDefinition`, `Template`, `Workflow`, `Resource`, `AccessMethod`):
a `hyve.io/environment: <short-name>` label, plus the real `metadata.name`
computed as `<environment>-<name>` per the naming section above. These
stay CRDs, unaffected by the persistence split — only the object that
*defines which environment names are valid and who can use them* moved to
Postgres, not the resources tagged with the label.

`HyveAccessBinding` (`internal/apis/hyve/v1alpha1/hyveaccessbinding_types.go`)
is superseded by a Postgres table for the environment-scoped case
described in the original draft — a grant row carries identity, role, and
an optional environment scope; no environment scope means "every
environment in this org" (the superset case, matching today's behavior
exactly for anyone who never uses the new scoping). `RequireRole`-
equivalent authorization logic gains an environment check alongside the
existing role check, for every handler that touches an environment-scoped
resource type, now querying Postgres instead of listing CRDs.

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
- See "Deployment topology" above for how the API server and controller
  are deployed and scaled once environment/RBAC data lives in Postgres.

## Downstream effect on the agent proposal

`HYVE-AGENT-ARCHITECTURE-PROPOSAL.md`'s "Multi-tenancy scoping of the
connection registry" section currently keys the connection registry by
`(namespace, clusterName)` and resolves scoping via `TenantNamespace(r)`
alone. Once this proposal lands, both need a second axis: the registry
key becomes `(namespace, environment, clusterName)` (matching the same
naming-collision reasoning above — two clusters named `web` in different
environments need distinct agent connections too), and the proxy handler's
existence check needs to resolve environment the same way it resolves
`TenantNamespace` today — as of this revision, that resolution is a
Postgres-backed authorization check made through the API server, not a
CRD lookup, but the shape of the change to the agent proposal is
otherwise unchanged. Flagging this now rather than leaving the agent doc
silently stale; the actual edit is a follow-up once this proposal's shape
is confirmed, not part of this doc.

## Open questions

- Does creating an organization always provision a default environment (so
  a brand-new org is immediately usable without a separate "create my
  first environment" step), or is environment creation always a distinct,
  explicit action?
- What happens to grants with no environment scope set once finer-grained
  bindings start being created for the same org — does an org-wide grant
  and an environment-scoped grant for the same identity need an explicit
  precedence rule, or are they additive (union of permissions)? *(The
  Postgres revision makes this a tractable query problem rather than a
  hand-rolled CRD comparison, but doesn't answer it by itself — still
  open.)*
- Migration mechanics for retiring the `HyveEnvironment` CRD in favor of a
  Postgres `organizations` table — this is a one-time export of the one
  real object (`acme`) into a Postgres row referencing its existing
  namespace, then deleting the CRD type, rather than the export/delete/
  reapply-under-new-name dance an in-place rename would have needed; worth
  confirming there's no code path left anywhere still expecting to read
  organization data via the Kubernetes API before the CRD is actually
  removed.
- What's the actual Postgres backup/HA story for the hosted offering, and
  is SQLite genuinely sufficient for self-hosted, or does even a
  single-tenant install eventually want Postgres (e.g. once a self-hosted
  org itself wants multiple concurrent API server replicas)?
- Does `HyveAccessBinding` get deleted outright once its environment-scoped
  successor ships, or does it stick around for org-wide (no-environment)
  grants specifically, with the Postgres table only covering the
  environment-scoped case? The draft above assumes full replacement but
  this hasn't been decided.
- What does the organization-deletion state machine actually look like —
  a status column plus a narrow background worker polling namespace
  termination, or something reusing more of the controller's existing
  retry/backoff machinery despite the controller otherwise never touching
  Postgres? And what happens on a crash mid-cascade: does the API server
  reconcile "orgs marked `pending_deletion`" on startup, or is that a gap
  until something notices?
