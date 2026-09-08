# Implementation plan: hyve-agent

**Status:** draft, not implemented. Companion to
[`HYVE-AGENT-ARCHITECTURE-PROPOSAL.md`](./HYVE-AGENT-ARCHITECTURE-PROPOSAL.md),
which is the source of truth for *why* — every design decision referenced
below (tunnel protocol, agent identity, proxy authorization, agent version
resolution, multi-tenancy scoping) is already resolved there. This doc is
only the *what/in what order* — it doesn't re-argue anything the proposal
already settled.

## Sequencing principle

Each milestone below is additive and independently mergeable — nothing in
an earlier milestone depends on a later one existing, and no existing
behavior changes until a milestone explicitly says so. This mirrors the
proposal doc's own "Rollout plan (sketch)," fleshed out into concrete
files, tests, and a manual-verification step per milestone — the same
build/test/live-verify discipline every change this session went through
(`go build ./... && go vet ./... && go test ./... && gofmt -l .`, then a
real redeploy to `k3d-hyve-local` and, for anything user-facing, a live
check in the browser).

Milestones 1–3 (CRD scaffolding, PKI/bootstrap, tunnel connectivity) are
the highest-uncertainty pieces — new dependency, new crypto plumbing, a
protocol hyve has never spoken before — and can all ship dark (observable
by nobody, changing no runtime behavior) well before milestone 4 wires any
of it into the reconcile loop. That's deliberate: prove the hard, unfamiliar
parts in isolation before making them load-bearing.

## Milestone 0: dependency decision

Use `golang.org/x/crypto/ssh` for both the tunnel transport and agent
identity — the proposal doc's "Tunnel protocol" and "Agent identity /
authentication" sections both settled on SSH (reverse port-forwarding for
the tunnel, SSH certificates for identity), specifically *instead of*
`github.com/rancher/remotedialer`: checking its actual adoption turned up
real usage confined to Rancher's own projects (`rancher/rancher`,
`rancher/rke`, `rancher/steve`, `rancher/wrangler`), not something
independent tools commonly build on — not a foundation worth taking on for
a mechanism this central to hyve's own architecture. `x/crypto/ssh` is
maintained by the Go team as part of the extended standard library, and
`golang.org/x/crypto` is already a direct dependency of this repo's own
`go.mod` (v0.55.0, confirmed by checking directly) — this milestone needs
no new top-level dependency at all, just a new import path into a module
already present. About as low-risk a dependency decision as this milestone
could have landed on: no protobuf codegen, no new build-time tooling, no
external project's own release cadence to track.

## Milestone 1: CRD + type scaffolding (inert)

Adds every new field this proposal needs, with nothing yet reading them —
pure schema groundwork.

**Files:**

- `internal/apis/hyve/v1alpha1/clusterdefinition_types.go`:
  - New `AgentSpec` struct (`Enabled`, `Proxy bool`) and `Agent *AgentSpec`
    field on the existing `AccessSpec` (alongside `Method`, `Tunnel`,
    `AccessMethodRef`, `AccessMethodClusterID`).
  - New `AgentStatus` struct (`Connected bool`, `LastConnectedAt`,
    `LastDisconnectedAt string`, `Version string` — the agent's own
    reported version, distinct from `HyveConfig.spec.defaultAgentImage`,
    useful for spotting version skew directly) and `Agent AgentStatus`
    field on `ClusterDefinitionStatus` (alongside the existing `Access`,
    `LastCreateOutput`/`LastDeleteOutput`).
- `internal/apis/hyve/v1alpha1/hyveconfig_types.go`: add
  `DefaultAgentImage string` to `HyveConfigSpec`, per the proposal's "Agent
  image/version" resolution.
- Regenerate: `internal/apis/hyve/v1alpha1/zz_generated.deepcopy.go`,
  `deploy/helm/hyve/crds/hyve.io_clusterdefinitions.yaml`,
  `hyve.io_hyveconfigs.yaml` — same `controller-gen object`/`controller-gen
  crd` invocation already used earlier this session for the cluster-
  lifecycle-visibility CRD fix, diffed before applying to confirm it only
  adds the new fields.

**Definition of done:** `go build ./...` clean; `kubectl explain
clusterdefinition.spec.access.agent` shows the new fields on a real
`k3d-hyve-local`; nothing else changes.

**Tests:** none beyond what `go vet`/the build already catch — there's no
behavior yet to unit test.

## Milestone 2: control-plane internal SSH CA + bootstrap/signing flow

Implements the "Agent identity / authentication" resolution: a small
internal SSH CA, short-lived signed SSH host and user certificates, and a
single-use bootstrap token that authenticates only the first signing
request.

**Files (new):**

- `internal/agentpki/ca.go` — generate-or-load the CA's own SSH keypair
  (an `ssh.Signer`, not an X.509 CA — SSH certificate authorities are just
  an ordinary keypair used to sign other keys), persisted as a `Secret` in
  `hyve-system` (`hyve-agent-ca`), loaded once at API/controller startup
  (mirrors how the API pod already reads its own in-cluster CA once at
  startup for `PrimaryClusterProvider`). Also signs hyve-api's own host
  certificate at startup, so the listener from milestone 3 has one ready
  to present.
- `internal/agentpki/sign.go` — sign an agent-submitted public key into a
  short-lived `ssh.Certificate` (`CertType: ssh.UserCert`), encoding the
  target `ClusterDefinition`'s namespace+name as the certificate's
  `ValidPrincipals` — this is what lets "Multi-tenancy scoping of the
  connection registry" tie agent identity to tenant identity
  cryptographically rather than by unauthenticated claim. There's no
  X.509-style CSR object in SSH; the agent just submits its raw public key
  alongside the bootstrap token, and this signs that key directly.
- `internal/agentpki/bootstrap.go` — generate/validate bootstrap tokens:
  single-use, short expiry, scoped to one `(namespace, clusterName)`.
- `internal/api/agent_bootstrap.go` — `POST /agent/bootstrap`: validates
  the token, signs the submitted public key, returns the certificate.
  Shape mirrors Kubernetes' own kubelet bootstrap-token →
  `CertificateSigningRequest` flow (the proposal doc's own cited
  precedent) with the same simplification — no separate approval step,
  since the bootstrap token itself is the one-time proof — carried over
  SSH's own certificate format instead of X.509.

**Tests:** `internal/agentpki/*_test.go` — sign round-trip (a submitted
public key comes back as a valid certificate the CA's own public key
verifies), token single-use enforcement (second use of the same token
fails), token expiry. `internal/api/agent_bootstrap_test.go` —
bad/expired/reused token rejected; a valid token + public key issues a
certificate exactly once.

**Manual verification:** generate an SSH keypair with `ssh-keygen` (or
Go's own `golang.org/x/crypto/ssh` `GenerateKey`) — a throwaway script, not
a real agent binary yet — `POST` the public key with a manually-minted
bootstrap token, confirm a real signed certificate comes back (verifiable
with `ssh-keygen -L -f` against the returned cert) and a second attempt
with the same token is rejected.

## Milestone 3: `cmd/agent` skeleton — connects, authenticates, reports status (no proxy yet)

The first real end-to-end proof: an agent binary that dials out, survives
the bootstrap dance, and the control plane can see it's alive. Deliberately
stops short of proxying anything — isolates "does the tunnel work at all"
from "does authorization on top of it work."

**Files (new):**

- `cmd/agent/main.go` — entrypoint: on startup, load a persisted SSH user
  certificate if one exists; otherwise read a bootstrap token (injected as
  an env var at install time — see milestone 4) and run the signing-request/
  bootstrap flow once, persisting the result.
- `internal/agent/bootstrap.go` — generates the agent's own SSH keypair,
  sends the public half + bootstrap token to `/agent/bootstrap`, persists
  the signed certificate into a `Secret` the agent manages in its own
  namespace (`hyve-agent-cert`) so a pod restart doesn't re-bootstrap.
- `internal/agent/connect.go` — `ssh.Dial` the control plane's tunnel
  listener, presenting the signed user certificate, verifying hyve-api's
  own host certificate against the same internal CA
  (`ssh.CertChecker.CheckHostKey`); reconnect/backoff on drop.
- `internal/agent/heartbeat.go` — periodic lightweight self-check (node
  count, API server reachability) folded into the session per the
  proposal's "Agent responsibilities" — no separate polling mechanism.
- `internal/api/agent_listener.go` — the control-plane-side SSH server
  listener: a new port (mirrors `Server.RelayRoutes`'s existing pattern of
  a separate, ingress-less listener from `/api/`), presents hyve-api's own
  host certificate, verifies the agent's user certificate against the CA
  from milestone 2, extracts `(namespace, name)` from the certificate's
  principals, and — this is the one place status writes happen in this
  milestone — on connect/disconnect, writes `ClusterDefinitionStatus.Agent`
  directly via `s.Client.Status().Update`. This lives in `internal/api`,
  not `internal/controller`, because the registry itself lives in the API
  process (the same process the listener runs in); no controller
  involvement needed for this one status field, the same way
  `AccessMethod`'s own status handling is entirely API-side today.
- `internal/api/agentregistry.go` — the `(namespace, name)`-keyed
  in-memory map of live SSH connections (each wrapping an `*ssh.ServerConn`
  that new channels get opened against per proxied request).

**Tests:** registry unit tests (register/lookup/remove-on-disconnect). A
local integration test standing up the listener and a test agent
(constructed directly with a locally-signed test cert, not via the full
CLI binary) confirming the registry reflects a real connect/disconnect
cycle.

**Manual verification:** run `cmd/agent` against a real `k3d-hyve-local`
(as a pod in a throwaway kind/k3d cluster, or even locally against a port-
forwarded listener for a first pass) and confirm `kubectl get
clusterdefinition <name> -o yaml` shows `status.agent.connected: true`,
then flips to `false` when the agent process is killed.

## Milestone 4: reconcile-loop agent lifecycle (first-class, not a workflow)

Wires `spec.access.agent.enabled` into the reconcile loop directly, per the
"agent lifecycle is a first-class reconcile concern" decision — installing/
removing the agent is triggered by the field, continuously, not by a
Template's `afterCreate` hook.

**Files (new/edited):**

- `internal/reconcile/agent.go` (new) — the reconcile step: given
  `spec.access.agent.enabled`, renders and applies (or removes, if flipped
  back off) the agent's manifests against the target cluster — using the
  *same* already-resolved kubeconfig the ACTIVE-status branch's other
  steps already have in hand this reconcile pass (the driver's own auth
  op), not a separate auth round-trip.
- Manifest templates (new, likely `internal/reconcile/agent_manifests.go`
  or embedded YAML): the agent `Deployment` (image from
  `HyveConfig.spec.defaultAgentImage`, falling back to the controller's own
  built-in default per milestone 1's field; a freshly-minted single-use
  bootstrap token from milestone 2 injected as an env var), a
  `ServiceAccount`, a `ClusterRole` granting only the `impersonate` verb +
  its `ClusterRoleBinding` for the agent's own identity, and — only when
  `spec.access.agent.proxy: true` — the two `RoleBinding`s
  (`admin`→`cluster-admin`, `read-only`→`view`) from the resolved proxy-
  authorization design, mirroring `api-access-roles.yaml`'s existing
  mapping exactly.
- `internal/reconcile/manager.go`: wire the new step into `reconcileCluster`,
  alongside the existing create/status/delete dispatch, gated on the
  ACTIVE-status branch (installing an agent before a cluster is actually up
  makes no sense).

**Tests:** `internal/reconcile/agent_test.go`, styled like
`TestReconcileCluster_ToolRequirements_OnlyEnforcedInlineNotViaJobDispatch`
(fake driver module, fake state provider) — assert the right manifests get
applied when `Agent.Enabled` is true and removed when it's flipped back to
false, and that `Agent.Proxy` alone (without `Enabled`) is rejected or
ignored per the spec's own documented dependency.

**Manual verification (the concrete proof this decision mattered):** flip
`spec.access.agent.enabled: true` on an *already-existing* real cluster —
not one just created — and confirm the agent installs and connects on the
very next reconcile, with no recreation and no workflow to remember to run.

## Milestone 5: proxy path — impersonation, tenant scoping, real `kubectl`

The payoff milestone: `spec.access.agent.proxy: true` becomes something a
caller can actually use.

**Files (new):**

- `internal/api/agent_proxy.go` — the proxy handler (mounted at, e.g.,
  `/agent-proxy/{namespace}/{name}/`): `requireAuth`+`requireRole`,
  `TenantNamespace(r)` resolution, a `Get` existence check against that
  `(namespace, name)` (the same check `handleGetCluster` already makes),
  *then* a registry lookup — per the resolved multi-tenancy design, in
  that exact order. Builds an `httputil.ReverseProxy` whose
  `Transport.DialContext` opens a new channel on the matched agent's SSH
  connection (`ssh.ServerConn.OpenChannel`, requesting a forwarded-tcpip
  channel to `https://kubernetes.default.svc` on the agent's side) instead
  of a direct network dial (the one substantive change to
  `internal/api/proxy.go`'s existing `BuildProxy` shape — everything else
  about it, header handling, streaming response bodies, is reused as-is).
  Sets `Impersonate-User`/`Impersonate-Group` from the caller's resolved
  hyve role before forwarding.
- `internal/api/access.go`: a real `AgentProvider` implementing the
  existing `AccessProvider` interface — when `Agent.Proxy` is enabled,
  mints a kubeconfig whose `server:` points at the new `/agent-proxy/...`
  path, carrying the caller's own hyve session token (the same
  `server:`-points-at-hyve-api's-own-path shape `PrimaryClusterProvider`
  already uses for `/proxy`).

**Tests:** `internal/api/agent_proxy_test.go` — must include explicit
cross-tenant-rejection cases (styled like this session's own
`TestHandleCreateEnvironment_RejectsReservedNames` regression tests: prove
a namespace-B caller 404s/403s against a namespace-A cluster, not just that
the happy path works) and role→impersonation-group mapping tests
(`admin`→`cluster-admin` group, `read-only`→`view` group, verified against
a fake/local target apiserver's own RBAC decision, not just that the
header got set).

**Manual verification (the real test of the whole proposal):** `hyve
cluster auth <name>` against an agent+proxy-enabled cluster, then run
actual `kubectl get pods`, `kubectl exec`, `kubectl logs -f`, and `kubectl
get --watch` through the minted kubeconfig — confirming the
streaming/connection-upgrade cases specifically, since those are the exact
reason a raw connection-level tunnel was chosen over anything HTTP-level in
the "Tunnel protocol" resolution. A plain `kubectl get pods` succeeding
proves far less than `kubectl exec` working.

## Milestone 6: a real testbed template + full live cycle

- nexus-config: `template-civo-agent.yaml` (new, cluster-mode,
  `spec.access.agent: {enabled: true, proxy: true}` set directly in the
  rendered spec) — the same "make a new template instead" pattern already
  used for `civo-rancher-agent` this session, kept as an isolated testbed
  rather than touching any existing template.
- Full live cycle, end to end: create → agent installs and connects →
  status visible in the CLI/UI → proxy works, including `exec`/`watch` →
  delete → confirm the agent's own `Deployment`/RBAC gets torn down along
  with the cluster, nothing orphaned in either direction.

## Milestone 7: CLI + web console surfacing

- `cmd/cluster/auth.go`: a fifth branch alongside the existing four (per
  its own doc comment structure, which already documents each path) for
  the agent-proxy case.
- `hyve cluster logs`/`hyve cluster show`: surface `status.agent.connected`/
  `lastConnectedAt`/`version`.
- Web console: extend `ClusterDetailPage.tsx`'s "Recent activity" panel
  (built earlier this session) with an agent status indicator. Note the
  `Agent.Enabled`/`Agent.Proxy` toggle already works for free once the CRD
  field exists — `SpecEditor` edits the full YAML spec directly — so a
  purpose-built toggle control in the UI is a nice-to-have polish item for
  this milestone, not a blocker for it.

## Milestone 8: deprecation notice + migration guide

Doc-only, matching the proposal's own "not an instant breaking change"
stance — this milestone's job is to give anyone still on the old paths a
real window and a real guide before milestone 9 deletes anything:

- Mark `access.method: primary`/`tunnel` and `AccessMethod` deprecated in
  `docs/ARCHITECTURE.md` and their own type doc comments, naming the
  release/milestone 9 is targeted for.
- Publish a migration guide for anyone on a `rancher-civo`-shaped setup
  (this session's own `acme-worker` included) — concretely, how to move
  from an `AccessMethod`-mediated cluster to an agent-mediated one: enable
  `spec.access.agent`, confirm it connects and proxies correctly, *then*
  clear `accessMethodRef`/`accessMethodClusterID`.
- **Definition of done for this milestone is not "docs merged," it's "the
  known live case is actually migrated."** Concretely: `acme-worker` itself
  moved off `rancher-civo` onto the agent, verified working, before
  milestone 9 starts — the plan doesn't get to remove a mechanism while its
  own reference deployment still depends on it.

## Milestone 9: remove `AccessMethod`, `primary`, and `tunnel`

The actual removal — scheduled, not deferred. Only starts once milestone
8's definition of done (the known live case migrated, not just docs
published) is met.

**Files removed:**

- `internal/apis/hyve/v1alpha1/accessmethod_types.go` (the whole CRD type).
- `internal/api/accessmethods.go`, `internal/api/accessmethod_mint.go` (CRUD
  handlers, the mint-Job-dispatch-plus-relay machinery, the relay listener
  registration).
- `internal/api/access.go`: `TunnelProvider` deleted outright.
  `PrimaryClusterProvider`'s `AccessMethodPrimary`-specific branch in
  `Kubeconfig` removed — but audit the rest of `PrimaryClusterProvider`
  (specifically the non-primary path that mints a caller's own resolved
  `ServiceAccountRef`) before assuming the whole type goes: confirm nothing
  besides the primary/host-cluster case still depends on it before deleting
  more than that one branch. Don't remove code this milestone hasn't
  actually confirmed is dead.
- `internal/apis/hyve/v1alpha1/clusterdefinition_types.go`: remove
  `AccessMethodRef`/`AccessMethodClusterID` from `AccessSpec`, remove
  `TunnelSpec`/`TunnelProviderRancher`/`TunnelProviderTeleport`, remove the
  `AccessMethodPrimary` constant and `Method`'s primary/tunnel values —
  regenerate `zz_generated.deepcopy.go` and
  `deploy/helm/hyve/crds/hyve.io_clusterdefinitions.yaml` (same
  `controller-gen` step milestone 1 used to add fields, now removing them —
  diff before applying, same discipline).
- `deploy/helm/hyve/crds/hyve.io_accessmethods.yaml` deleted; any
  `AccessMethod`-specific RBAC rules in `deploy/helm/hyve/templates/*rbac*.yaml`
  pruned (audit these directly rather than assuming which rules exist —
  this doc hasn't re-verified their current exact shape).
- Web console: `AccessMethodsPage.tsx`, `AccessMethodDetailPage.tsx`,
  `web/src/lib/api/accessmethods.ts`, the "Access methods" nav entry in
  `AppShell.tsx`'s `navGroups`.
- CLI: whatever in `cmd/cluster/auth.go` and `cmd/shared/apiclient.go`
  implements the `accessMethodRef`/mint request path.

**Tests:** delete the corresponding `*_test.go` files/cases outright
rather than leaving them testing removed code; any test fixture elsewhere
in the suite that happens to set `AccessMethodRef` on a `ClusterDefinition`
(a real risk — this field has been used as convenient sample data in
tests unrelated to access methods themselves) needs auditing and updating,
not just the access-method-specific test files.

**Manual verification:** `go build ./...` clean confirms nothing else in
the tree still references the removed types (the compiler does this audit
for free); a full redeploy to `k3d-hyve-local`; confirm the web console's
Access Methods nav entry and pages are actually gone, not just unlinked;
confirm `acme-worker` (migrated in milestone 8) is still working
post-removal, since that's the one live proof this milestone didn't just
delete code nobody was using.

## Cross-cutting

- **Verification discipline**: every milestone gets `go build ./... && go
  vet ./... && go test ./... && gofmt -l .` clean, then a real redeploy to
  `k3d-hyve-local` (or, for milestones 3–6, a second throwaway cluster
  playing the role of the managed/agent-side cluster — this is genuinely
  two-cluster testing, unlike almost everything else in this codebase,
  which is worth calling out as a real change to the usual dev loop) and a
  live check — CLI for backend-only milestones, browser for anything
  touching the web console.
- **A dedicated verification script is worth it here.** This mirrors the
  existing multi-tenancy plan's own `scripts/test-multi-tenant.sh` —
  standing up two real clusters (control plane + one agent-managed) and
  scripting the connect/status/proxy/exec/delete cycle is exactly the kind
  of thing worth automating once, given how many of this plan's milestones
  need the same two-cluster setup to verify by hand otherwise.

## Suggested PR breakdown

One PR per milestone, in the order above — matches the additive-first
philosophy the proposal doc's own rollout plan already committed to.
Milestones 1–3 are the ones worth landing well ahead of 4–5: they carry
the highest technical uncertainty (a new dependency, new crypto, a
protocol hyve has never spoken) and de-risking them in isolation, with zero
behavior change to anything existing, is worth more than sequencing speed.

Milestone 9 is the one PR in this list that isn't purely additive — it's
the actual removal, gated on milestone 8's definition of done (the live
case migrated, not just the docs published), not on calendar time. If
milestone 8 finds the migration doesn't hold up cleanly for some real
setup, that's a reason to fix the agent's own migration path, not to
schedule milestone 9 anyway.
