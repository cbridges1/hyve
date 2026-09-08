# Proposal: a hyve-native agent

**Status:** draft, not implemented. Written to capture a design direction
raised in conversation, for discussion before any code changes.

## Summary

Ship a small, standing `hyve-agent` workload that gets installed onto each
managed cluster (opt-in, per `ClusterDefinition`). The agent dials *out* to
the hyve control plane and keeps that connection open, giving hyve two
capabilities it doesn't have today without leaning on an external system:

1. **Live status** — the agent reports connection/health state continuously,
   instead of hyve finding out a cluster is reachable only when something
   happens to dispatch a Job against it.
2. **Proxy** — the control plane can tunnel arbitrary Kubernetes API traffic
   (kubectl, the web console's "Get kubeconfig" flow, `hyve cluster auth`)
   through that same open connection, to *any* managed cluster — not just the
   one cluster hyve-api happens to be running on.

The proxy half is togglable per cluster (`spec.access.agent.proxy: true/false`
or similar) — a cluster can run the agent purely for status reporting without
exposing itself to control-plane-mediated `kubectl` access.

This is a full replacement for, not a complement to, the access-method
machinery built this session: `AccessMethod` (the CRD, the `Job`+relay mint
mechanism, `accessMethodRef`), the primary/host-cluster path, and the
unimplemented tunnel path all go away once the agent exists — see
"Relationship to existing access paths" below. Going the agent route means
committing to *one* connectivity mechanism, not adding a fifth alongside the
other four.

## Why this is being considered

Getting a `hyve-civo-module`-provisioned cluster hooked up to a *pull-based*
proxy this session (Rancher's own cluster agent, via `register-with-rancher.yaml`
+ the `rancher-civo` `AccessMethod`) worked, but only by standing up and
depending on an entire second product (Rancher) to get the one thing hyve
itself doesn't do: dial-out connectivity from a cluster hyve doesn't control
the network path to. That's the concrete trigger for this proposal — if hyve
needs this capability often enough to be worth wiring up Rancher for it, it's
worth asking whether hyve should just have it natively instead of asking
users to run a second control plane to get it.

Separately, the access model that exists today is genuinely complex — five
independent code paths, all reachable from the same `hyve cluster auth`
command, each with a different mental model:

| Path | Where the driver's `auth` op runs | What you get | Code |
|---|---|---|---|
| Default (client-side) | The caller's own machine | A kubeconfig, written locally | `cmd/cluster/auth.go` fetches the resolved `auth.yaml` content via `GET /clusters/{name}/auth-context` and runs it locally |
| `access.method: module-auth` | Server-side, inline in the API pod | A kubeconfig, fetched via `GET /kubeconfig` | `internal/api/access.go`'s `ModuleAuthProvider` |
| `access.method: primary` (the "host cluster") | N/A — mints a `ServiceAccount` token, no driver op at all | A kubeconfig whose `server:` points at `hyve-api`'s own `/proxy`, which reverse-proxies to `https://kubernetes.default.svc` | `internal/api/access.go`'s `PrimaryClusterProvider` + `internal/api/proxy.go` |
| `access.method: tunnel` | N/A — reads a pre-existing Secret | A kubeconfig some other process (a workflow, or a human) put in a Secret ahead of time | `internal/api/access.go`'s `TunnelProvider` — the write side (`workflows/mint-tunnel-access.yaml`) is documented as needing "a real Rancher or Teleport deployment", and doesn't exist yet |
| `spec.access.accessMethodRef` (`AccessMethod` CRD) | Server-side, in a short-lived `Job`, result pushed back over an internal-only relay listener | A kubeconfig, streamed back through `POST /relay/{id}` | `internal/api/accessmethod_mint.go` — ~450 lines: `Job` dispatch, a one-shot credential `Secret` with an owner reference for GC, a `sync.Map` of pending requests keyed by a random ID, a bearer-token-gated relay endpoint on a *separate* unauthenticated listener, and a timeout path that inspects pod state for a better error message |

Five different answers to "get me a kubeconfig for this cluster," each
correct for the case it was built for, but genuinely a lot of surface area —
and the three paths that don't need a locally-reachable driver (`primary`,
`tunnel`, and `AccessMethod`) all work around the same underlying gap:
**hyve has no standing, authenticated channel to a cluster it doesn't
already have direct network access to.** `primary` sidesteps this by only
ever supporting the one cluster hyve-api itself runs in. `tunnel`/
`AccessMethod` sidestep it by asking something else (a workflow script, an
admin, an already-running Rancher) to have already solved connectivity and
handed hyve a working kubeconfig.

An agent that dials out and stays connected is a direct answer to the actual
gap, not another provider bolted onto the same five-provider list — which is
exactly why it should *replace* the three paths that exist only because that
gap wasn't solved yet, rather than sit alongside them as a sixth option.

## Pros

- **One connectivity story instead of five.** A single "does this cluster
  have an agent connected" question replaces "which of five access methods
  does this ClusterDefinition use, and does today's caller's environment
  satisfy it." No `AccessMethod` CRD, no per-cluster `accessMethodRef`/
  `accessMethodClusterID` bookkeeping, no separate mental model for "a
  cluster with an externally-managed identity service" — every cluster is
  either agent-connected or it isn't.
- **No host/primary cluster required.** Today, `access.method: primary`
  requires designating one specific cluster as *the* cluster hyve-api runs
  on, and only that one cluster gets the `/proxy` reverse-proxy convenience —
  every other cluster needs client-side tooling, `module-auth`, or an
  `AccessMethod`. An agent on every cluster means every cluster gets the same
  capability, symmetrically.
- **Live status, not point-in-time.** Today's `status` op only runs when the
  reconcile loop happens to dispatch a `Job` for it (see
  `internal/controller/reconciler.go`'s periodic reconcile). A connected
  agent can push "I'm here, I'm healthy" (or "I just lost connectivity to
  $X") continuously — this is exactly the "cluster lifecycle visibility"
  work from earlier this session (`ClusterDefinitionStatus.LastCreateOutput`/
  events), extended from "what happened at create/delete time" to "what's
  true right now."
- **Removes an entire class of infrastructure.** No more short-lived `Job` +
  owner-referenced credential `Secret` + relay listener + pending-request
  map + timeout/pod-inspection fallback (`accessmethod_mint.go`) for the
  common case. An already-connected agent just... proxies the request.
- **No external dependency for the capability that matters most.** Nobody
  has to stand up Rancher or Teleport to get reverse-tunnel connectivity to a
  private/NAT'd/firewalled cluster — which is exactly the case `TunnelProvider`
  was written for and exactly the case this session hit hand-wiring
  `acme-worker` into Rancher.
- **A natural place for more agent-mediated features later** — log
  streaming, exec, port-forward, metrics scraping — the same kind of thing
  Rancher's own `cattle-cluster-agent` and k3s's tunnel-server already do,
  without hyve depending on either.

## Cons and risks

- **A real, standing workload on every opted-in managed cluster.** Everything
  hyve runs today — module operations, workflow steps — is a short-lived
  `batch/v1.Job` that exists for seconds to minutes and is deleted
  immediately after (`internal/k8sjob`'s whole design). An agent is a
  `Deployment` (or `DaemonSet`) that's *always there*: its own upgrade
  story, its own resource requests, its own RBAC footprint, its own failure
  mode when it crashloops or gets evicted. This is a genuine architectural
  shift, not an additive feature — it introduces the one thing `docs/ARCHITECTURE.md`'s
  package map currently has no entry for at all.
- **New security surface.** A component with a standing, authenticated,
  bidirectional channel from every managed cluster back to the control plane
  is a meaningfully more attractive attack target than "an ephemeral Job
  that lives for 90 seconds and then doesn't exist." Getting the agent's own
  authentication (how does it prove which cluster it is?), transport
  security, and the proxy's own authorization (does *every* control-plane
  caller get to proxy to *every* connected cluster, or is this scoped by
  tenant/role the same way `TenantNamespace` scopes everything else?) right
  matters a lot more here than it does for the current model, where the
  blast radius of any one path is naturally bounded by the short-lived Job
  it runs in.
- **Protocol choice is a real decision, not a detail.** A naive
  implementation (agent opens a persistent gRPC or WebSocket stream, control
  plane multiplexes proxied HTTP requests over it) is exactly what Rancher's
  own [`remotedialer`](https://github.com/rancher/remotedialer) library
  does — reusing (or closely modeling on) prior art here is worth serious
  consideration before building this from scratch, precisely because getting
  reconnect/backoff/multiplexing/backpressure right is easy to get subtly
  wrong.
- **Multi-tenancy interaction.** This session's multi-tenancy work made
  `TenantNamespace` (namespace-scoped `HyveAccessBinding`, per-tenant RBAC)
  the core isolation primitive. An agent-mediated proxy needs to slot into
  that cleanly: a tenant's admin proxying to a cluster in another tenant's
  namespace must be exactly as impossible as it is today for every other
  endpoint — this needs to be a first-class part of the design, not
  retrofitted.
- **Version skew.** Once an agent is out on N clusters, it has its own
  release cadence independent of the control plane's. Today, every "does
  this still work" question is answered by whatever `hyve` binary/image is
  currently running — an agent adds a second, distributed answer to that
  question.
- **Not every cluster wants a standing workload.** Ephemeral/short-lived
  clusters (this session's `civo-timed`/expiring templates are a real,
  existing use case) pay the agent's install/registration cost for a
  lifetime that might be hours. The per-cluster opt-in already proposed
  handles this, but it means the fleet is permanently split between
  agent-managed and not, and every feature built on the agent (proxy, live
  status) needs a defined fallback for clusters that never opted in.
- **Retiring `AccessMethod` has a real migration cost, not just a design
  cost.** This session's `rancher-civo` `AccessMethod` and
  `template-civo-rancher-agent.yaml` are live, working setups (`acme-worker`
  included) — going the agent route means an actual migration path for
  them, not just deleting the CRD and its handlers once the agent exists.

## Architecture sketch

This section is intentionally a sketch, not a spec — the point is to surface
the real design questions, not to pre-answer all of them.

### Agent responsibilities

- On startup: authenticate to the control plane (see "Agent identity"
  below), establish the standing connection, report its own version and the
  cluster's basic identity (name it was registered under, driver
  source/version if known).
- While connected: periodic heartbeat (connection state *is* the status
  signal — no separate polling needed); optionally, lightweight periodic
  self-checks (node count, API server reachability) folded into the
  heartbeat payload rather than a separate mechanism.
- When `proxy` is enabled for its cluster: accept multiplexed dial requests
  from the control plane over the standing connection and, per request, open
  a real TCP connection to `https://kubernetes.default.svc` inside its own
  cluster, piping bytes in both directions (see "Tunnel protocol" below —
  this is a raw connection-level proxy, not an HTTP-level one, precisely so
  `kubectl exec`/`port-forward`/`logs -f`/`get --watch` — all of which
  upgrade the connection to SPDY or WebSocket *at the Kubernetes API server*
  — work transparently without the agent needing to understand any of
  that).
- When `proxy` is disabled: connection stays up for status only; the control
  plane's proxy handler refuses to route to it (a clear "proxy disabled for
  this cluster" error, not a silent hang).

### Control-plane side

- A new long-lived listener (likely alongside the existing relay listener's
  pattern — `Server.RelayRoutes`, already a separate, ingress-less listener
  from `/api/`) that agents dial into.
- A registry of currently-connected agents, keyed by cluster identity —
  in-memory is enough for "is this cluster's agent connected right now"; the
  cluster's `ClusterDefinitionStatus` (or a new `AgentStatus` sub-struct) is
  the durable record of "was it connected as of the last time we checked,"
  same pattern as this session's `LastCreateOutput`/`Events` work.
- The existing `AccessProvider` interface (`internal/api/access.go`) is
  already exactly the right shape for this to slot into as a fifth
  implementation (`AgentProvider`) *first*, before anything is removed —
  see rollout plan below.

### Tunnel protocol (resolves the open question below)

The stated goal — tunnel through hyve-api to run real `kubectl` commands
against the cluster, not just fetch a one-time kubeconfig — settles this:
**a WebSocket-based multiplexed reverse dialer, the same shape as Rancher's
own [`remotedialer`](https://github.com/rancher/remotedialer)**, not raw
gRPC streaming and not a simpler agent-long-polls model. Reasoning:

- `kubectl exec`, `port-forward`, `logs -f`, and `get --watch` all upgrade
  their connection to SPDY or WebSocket *at the Kubernetes API server
  itself* — that upgrade happens on top of whatever transport carries the
  bytes there. A model where the agent long-polls for "pending requests" is
  fundamentally request/response and can't carry any of these at all; only a
  truly persistent, bidirectional stream can. This alone rules out long-polling
  for the stated goal.
- `remotedialer`'s actual model fits directly: the agent opens *one*
  outbound WebSocket to the control plane and keeps it open; the control
  plane can then ask the agent to open new logical "sessions" multiplexed
  over that single WebSocket, each behaving like an ordinary `net.Conn` on
  the control-plane side — Rancher's `cattle-cluster-agent` (the exact thing
  this session hand-wired `acme-worker` into) uses precisely this to let
  Rancher's own UI/CLI run `kubectl` against clusters it has no direct
  network path to.
- This is a near-drop-in fit for hyve's *existing* proxy code, not new
  proxying logic: `internal/api/proxy.go`'s `BuildProxy` already builds an
  `httputil.ReverseProxy` with a custom `http.Transport` (today its
  `TLSClientConfig` trusts the in-cluster CA and it dials
  `https://kubernetes.default.svc` directly, since it only ever proxies to
  the one cluster hyve-api itself runs on). The agent path only needs that
  same `Transport`'s `DialContext` swapped to dial through the connected
  agent's remotedialer session instead of a direct network dial — the
  `ReverseProxy` plumbing above it (header handling, streaming response
  bodies, everything `BuildProxy` already gets right) is unchanged.
- Raw gRPC bidirectional streaming could technically be forced into this
  same shape, but buys nothing over WebSocket here and costs proto codegen
  plus HTTP/2 framing overhead for what's fundamentally "a multiplexed byte
  pipe," not an RPC. WebSocket is the lighter-weight fit, and it's what the
  proven prior art already uses.

### Agent identity / authentication

This is the single most important open question. Candidates, roughly in
order of how much new infrastructure they need:

1. **A per-cluster bootstrap token**, generated at agent-install time
   (mirrors exactly how `register-with-rancher.yaml`'s `clusterregistrationtoken`
   dance works today, and how most agent-based systems — Rancher, Teleport,
   Tailscale — solve this). Simple, no new PKI, but the token itself becomes
   a credential that needs the same care `AccessMethodSpec`'s doc comments
   already give the mint-time credential `Secret`.
2. **mTLS**, with the control plane acting as a small internal CA, issuing
   the agent a short-lived client cert at install time and rotating it over
   the standing connection before expiry. More moving parts up front, no
   long-lived shared secret sitting in a cluster's `Secret` store
   indefinitely.
3. Piggyback on the cluster's own driver-minted credentials somehow (e.g.
   the agent authenticates using something the `create` op already produced)
   — probably not worth the coupling this implies between "how a cluster was
   provisioned" and "how its agent authenticates," but worth ruling out
   explicitly rather than silently.

### Per-cluster toggle

Extend `ClusterDefinitionSpec.Access` (already the home of `Method`,
`AccessMethodRef`, `AccessMethodClusterID`) with something like:

```go
type AgentSpec struct {
    // Enabled installs the agent at all (status reporting). Proxy requires
    // this to also be true.
    Enabled bool `json:"enabled,omitempty"`
    // Proxy additionally allows the control plane to route kubectl/API
    // traffic through this cluster's agent. Independently toggleable so a
    // cluster can report live status without ever accepting proxied
    // traffic.
    Proxy bool `json:"proxy,omitempty"`
}
```

Agent lifecycle is a first-class reconcile concern, not a lifecycle-hook
workflow — deliberately not modeled on `register-with-rancher.yaml`, despite
that being the closest existing precedent. `Agent.Enabled`/`Agent.Proxy` are
spec fields the reconcile loop itself acts on directly, the same way it
already acts on `spec.driver`/`spec.pause`/`spec.delete` — install/
reconcile/uninstall the agent as its own step in
`internal/reconcile.Reconciler`'s per-cluster reconcile pass (alongside the
existing create/status/delete dispatch in `reconcileCluster`), never as
something a Template's `workflows.afterCreate` opts into or a user has to
know to wire up. The distinction matters: an `afterCreate` hook only ever
runs once, at creation time, and is invisible to `ClusterDefinitionSpec`
itself (you'd have to read the Template/Workflow to know a cluster has one)
— a first-class field is visible on the `ClusterDefinition` directly,
reconciled continuously (so flipping `Agent.Enabled` on an *existing*
cluster installs the agent on the next reconcile, not only at creation), and
uninstalled the same way if turned back off.

### Relationship to existing access paths

This is the one point in this doc that isn't a tradeoff to weigh — it's a
decision already made: **going the agent route means `AccessMethod` stops
being a thing.** Not "kept for the external-service case" — retired, CRD and
all, once the agent covers what it covered. The reasoning: `AccessMethod`'s
entire reason to exist is "hyve needs a working kubeconfig for a cluster it
can't reach directly, and something else (Rancher, Teleport, a hand-written
`inlineAuth` script) already solved connectivity." An agent solves
connectivity itself, unconditionally, for every cluster that has one — there
is no remaining case where deferring to an external identity/access service
is the *only* way to reach a cluster, only cases where someone already runs
one for unrelated reasons. That's not a reason for hyve to keep a second,
parallel mechanism alive; it's a reason for that external service to sit
downstream of the agent (or be irrelevant to it) rather than upstream of
hyve's own access model.

Concretely, per existing path:

- **Client-side default** stays exactly as-is — it needs no server
  infrastructure at all and is the right answer for someone who already has
  `civo`/`aws`/`gcloud` configured locally, agent or no agent.
- **`ModuleAuthProvider` (`access.method: module-auth`)** stays too — it's
  orthogonal to connectivity (it just runs the driver's own `auth` op
  server-side instead of client-side) and doesn't compete with the agent at
  all.
- **`access.method: primary`** is retired. The host cluster's own agent
  (talking to itself) is the general mechanism's special case, not a reason
  to keep a second one around.
- **`access.method: tunnel`** is retired outright — it was a stub waiting on
  exactly this capability (its own doc comment says as much) and never
  shipped a real write side. The agent *is* what it was going to be.
- **`AccessMethod` (`accessMethodRef`, `internal/apis/hyve/v1alpha1/accessmethod_types.go`,
  `internal/api/accessmethods.go`, `accessmethod_mint.go`) is deleted**:
  the CRD, the mint-Job-plus-relay-listener machinery, `spec.access.accessMethodRef`/
  `accessMethodClusterID` on `ClusterDefinitionSpec`, the web console's
  Access Methods page, all of it. The `rancher-civo` `AccessMethod` and
  `template-civo-rancher-agent.yaml` built this session were the concrete
  proof that this mechanism works, but they were also proof of exactly the
  complexity this proposal exists to remove — they get replaced by the
  agent doing the same job natively, not preserved as a legacy escape
  hatch.

## Rollout plan (sketch)

1. Land `AgentProvider` as a fifth `AccessProvider`, entirely additive —
   no existing path changes behavior.
2. Ship `hyve-agent` as a new image (`cmd/agent`, mirroring `cmd/api`/
   `cmd/controller`'s existing split). Add `Agent.Enabled`/`Agent.Proxy` to
   `ClusterDefinitionSpec.Access` and wire agent install/reconcile/uninstall
   directly into `internal/reconcile.Reconciler`'s per-cluster pass
   (alongside the existing create/status/delete dispatch) from the start —
   no intermediate workflow-hook version, per the "first-class field, not a
   lifecycle hook" decision above.
3. Prove it end-to-end against one template with `Agent.Enabled: true` set
   directly in its rendered spec (a new `civo-agent` template, the same
   "make a new template instead" pattern already used for
   `civo-rancher-agent` this session, as a safe, isolated testbed) before
   touching any existing one — the mechanism under test is the reconcile
   step itself, not a workflow.
4. Add live status surfacing (CLI + web console "Recent activity" panel,
   extending this session's work) once the connection/heartbeat mechanism
   is proven.
5. Start the deprecation clock on `access.method: primary`/`tunnel` and
   `AccessMethod` itself — announced, with a migration path for anyone with
   a live `rancher-civo`-shaped setup (this session's own `acme-worker`
   included), not an instant breaking change. The end state has none of the
   three left; the only question this rollout plan is actually sequencing
   is how to get there without breaking whoever's mid-migration.

## Open questions

- Bootstrap-token vs. mTLS vs. something else for agent identity (see
  above) — this is the decision most worth getting right before writing
  code, since it's the hardest to change later.
- ~~Does the control plane need a real multiplexed tunnel protocol...~~
  **Resolved: yes — a `remotedialer`-style multiplexed WebSocket reverse
  dialer** (see "Tunnel protocol" above), driven directly by the stated goal
  of running real `kubectl` commands (including `exec`/`port-forward`/
  `logs -f`/`watch`) through the tunnel, not just fetching a one-time
  kubeconfig.
- Where does agent *version* live relative to the `hyve` binary's own
  version — pinned per HyveConfig, per-Template, or always-latest with its
  own upgrade workflow?
- Should `proxy: true` require a role above ordinary tenant `admin` (mirrors
  `PrimaryClusterProvider`'s existing superadmin-only gate on the host
  cluster), given what direct `kubectl` access to a tenant's cluster
  implies?
- Multi-tenancy scoping of the connection registry and the proxy handler
  itself — needs explicit design against `Server.TenantNamespace`, not an
  afterthought.
