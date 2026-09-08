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
- **Protocol choice is a real decision, not a detail.** Getting
  reconnect/backoff/multiplexing/backpressure right for a persistent
  reverse tunnel is easy to get subtly wrong — worth resolving deliberately
  rather than picking whatever's closest to hand. See "Tunnel protocol"
  below for the resolution (SSH's own native multiplexing, not a
  hand-rolled protocol or a niche library).
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
against the cluster, not just fetch a one-time kubeconfig — settles the
*shape* needed: a persistent, bidirectional, multiplexed stream, not a
request/response model. It does *not*, on its own, settle *which*
transport — that took a second pass.

**First pass, reconsidered:** a WebSocket-based multiplexed reverse dialer
modeled on Rancher's own [`remotedialer`](https://github.com/rancher/remotedialer)
library was the initial answer, since it's exactly this shape and is
proven at scale inside Rancher. But adopting the library itself, rather
than just its shape, doesn't hold up: checking actual adoption turned up
real usage confined to Rancher's own projects (`rancher/rancher`,
`rancher/rke`, `rancher/steve`, `rancher/wrangler`) plus a single small,
unaffiliated hobby project — not something independent tools commonly
build on. Taking on a dependency maintained by another organization for
its own internal purposes, with essentially no adoption outside that
organization, is a real, avoidable risk for a mechanism this central to
hyve's own architecture.

**Resolved: SSH's own reverse port-forwarding and channel multiplexing,
via `golang.org/x/crypto/ssh`** — not a custom protocol, and not gRPC.
Reasoning:

- `kubectl exec`, `port-forward`, `logs -f`, and `get --watch` all upgrade
  their connection to SPDY or WebSocket *at the Kubernetes API server
  itself* — that upgrade happens on top of whatever transport carries the
  bytes there. A model where the agent long-polls for "pending requests" is
  fundamentally request/response and can't carry any of these at all; only
  a truly persistent, bidirectional stream can. This alone rules out
  long-polling for the stated goal, independent of the transport question.
- **"Agent behind NAT, server needs to reach it" is the exact problem SSH's
  own remote port-forwarding (`-R`) was built to solve**, and has solved
  since long before any of the alternatives here existed. SSH's `Channel`
  abstraction is *natively* a multiplexed logical connection over one
  transport connection — hyve doesn't need to invent framing, session IDs,
  or backpressure handling on top of a lower-level transport (WebSocket or
  otherwise); the protocol already has all of that built in and proven.
- `golang.org/x/crypto/ssh` gives Go-native access to both sides directly:
  the agent is an SSH client (`ssh.Dial`, then requests a remote listener
  via the `tcpip-forward` global request, the same primitive `ssh -R` uses)
  and hyve-api is an SSH server (`ssh.NewServerConn`) — no protobuf
  codegen, no hand-rolled wire format, and the dependency itself is
  maintained by the Go team as part of the extended standard library, about
  as low bus-factor risk as an external dependency gets.
- Still a near-drop-in fit for hyve's *existing* proxy code, not new
  proxying logic: `internal/api/proxy.go`'s `BuildProxy` already builds an
  `httputil.ReverseProxy` with a custom `http.Transport` (today its
  `TLSClientConfig` trusts the in-cluster CA and it dials
  `https://kubernetes.default.svc` directly, since it only ever proxies to
  the one cluster hyve-api itself runs on). The agent path only needs that
  same `Transport`'s `DialContext` swapped to open a new SSH channel
  through the connected agent's session instead of a direct network dial —
  the `ReverseProxy` plumbing above it (header handling, streaming response
  bodies, everything `BuildProxy` already gets right) is unchanged.
- gRPC bidirectional streaming was the other real candidate — genuinely
  common for new agent-style tools, unlike `remotedialer` — but needs a
  bespoke adapter layer to present a gRPC stream as a `net.Conn`-shaped
  thing `DialContext` can use, plus protobuf codegen as a new build-time
  dependency hyve doesn't otherwise have. SSH gives the same multiplexed-
  connection shape natively, with less custom code on top and a narrower,
  more mature dependency underneath.

### Agent identity / authentication (resolves the open question below)

**Resolved: SSH certificates — hyve-api's own SSH CA signs short-lived
host and user certificates — with a short-lived bootstrap token only for
the one-time step of obtaining the agent's first certificate, not a
standing bootstrap token used as the ongoing credential.**

This is a direct consequence of "Tunnel protocol" above landing on SSH
rather than a TLS-carried transport: SSH has its *own* transport-layer
handshake, entirely separate from TLS, so mutual TLS doesn't compose with
it the way it would have with a WebSocket — there's no TLS layer underneath
an SSH connection to attach a client cert to. SSH has a native equivalent
that gives every property mTLS was chosen for, just in SSH's own
certificate format instead of X.509: `golang.org/x/crypto/ssh`'s
`ssh.CertChecker`/`ssh.Certificate` types let hyve-api's own SSH CA sign
short-lived *user* certificates (for the agent, presented during its SSH
handshake) and short-lived *host* certificates (for hyve-api itself, so the
agent can verify it's really talking to hyve-api and not an on-path
impersonator) — the SSH analogue of mutual TLS, off one internal CA, native
to the transport actually being used:

- **A standing bootstrap token is a long-lived bearer secret.** Whatever
  form it takes, it either has to be the credential the agent presents on
  *every* reconnect (in which case it's sitting in that cluster's `Secret`
  store indefinitely — read access to that namespace is read access to
  "impersonate this cluster's agent"), or hyve has to build a whole separate
  rotation/expiry mechanism to stop being one — at which point it's not
  simpler than certificate-based auth, just a worse version of it.
- **SSH certificates give short-lived, automatically-rotated credentials
  for free** — the control plane (a small internal SSH CA) issues the agent
  a user certificate with a short TTL (hours-to-days, not indefinite), and
  the agent renews it over its own already-open connection before expiry,
  the same shape Kubernetes' own kubelet client-cert rotation already uses.
  A stolen cert has a bounded exploitation window instead of being valid
  until someone notices and manually rotates it.
- **Mutual, not one-directional.** A bearer token only proves the agent's
  identity to the server — the agent still needs a *separate* mechanism to
  know it's really talking to hyve-api and not something on-path
  impersonating it. SSH host certificates (the agent's `ssh.Dial` validates
  hyve-api's host certificate via the same internal CA, using
  `ssh.CertChecker.CheckHostKey`) answer that direction with the same CA
  that signs the agent's own user certificate — one trust root, both
  directions.
- **The private key never has to be transmitted anywhere.** The agent
  generates its own SSH keypair locally and sends only the *public* half to
  the control plane for signing — unlike a token, which is a secret that
  has to reach the cluster intact from wherever it was generated, and is
  compromised the moment it leaks in transit or at rest.

None of that eliminates needing *some* one-time credential to authenticate
the agent's very first signing request before it has a certificate of its
own — that's what the bootstrap token is actually for here, and it's a
materially smaller thing to get right: single-use, short expiry, and
irrelevant to security the moment the first certificate is issued, rather
than the thing standing between an attacker and cluster access for the
agent's entire lifetime. This is the same two-phase shape as Kubernetes'
own kubelet TLS bootstrapping (`kubeadm`'s bootstrap-token →
`CertificateSigningRequest` → kubelet client cert), just carried over SSH's
own certificate format instead of X.509: `register-with-rancher.yaml`'s
`clusterregistrationtoken` dance is the closest thing already in this
codebase, but the kubelet flow is the more precise model for "one-time
token proves identity once, then a rotated certificate takes over."

Piggybacking on the cluster's own driver-minted credentials (the agent
authenticating using something the `create` op already produced) was
considered and set aside — it couples "how a cluster was provisioned" to
"how its agent authenticates" for no real benefit over the bootstrap-token
step above, which is already driver-agnostic.

### Proxy authorization model (resolves the open question below)

Everything above ("Agent identity / authentication") is about the agent
authenticating *to the control plane* — a separate question is what happens
on the *other* end: once a request is flowing through the tunnel, what
identity does it carry on the target cluster's own API server, and who gets
to send one at all? This deserved more than the one-line "gate it behind a
role" the open question below originally posed, because there are really
two different authorization layers being conflated there, and the agent
model changes the relationship between them in a way the existing
`primary`/`AccessMethod` paths didn't have to confront.

**The two layers:**

1. **hyve's own role gate** — who's *allowed to ask* for a proxy session at
   all. This is the thing `RequireRole`/`TenantNamespace` already govern for
   every other endpoint, and it's what the original question was really
   asking about.
2. **The target cluster's own RBAC** — what a granted session can actually
   *do*, once traffic reaches the real kube-apiserver. This is a completely
   separate authorization system, on a completely separate cluster, that
   hyve doesn't own.

Today, these two layers are naturally kept distinct for every existing
`AccessProvider`: `ModuleAuthProvider` and `AccessMethod`'s mint flow both
produce a kubeconfig scoped to *that specific request*, and
`PrimaryClusterProvider` mints a fresh `ServiceAccount` token *per caller*
(`saRef` resolved from `ServiceAccountRefFromContext`) — so whatever the
target cluster's own RBAC grants that identity is already a second,
independent boundary underneath hyve's own role check. **An agent-mediated
proxy doesn't get this for free.** The agent is a single, standing identity
on its cluster (whatever `ServiceAccount`/permissions it was installed
with) — unless something more is built, *every* proxied request rides on
that same identity regardless of which hyve user sent it. That collapses
the two layers into one: hyve's own role gate becomes the *entire*
authorization boundary, with zero differentiation once past it — a tenant
`admin` and a tenant `read-only` user would get identical `kubectl` access
once either is allowed to proxy at all, because the target cluster's own
RBAC never sees a difference between them.

**Closing that gap** means the agent needs to forward *caller identity*,
not just caller traffic. The direct mechanism for this already exists in
Kubernetes: [impersonation](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#user-impersonation) —
hyve-api attaches `Impersonate-User`/`Impersonate-Group` headers (derived
from the caller's resolved hyve identity/role) to the proxied request; the
agent's own `ServiceAccount` is granted *only* the `impersonate` verb (not
broad access itself); and the target cluster's own RBAC — `RoleBinding`s the
agent provisions at install — makes the real decision. This is a direct
extension of a mapping hyve already has: `deploy/helm/hyve/templates/api-access-roles.yaml`
already binds `admin`→`cluster-admin` and `read-only`→`view` for the
primary/host cluster; an agent-installed cluster would provision the exact
same two `RoleBinding`s locally and let impersonation carry hyve's role
across the tunnel, rather than inventing a new mapping. Without this, `proxy`
access is necessarily all-or-nothing per cluster, no matter what role gate
sits in front of it.

**Prior art confirms the mechanism.** This is exactly how Rancher's own
`cattle-cluster-agent` tunnel handles the identical problem: `cattle-cluster-agent`'s
own standing identity on the downstream cluster is *not* what authorizes a
user's proxied request. Rancher resolves the caller's identity against
*Rancher's own* auth (independent of the downstream cluster entirely),
attaches `Impersonate-User`/`Impersonate-Group` headers before forwarding
through the tunnel, and lets the downstream cluster's own native RBAC —
`RoleBinding`s Rancher's own controllers keep synced there — make the real
authorization decision. The agent's own `ServiceAccount` only needs the
`impersonate` verb, not broad access used directly for user traffic.

**Resolved, with that precedent as the model:**

1. **Impersonation is the mechanism** — hyve-api resolves the caller's
   identity/role the same way it already does for every other endpoint
   (`RequireRole`), attaches `Impersonate-User`/`Impersonate-Group` before
   forwarding through the tunnel. The agent's `ServiceAccount` is granted
   the `impersonate` verb specifically, not broad access.
2. **Static role mapping, not Rancher's dynamic sync layer.** Rancher needs
   a `RoleTemplate`-sync controller because its permission model is rich
   (multiple templates, project scoping, custom roles); hyve's three flat
   roles don't need that. Two `RoleBinding`s per cluster, provisioned once
   at agent-install time (part of the same first-class reconcile step that
   installs the agent — see "Per-cluster toggle" below, not a separate
   mechanism), identical in shape to what `api-access-roles.yaml` already
   creates for the primary cluster: `admin`→`cluster-admin`,
   `read-only`→`view`.
3. **The host/control-plane cluster keeps its own hardcoded exception on
   top of the general rule, not folded into it.** `PrimaryClusterProvider`'s
   existing superadmin-only gate stays exactly as-is — that cluster is
   special because it holds *every tenant's* data, not because `kubectl`
   access is inherently sensitive. Ordinary tenant `admin` gets
   impersonation-scoped access to their *own* clusters under the general
   rule; nobody except superadmin gets the host cluster, regardless.
4. **`read-only` gets proxy access too** — impersonation maps it to the
   `view` `ClusterRole`, so "read-only" stays genuinely true at the
   `kubectl` level, not just hyve's own REST layer. No reason to withhold
   it once the authorization actually holds.
5. **Audit trail: connection-level for v1, not full command-level.** Emit
   an Event (the same pattern this session's own cluster-lifecycle-visibility
   work already established — `ClusterDefinitionStatus`'s events/output
   fields) recording who proxied to which cluster and when. Parsing/logging
   every proxied request (what `kubectl exec` actually ran) is a real,
   documented future enhancement, not a v1 blocker.
6. **Keep `Agent.Enabled`/`Agent.Proxy` and this authorization model
   orthogonal.** The toggle controls whether the mechanism exists on a
   cluster at all; impersonation + `RoleBinding`s control who can use it
   and what they get, evaluated per request — don't conflate "is proxy on"
   with "who's allowed."

### Multi-tenancy scoping of the connection registry (resolves the open question below)

**Resolved: not new design — a direct extension of `Server.TenantNamespace`,
the same mechanism that already keeps every other endpoint from leaking
across tenants.**

- **The registry key is `(namespace, clusterName)`, never a bare cluster
  name.** Two different tenants can each have a `ClusterDefinition` literally
  named `prod` — that's exactly why `ClusterDefinition` is namespace-scoped
  in the first place, and the connection registry has to respect the same
  identity or it reintroduces the collision Kubernetes namespacing already
  solved.
- **The proxy handler resolves `TenantNamespace(r)` before it ever touches
  the registry** — the same call every other handler
  (`handleGetCluster`/`handleListClusters`/etc.) already makes first,
  already handling the superadmin act-as override for free. It then does a
  `Get` against `(that namespace, the requested name)` — the identical
  existence check `handleGetCluster` already performs — and only *then*
  looks up that same key in the connection registry. A caller structurally
  cannot even *ask* to proxy outside their own tenant: the namespace half of
  the lookup key comes from their own resolved identity via
  `TenantNamespace`, never from anything they supply directly in the
  request. This is the same property that already makes cross-tenant access
  impossible for every other endpoint today, not a new isolation mechanism
  invented for the agent.
- **Agent registration ties back to "Agent identity."** Since the SSH user
  certificate issued at bootstrap already encodes which `ClusterDefinition`
  (namespace + name) the agent belongs to (bound as a certificate principal
  at signing time), the agent registers into the connection registry under
  that same cryptographically-asserted identity — the control plane never
  has to trust an unauthenticated claim about which cluster is connecting,
  closing the loop between "who is this agent" and "which tenant does it
  belong to"
  with the same certificate that already answers the first question.

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

### Agent image/version (resolves the open question below)

**Resolved: a built-in default, baked into the controller binary and pinned
to whatever agent version that controller release was actually tested
against, overridable via `HyveConfig`** — the exact same shape
`HyveConfigSpec.DefaultModuleImage`/`DefaultWorkflowImage` already
establish for this codebase's other "what image do we run" questions (see
their own doc comments in `internal/apis/hyve/v1alpha1/hyveconfig_types.go`
for the precedent this mirrors).

```go
// DefaultAgentImage is hyve-agent's image, installed onto every cluster
// with spec.access.agent.enabled: true. Empty uses the controller's own
// built-in default — the hyve-agent version that controller release was
// actually built and tested against, not a moving "latest" tag — so an
// unconfigured install still gets a known-compatible agent without an
// operator having to track version pairings by hand. Set this to run a
// different version deliberately (a newer agent for a feature the running
// controller doesn't need but the agent does, a custom build, pinning
// during a rollout) — same override stance DefaultModuleImage/
// DefaultWorkflowImage already take, just for the one image that isn't
// per-operation.
DefaultAgentImage string `json:"defaultAgentImage,omitempty"`
```

Why "baked-in default, tested against that specific controller release"
rather than always-latest: this doc's own "Version skew" con already flags
that an agent running its own release cadence is a real risk — floating to
whatever's newest at install time makes that worse, not better, since two
clusters installed a week apart could silently end up on different agent
versions with no controller-side change at all. Pinning the *default* to a
known-good, co-tested pairing (and requiring an explicit `HyveConfig` edit
to deviate) keeps "what agent version is actually running" an intentional
choice rather than an accident of install timing.

This doesn't need a per-cluster override tier the way `DefaultModuleImage`
sits below `ClusterDefinition.spec.runner.image` — nothing about *this*
cluster's driver should determine what agent version it runs, unlike a
module operation's image, which genuinely can vary per cluster. If a
per-cluster override turns out to be needed later (piloting a new agent
version on one cluster before a fleet-wide `HyveConfig` change), it's a
natural, additive extension of `AgentSpec` — not a reason to add it now.

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

- ~~Bootstrap-token vs. certificate-based auth vs. something else for agent
  identity...~~ **Resolved: SSH certificates (hyve-api's own SSH CA signs
  short-lived host and user certs), with a short-lived bootstrap token used
  only once** to authenticate the agent's first signing request (see "Agent
  identity / authentication" above) — the two aren't actually competing
  options, certificate-based auth still needs a one-time bootstrap step,
  it's just a much smaller thing to secure than a standing bearer
  credential.
- ~~Does the control plane need a real multiplexed tunnel protocol...~~
  **Resolved: yes — SSH's own reverse port-forwarding and channel
  multiplexing, via `golang.org/x/crypto/ssh`** (see "Tunnel protocol"
  above), driven directly by the stated goal of running real `kubectl`
  commands (including `exec`/`port-forward`/`logs -f`/`watch`) through the
  tunnel, not just fetching a one-time kubeconfig. An initial WebSocket-
  based design modeled on Rancher's `remotedialer` was reconsidered once
  its real-world adoption turned out to be essentially Rancher-only, with
  no meaningful use outside that one organization's own projects.
- ~~Where does agent *version* live relative to the `hyve` binary's own
  version...~~ **Resolved: a controller-built-in default, pinned to
  whatever agent version that controller release was tested against,
  overridable via a new `HyveConfig.spec.defaultAgentImage`** (see "Agent
  image/version" above) — the same shape `DefaultModuleImage`/
  `DefaultWorkflowImage` already establish, deliberately not
  always-latest, since floating versions would make this doc's own
  "Version skew" con worse rather than better.
- ~~Should `proxy: true` require a role above ordinary tenant `admin`?~~
  **Resolved: no elevated role needed beyond ordinary tenant `admin`/
  `read-only`, provided the proxy carries per-caller identity via
  Kubernetes impersonation** — the same mechanism Rancher's own
  `cattle-cluster-agent` tunnel uses for this exact problem (see "Proxy
  authorization model" above). A tenant's own `admin`/`read-only` role maps
  to `cluster-admin`/`view` on their own cluster via `RoleBinding`s
  provisioned at agent-install time (mirroring `api-access-roles.yaml`'s
  existing mapping); the host/control-plane cluster keeps
  `PrimaryClusterProvider`'s superadmin-only gate as a separate, hardcoded
  exception on top of that general rule, not superseded by it.
- ~~Multi-tenancy scoping of the connection registry and the proxy handler
  itself...~~ **Resolved: not new design — a direct extension of
  `Server.TenantNamespace`** (see "Multi-tenancy scoping of the connection
  registry" above): the registry is keyed by `(namespace, clusterName)`,
  never a bare name, and the proxy handler resolves `TenantNamespace(r)`
  before ever touching the registry — the same first step every other
  handler already takes — so a caller structurally cannot reach a
  connection outside their own tenant, the same property that already
  holds for every other endpoint today. Distinct from the impersonation
  question above: that's about what a *granted* session can do on the
  target cluster; this is about which callers can reach which cluster's
  connection at all.

All five open questions are resolved as of this revision — nothing left
unaddressed here needs to block moving from proposal to an implementation
plan.
