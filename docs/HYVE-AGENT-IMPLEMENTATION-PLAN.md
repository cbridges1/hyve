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
  SSH's own certificate format instead of X.509. Mounted on `Routes()`'s
  own top-level mux (alongside `/auth/login`), not `/api/` — no
  `requireAuth`/`requireRole` applies, the token itself is the entire
  authorization model.
- `deploy/helm/hyve/templates/api-ingress.yaml`: add `/agent` to the
  routed path list — found live, not anticipated up front: unlike the
  relay listener (internal-only, no Ingress at all), `/agent/bootstrap`
  has to be reachable from *outside* this cluster, since a real agent
  runs on the managed cluster being bootstrapped, not this one. Confirmed
  via a real 404 from nginx (not the Go handler) before this was added.
- `cmd/api/run.go`: wire `agentpki.LoadOrCreateCA` into `Server.AgentCA`
  at startup, using the same `clientset` already built there for
  access-method minting — soft-fail (log a warning, leave `AgentCA` nil)
  rather than `Fatal`, since hyve-agent is opt-in and an install that
  never uses it shouldn't be unable to start over a transient CA-bootstrap
  problem.

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
- `deploy/helm/hyve/templates/api-deployment.yaml`, `api-service.yaml`,
  `values.yaml` (new `api.agentBindAddress`, default `:8092`): a
  `containerPort`/Service port for the tunnel listener, added to the
  **main** `hyve-api` Service — unlike the relay listener's pod-network-
  only `hyve-api-internal` Service, a real agent dials in from outside
  this cluster's own pod network, so it needs the same kind of external
  reachability `api.ingress.host` gives the HTTP API (though, being raw
  SSH over TCP rather than HTTP, it can't actually go through
  `api-ingress.yaml` itself — that's a real install's own exposure
  decision, e.g. a `LoadBalancer` Service or a TCP-mode gateway, same
  "chart doesn't decide this for you" stance as `api.ingress.enabled`).

**Tests:** registry unit tests (register/lookup/remove-on-disconnect). A
local integration test standing up the listener and a test agent
(constructed directly with a locally-signed test cert, not via the full
CLI binary) confirming the registry reflects a real connect/disconnect
cycle.

**Live-discovered fixes** (none anticipated up front — the integration
test and manual verification below caught all five):

- `buildAgentServerConfig`'s host certificate had `ValidPrincipals`
  hardcoded to `[]string{"hyve-api"}` — but `ssh.CertChecker.CheckHostKey`
  checks that against the actual host portion of whatever address the
  agent dials (an IP, an internal DNS name, a LoadBalancer host — never
  literally the string "hyve-api" in practice). Confirmed by reading
  `golang.org/x/crypto/ssh`'s own `CheckCert`: an *empty* `ValidPrincipals`
  list means "valid for all hosts," which is the actually-correct value
  here — there's no one fixed hostname to assert.
- Both `internal/agent/connect.go`'s `connectOnce` and the test's own
  `dialTestAgent` hardcoded the SSH client username to `"hyve-agent"` —
  but `ssh.CertChecker.Authenticate` checks the *connection's own
  username* against the certificate's `ValidPrincipals`
  (`agentpki.AgentPrincipal(namespace, clusterName)`, e.g. `"acme/web"`),
  so a fixed placeholder username could never match. Fixed by reading the
  username back off `Identity.Certificate.ValidPrincipals[0]` instead of
  hardcoding it — the certificate is the one place this value is already
  correct.
- `AgentStatus.Connected` was tagged `json:"connected,omitempty"` — since
  `omitempty` drops a `false` bool entirely, `writeAgentStatus`'s merge
  patch on disconnect never sent a `"connected"` key at all, so the merge
  silently left the prior `true` in place. Every disconnect looked like a
  no-op from `kubectl`'s point of view. Fixed by dropping `omitempty` on
  this one field — `false` is exactly as meaningful as `true` here, unlike
  most other status fields in this file.
- `hyve-api`'s own RBAC (`api-rbac.yaml`) only ever granted `get` on
  `clusterdefinitions/status`, never `patch` — nothing before this
  milestone had hyve-api itself writing status (only the controller did).
  `writeAgentStatus` 403'd on every connect/disconnect, silently from the
  agent's own point of view (the tunnel itself stayed up fine). Added
  `patch` to that rule.
- `agent.Run`'s reconnect loop blocked in `conn.Wait()` with nothing
  watching `ctx.Done()` — confirmed live: a plain `kill` (SIGTERM, what
  Kubernetes actually sends first on pod termination) canceled the
  context but left the goroutine parked in `Wait()` indefinitely, so
  `status.agent.connected` stayed `true` until something eventually
  force-killed the process. Fixed by having a small goroutine close `conn`
  on `ctx.Done()`, so a graceful shutdown now unblocks `Wait()`
  immediately instead of relying on a force-kill to ever notice.

**Manual verification:** done live against `k3d-hyve-local` — the real
`cmd/agent` binary run locally against `kubectl port-forward
svc/hyve-api 8092:8092` (the "or even locally against a port-forwarded
listener for a first pass" option), using a bootstrap token minted
directly via `agentpki.GenerateBootstrapToken` against the real cluster
(no CLI/reconcile-loop token issuance exists yet — that's milestone 4).
Confirmed the full cycle: `kubectl get clusterdefinition agent-test -o
yaml` showed `status.agent.connected: true` with `lastConnectedAt` set
immediately after connect; a plain SIGTERM against the agent process
flipped it to `false` with `lastDisconnectedAt` set within ~1s (after the
`ctx.Done()` fix above — before it, only a force-kill worked at all,
and only after the RBAC/omitempty fixes above did disconnect status
writes take effect rather than silently failing or silently no-op'ing).
Also confirmed a pod restart correctly reuses the persisted
`hyve-agent-cert` Secret (no bootstrap token needed on the second
connect).

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

Also touched: `internal/types` (new `AgentSpec`/`AppliedAgent`, mirroring
the CRD's own — `internal/reconcile` is mode-agnostic and needed these to
reach it, same "primary needed to reach internal/types" precedent
`AccessMethod` already established), `internal/apis/hyve/v1alpha1` (new
`AppliedAgent` status type, CRD regenerated), `internal/crdconv` (wires
`Agent`/`AppliedAgent` both directions), `internal/agentpki/issuer.go`
(new — `TokenIssuer`, the concrete client-go-backed implementation of
`reconcile.AgentTokenIssuer`, wired in by `cmd/controller/run.go` alongside
the same clientset it already builds for `KubernetesJobStepRunner`/
`JobRunner`), `deploy/helm/hyve` (new `controller.agent.controlPlaneURL`/
`tunnelAddress` values, `controller.hyveConfig.defaultAgentImage`, a new
RBAC `create` rule on Secrets for `TokenIssuer`), `deploy/Dockerfile.agent`
(new — hyve-agent's own dev-build image, genuinely separate from the main
`hyve` image per `cmd/agent/main.go`'s own standalone-binary precedent).

**Tests:** `internal/reconcile/agent_test.go` — a fake "kubectl" script put
first on `PATH` (this package has no Kubernetes client dependency at all;
every mutating call already shells out to the real `kubectl` binary by
name, so intercepting at the process-exec boundary is the only seam
available, and the established convention here is unit-testing pure logic
while leaving real `kubectl` calls to live verification — this test
exercises the real `reconcileAgent` end-to-end instead, since the decision
logic and the manifest rendering are exactly what's worth covering
together). Covers: install when `Enabled`, no-op skip when config is
unchanged (no token re-minted), removal when flipped back to `false`,
`Agent.Proxy` alone (without `Enabled`) ignored rather than erroring per
the spec's own documented dependency, proxy bindings applied/removed
independently of the core install, and a soft no-op (not an error) when
the controller has no `AgentTokenIssuer`/control-plane URL/tunnel address
configured at all (local/CLI mode's own default).

**Manual verification (the concrete proof this decision mattered):** flip
`spec.access.agent.enabled: true` on an *already-existing* real cluster —
not one just created — and confirm the agent installs and connects on the
very next reconcile, with no recreation and no workflow to remember to
run. Done live against `k3d-hyve-local`, self-referentially (an `authOnly`
fake-driver `ClusterDefinition` whose `auth` op exports a real, working
in-cluster kubeconfig for the same cluster hyve-controller itself runs
on — the only way to get a genuinely reachable target cluster without
standing up a second one) — confirmed both a fresh-create-with-agent-
enabled cluster and, separately, an already-ACTIVE cluster that had `agent:
{enabled: true}` patched onto it after the fact both installed and
connected on their very next reconcile, with `status.appliedAgent`/
`status.agent.connected` populated correctly. Also confirmed a
`proxy: true → false` toggle removes exactly the two proxy
`ClusterRoleBinding`s while leaving the core install untouched.

**Live-discovered fixes** (four, all real — three were reachable in
completely ordinary, non-error operation, not edge cases):

- `types.AppliedAgent.ConfigHash` (via `agentConfigHash`) originally hashed
  only `(proxy, image)` — but `HYVE_CONTROL_PLANE_URL`/`HYVE_TUNNEL_ADDRESS`
  are *also* embedded directly in every agent's rendered `Deployment` env,
  so a real change to either (hyve-api migrating to a new address) would
  never re-apply any already-installed agent — the "up to date, skip"
  check had no way to notice. Fixed by folding both into the hash.
- `internal/controller/reconciler.go`'s `Reconcile` did a single re-fetch
  immediately before its own final `setCondition` status write, with a
  comment explaining *why* the re-fetch was there (to avoid reverting
  whatever `ReconcileOne` had already written mid-cycle via
  `SaveClusterDefinition`) — but a single re-fetch only narrows that race,
  it doesn't close it. Confirmed live: milestone 4's own agent-install path
  (the first `ReconcileOne` step to routinely call `SaveClusterDefinition`
  mid-cycle on a reconcile that previously wrote nothing else) hit "the
  object has been modified" on its very first successful run — a real,
  routinely-reachable sequence, not a rare edge case. Fixed by wrapping the
  re-fetch-and-write in `retry.RetryOnConflict`, the standard client-go
  pattern for exactly this, rather than a single attempt that just logs a
  scary-looking warning on every success.
- `deploy/helm/hyve/templates/controller-rbac.yaml` granted no `create` on
  Secrets at all (only a narrowly-scoped `get` on `hyve-cli-secrets`) —
  `agentpki.TokenIssuer.IssueBootstrapToken` needs to create a fresh
  `hyve-agent-bootstrap-*` Secret per install/re-apply and would 403
  without it. Added an unscoped `create` rule (RBAC `resourceNames` can't
  scope `create` at all — same gotcha `api-rbac.yaml`'s own
  `hyve-cli-secrets` comment already documents for the identical reason).
- Self-inflicted test-methodology artifact, not a product bug, but worth
  recording since it cost real debugging time: reusing the *same* physical
  target cluster across several different `ClusterDefinition` test objects
  (unavoidable for a self-referential live test with no second cluster
  available) meant a later test's agent pod kept finding and reusing an
  *earlier* test's persisted `hyve-agent-cert` Secret (fixed name, fixed
  namespace, keyed by nothing test-specific) — silently asserting the
  wrong identity rather than bootstrapping fresh. A real deployment never
  hits this (one `ClusterDefinition` per physical cluster, for its whole
  lifetime); only a repeated self-referential test does.

**Confirmed, not fixed (deliberately out of scope):** deleting a
`ClusterDefinition` outright (a real `kubectl delete`, not flipping
`enabled` back to `false`) routes through `reconcileDelete`'s finalizer
path, not the ACTIVE-status branch `reconcileAgent` lives on — so it never
runs hyve-agent's own removal step. In real usage this is harmless (the
driver's own delete operation destroys the whole underlying cluster,
hyve-agent included, as a side effect); confirmed live only because this
milestone's self-referential test deleted the `ClusterDefinition` *without*
a real driver deleting anything underneath it, which is not how this ever
happens outside of a test built this way.

## Milestone 5: proxy path — impersonation, tenant scoping, real `kubectl`

The payoff milestone: `spec.access.agent.proxy: true` becomes something a
caller can actually use.

**Files (new):**

- `internal/api/agent_proxy.go` — the proxy handler, mounted at
  `/api/agent-proxy/{name}/{rest...}` — deliberately *not*
  `/agent-proxy/{namespace}/{name}/` as originally sketched here: the
  namespace is never taken from the URL at all, always from
  `TenantNamespace(r)` (the caller's own verified session/act-as
  namespace), exactly matching `handleGetCluster`'s own established
  pattern for every other cluster-scoped route — a caller-suppliable
  namespace segment would have been exactly the cross-tenant hole the
  Tests section below guards against. Nested under `/api/` (inside
  `apiMux`) rather than a separate top-level mount like `/proxy/`, so it
  inherits `requireAuth`+`requireRole` from `Routes()`'s existing apiMux
  wrapping for free — unlike `/proxy/`, whose caller presents a real
  Kubernetes token, not a hyve one, this route needs the same session
  auth every other `/api/*` route already has. A `Get` existence check
  against `(TenantNamespace(r), name)` (the same check `handleGetCluster`
  already makes), *then* a registry lookup — per the resolved
  multi-tenancy design, in that exact order. Builds an
  `httputil.ReverseProxy` whose
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

Also touched, beyond the two files above: `internal/agentpki/proxy.go`
(new — `ProxyChannelType`/`ProxyChannelPayload`/`NewProxyChannelPayload`,
`KubernetesAPIServerAddr`, and the two `AgentProxyAdminGroup`/
`AgentProxyReadOnlyGroup` constants, moved here from
`internal/reconcile/agent.go` so both the reconcile step that provisions
the `ClusterRoleBinding`s and this milestone's proxy handler share one
literal source of truth instead of two hand-synced copies —
`internal/reconcile/agent.go` itself now imports `agentpki` for them);
`internal/agentpki/protocol.go` (`HeartbeatPayload` gains
`ServiceAccountToken`); `internal/agent/heartbeat.go` (reads the agent's
own mounted projected-token file fresh every heartbeat, plus fires one
immediately on connect rather than waiting out the first 30s interval);
`internal/agent/connect.go` (`ssh.Dial` → `net.Dial`+`NewClientConn`+
`NewClient`, since `ssh.Dial`'s own shortcut discards exactly the
incoming-channel plumbing `HandleChannelOpen` needs) and
`internal/agent/proxy.go` (new — the agent-side channel accept loop, a
pure raw byte-relay to `kubernetes.default.svc`, no HTTP/TLS of its own);
`internal/api/agentregistry.go` (`AgentConnection.Version` changed from a
field to a mutex-guarded method, `ServiceAccountToken` added alongside it
— both written by `drainAgentRequests`, both now read from a different
goroutine by `agent_proxy.go`); `internal/api/kubeconfig_handler.go`
(dispatches to `AgentProvider` ahead of the `Access.Method` switch
entirely, since Agent/Proxy is orthogonal to Method); `internal/api/
auth_context.go` (a live-discovered fix — see below);
`deploy/helm/hyve/templates/api-rbac.yaml` (`events` gains `create`, for
the audit-trail write).

**Tests:** `internal/api/agent_proxy_test.go` — cross-tenant-rejection
(styled like this session's own
`TestHandleCreateEnvironment_RejectsReservedNames` regression tests: prove
a namespace-B caller 404s against a namespace-A cluster, not just that the
happy path works), proxy-not-enabled (both a `Proxy: false` and a nil
`Agent` spec) 403s, not-connected/no-registry/no-credentials-yet 503s, and
a pure `agentImpersonationGroup` mapping unit test. Role→impersonation-
group mapping is verified separately, in
`internal/api/agent_proxy_rbac_test.go`, against a *real* local
apiserver's own RBAC decision (not just that the header string got
built) via `sigs.k8s.io/controller-runtime/pkg/envtest` (already a
transitive dependency, and its binaries happened to already be installed
on this dev machine) — `admin` group actually creates a Namespace via a
real `cluster-admin` ClusterRoleBinding, `read-only` actually lists Pods
but is genuinely `Forbidden` creating one, and the agent's own
un-impersonated identity (impersonate verb only) is genuinely `Forbidden`
doing anything else — using `rest.Config.Impersonate` (client-go's own
supported mechanism for the identical `Impersonate-User`/
`Impersonate-Group` headers `buildAgentReverseProxy`'s Director sets) so
the test doesn't need this package's own HTTP handler, a fake SSH tunnel,
or a fake agent connection just to reach the same two header values. Skips
(does not fail) when envtest binaries aren't available locally, so `go
test ./...` stays runnable anywhere.

**Manual verification (the real test of the whole proposal):** done live
against `k3d-hyve-local`, self-referentially (the same `authOnly`
fake-driver technique milestone 4's own live verification used) — real
GET/POST/watch requests through a minted `/api/agent-proxy/<name>`
kubeconfig's own token confirmed: reads and writes both actually reach the
real apiserver and return real data (a `ConfigMap` genuinely created,
then genuinely read back); `?watch=true` streams real incremental events
rather than buffering (the plan's own core concern about the tunnel
choice); the `admin`→`read-only` role split holds live, not just in the
envtest suite (a real read-only session's `GET` returned `200`, its `POST`
returned a real `403 Forbidden` naming the impersonated user); the
audit-trail `Event` fires for every proxied request, correctly attributed
to the calling user, once the RBAC fix below was applied. **Not** done
live: `kubectl exec`/`kubectl logs -f`/`kubectl get --watch` through a
real `kubectl` binary specifically — see the live-discovered limitation
below for why, and why the streaming/write verification above is still
real evidence for those code paths despite that gap.

**Live-discovered fixes and findings:**

- `deploy/helm/hyve/templates/api-rbac.yaml` granted `hyve-api`'s own Role
  only `get`/`list` on `events`, never `create` — `emitAgentProxyEvent`'s
  audit-trail write 403'd silently (the proxied request itself still
  succeeded; only the audit Event failed to be written) until `create` was
  added.
- `internal/api/auth_context.go`'s `handleAuthContext` only ever checked
  `spec.access.method != ""` to decide "this cluster doesn't use
  client-side auth, tell the caller to use `GET /api/kubeconfig` instead"
  — it never checked `spec.access.agent.proxy` at all. Since Agent/Proxy
  is deliberately orthogonal to Method (a real agent+proxy cluster
  normally leaves Method unset entirely), `hyve cluster auth <name>`
  against a real agent+proxy cluster would have silently taken the
  *wrong* path — attempting to run the driver module's own `auth.yaml`
  locally, client-side, instead of ever reaching the agent-proxy
  kubeconfig dispatch. Fixed by adding the same rejection
  `handleAuthContext` already had for `Method`, now also for
  `Agent.Proxy`; covered by a new regression test,
  `TestHandleAuthContext_RejectsAgentProxy`.
- **Environment limitation, not a hyve bug** (confirmed live, cost real
  debugging time): a real `kubectl`/client-go client refuses to attach
  *any* credential — bearer token, client cert, or basic auth — to a
  request whose target URL scheme is `http://` rather than `https://`.
  This is `k8s.io/client-go/tools/clientcmd`'s own `DirectClientConfig.
  ClientConfig()`, gated by `restclient.IsConfigTransportTLS` (a bare
  `baseURL.Scheme == "https"` string check, confirmed by reading the
  actual resolved client-go source) — not a flag, not something
  `insecure-skip-tls-verify` overrides, and not anything specific to this
  milestone's own code. `k3d-hyve-local`'s local-dev Ingress has never
  terminated TLS (plain HTTP throughout this whole project's local
  testing), which means this exact limitation already applied equally to
  the *pre-existing* `PrimaryClusterProvider`/`/proxy` kubeconfig path —
  it was simply never exercised through a real `kubectl` binary before
  now. A real deployment's `PublicBaseURL` is `https://...` behind a real
  Ingress/LB, where this never triggers at all. Standing up real Ingress
  TLS for this one local dev cluster was judged out of scope for this
  milestone (a genuinely separate, environment-level piece of
  infrastructure, not specific to hyve-agent); verified the underlying
  mechanism instead via direct authenticated HTTP requests (curl, which
  has no such client-side restriction) exercising the exact same
  server-side code path `kubectl` would have — see "Manual verification"
  above.

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

**Scope, as actually run:** the real Civo cluster half of this milestone
was explicitly descoped by the user after two rounds of discussion — a
real Civo cluster is billed, and (separately, the actual blocker) a real
external cluster's hyve-agent has no path back to this laptop's
k3d-hyve-local control plane without standing up genuinely new networking
infrastructure (a public HTTPS endpoint + a reachable raw-TCP endpoint —
neither ngrok nor Cloudflare Tunnel's free tier cover both without an
account this agent isn't able to create on the user's behalf). Directed
instead to "just rely on k3d for now" — the full live cycle below was run
self-referentially, the same technique milestones 4/5 already established,
now driven through a real `Template` (not raw `ClusterDefinition` YAML) to
exercise the actual `hyve template create`/`hyve cluster create --template`
path end to end, which is what surfaced this milestone's own real bug (see
below). `template-civo-agent.yaml` was still written and committed to
nexus-config as the real, correct artifact for whenever real external
reachability exists — it's simply never been created-from live in this
session.

**Live-discovered fix — a real, pre-existing bug, not specific to
hyve-agent:** `hyve template create m6-agent-test -f <file-with-spec.access.agent-set>`
followed by `hyve cluster create --template m6-agent-test` produced a
cluster with `spec.access: {}` — completely empty. Root cause:
`hyvev1alpha1.TemplateSpec` (the `Template` CRD's own spec type) never had
an `Access` field *at all* — not added when `AccessMethodRef`/`Method`
were introduced, still missing when `Agent` was added in milestone 1. A
Kubernetes CRD silently drops any property its schema doesn't declare, so
every one of the three ways to set `spec.access` on a Template (`--set`,
`-f` a file, or a direct `kubectl apply`) hit the exact same silent
data-loss, regardless of which one was used — confirmed live by a real
Template CRD dropping a hand-written `spec.access.agent` block. This
explains why `template-civo-rancher-agent.yaml`'s own precedent (predating
this session) documents a manual `kubectl patch clusterdefinition ...
access.accessMethodRef` step performed *after* `hyve cluster create`
rather than setting it in the Template at all — that workaround was
already routing around this same gap, just never diagnosed as a Template
schema bug before now. Fixed by adding `Access AccessSpec` to
`TemplateSpec` (`internal/apis/hyve/v1alpha1/template_types.go`) and
threading it through `RenderClusterDefinitionSpec`
(`internal/apis/hyve/v1alpha1/render.go`); CRD regenerated;
`internal/api/templates.go`'s own create/update handlers needed no change
at all, since `createTemplateRequest`/`updateTemplateRequest` already
embed `hyvev1alpha1.TemplateSpec` directly rather than a hand-copied DTO.
Covered by a new regression test,
`TestRenderClusterDefinitionSpec_CopiesAccess`.

**Full live cycle, as actually verified** (self-referentially, on
`k3d-hyve-local`, via a real `Template` → `hyve cluster create --template`
→ real reconcile → real proxy traffic → real teardown):

- **Create → agent installs and connects**: `hyve template create` +
  `hyve cluster create --template` produced a `ClusterDefinition` whose
  `spec.access.agent` correctly carried through (post-fix); hyve-agent
  installed and reported `status.agent.connected: true` on the very first
  reconcile cycle, no manual follow-up step — the actual point of this
  milestone's own testbed, and the first time the *Template* path (not a
  hand-written `ClusterDefinition`) proved this.
- **Status visible in the CLI**: only partially — `hyve cluster show`
  and `hyve cluster logs` currently surface neither `status.agent` nor
  `status.appliedAgent` at all; confirmed via `kubectl get -o yaml`
  instead. Not fixed here — this is milestone 7's own explicit scope
  ("CLI + web console surfacing"), not a milestone 6 blocker.
- **Proxy works, including `exec`/`watch`**: verified with *real*
  `k8s.io/client-go/tools/remotecommand` (SPDY exec, the same mechanism
  `kubectl exec` itself uses) and the real typed client's `Watch`
  interface — not curl this time. A real command (`echo ...`, `hostname`)
  actually ran inside a real pod and streamed real stdout back through
  the full tunnel; a real `Watch` delivered a real `ADDED` event. This
  resolves milestone 5's own documented gap (`kubectl exec`/`watch` never
  verified through real client-go machinery, only curl) — the *only*
  difference from a literal `kubectl` invocation is building `rest.Config`
  directly instead of loading it via `clientcmd` from a kubeconfig file,
  which is what milestone 5 found refuses to attach credentials over
  `http://` — every server-side code path (auth, the existence/registry
  checks, the SSH tunnel, the agent's own relay, the real apiserver) is
  identical either way.
- **Delete → confirm nothing orphaned**: hyve's own delete path
  (`reconcileCluster`'s `Delete && ACTIVE` branch) never calls
  `reconcileAgent` at all — for a real driver this is harmless (the
  driver's own delete operation destroys the whole cluster, agent
  included, as a side effect), but this self-referential test's fake
  driver destroys nothing. Verified the *intended* clean-shutdown
  sequence instead: `spec.access.agent.enabled: false` first (the
  first-class, already-tested milestone 4 mechanism) — confirmed live
  that this alone removed every object hyve-agent had created
  (`Deployment`, `ServiceAccount`, `Role`, `RoleBinding`,
  `ClusterRole`, both proxy `ClusterRoleBinding`s — `kubectl get` for
  each came back empty) — *then* deleted the `ClusterDefinition` itself,
  which removed cleanly with nothing left behind. This is the sequence a
  real decommissioning flow should actually use (disable the agent first,
  destroy the cluster second), and it's the one milestone 4 was actually
  built and tested against — not a workaround adopted only because this
  test's own driver doesn't destroy anything.

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

**Files, as actually touched:** `internal/api/clusters.go` (`clusterDTO`
gains `Agent`/`AgentStatus`, mirroring `spec.access.agent`/`status.agent`
— the CRD's own status field is a value type with no `omitempty`, on
purpose, so `AgentStatus` mirrors that exactly rather than a pointer);
`cmd/shared/apiclient.go` (`ClusterDTO` gains the same two fields — this
package already imports `hyvev1alpha1`, so no new dependency); `cmd/
cluster/auth.go` (the fifth branch, plus a new `Long:` paragraph
describing it — `cd` is now captured at function scope in `authClusterAPI`
so the final branch can inspect `cd.Agent.Proxy` before choosing its
message); `cmd/cluster/api.go` (`showClusterAPI` prints Agent/connected/
version/last-connected/last-disconnected when configured);
`internal/api/server.go` (new shared `emitClusterEvent` helper — factored
out once a *second* call site needed the exact same Event-construction
boilerplate `emitAgentProxyEvent` from milestone 5 already had);
`internal/api/agent_listener.go` (`handleAgentConnection` now emits a real
`AgentConnected`/`AgentDisconnected` Event on every transition, not just a
status-field write — giving `hyve cluster logs`/the web console's
"Recent activity" panel real connect/disconnect *history*, which a
current-state-only status snapshot can't; `AgentProxyRequest` events from
milestone 5 already flowed through the *existing*
`GET /clusters/{name}/events` mechanism with zero changes needed, since
that endpoint reads every Event on the object regardless of Reason);
`web/src/lib/api/types.ts` (`AgentSpec`/`AgentStatus` types, added to both
`ClusterSummary` — the DTO's top-level fields — and
`ClusterDefinitionSpec.access` — for `SpecEditor`'s own typed value,
though confirmed unnecessary for the toggle to actually work: `SpecEditor`
round-trips through `js-yaml`'s `dump`/`load` on a generic object, not a
TS-type-enforced serializer, so an untyped `agent:` block would have
survived a save regardless); `web/src/routes/ClusterDetailPage.tsx` (the
status indicator itself — a green/grey connected-dot badge plus a
"Proxy enabled" badge and version/timestamp, at the top of "Recent
activity", only rendered when `cluster.agent?.enabled`).

**Verified live**, self-referentially on `k3d-hyve-local` (same technique
as milestones 4-6): `hyve cluster show` prints `Agent: enabled (proxy:
true)` / `Connected: true` / `Version` / `Last connected`; `hyve cluster
auth` against the same cluster printed the new, distinct agent-proxy
message ("kubectl traffic is relayed through the API to this cluster's
own agent, not a direct connection") rather than the generic server-side-
auth one; `hyve cluster logs` showed real `AgentConnected` **and**
`AgentDisconnected` events after forcing a real reconnect (deleting the
agent pod) — confirming the new event emission fires on both transitions,
not just one; the web console's cluster detail page, viewed in a real
browser, rendered the "● Agent connected" / "Proxy enabled" / "vdev" /
"since ..." badge row exactly as designed, with the same events listed
below it via the pre-existing events panel; the "Edit spec" YAML editor
correctly showed `access.agent.enabled: true` / `proxy: true` for a real
cluster, confirming the "already works via SpecEditor" claim held for
real, not just in theory. No new bugs found this milestone — the DTO/CLI/
web layers were new surface area with no existing behavior to conflict
with.

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

**Actually done, as of this update: the doc-only half only.** Written —
`docs/HYVE-AGENT-MIGRATION-GUIDE.md` (new), `docs/ARCHITECTURE.md`'s new
"Cluster access paths" section, and `// Deprecated:` notices on
`AccessMethod`, `AccessMethodTunnel`/`TunnelSpec`, `AccessMethodPrimary`,
and `AccessSpec.AccessMethodRef`/`AccessMethodClusterID`
(`internal/apis/hyve/v1alpha1/{accessmethod,clusterdefinition}_types.go`),
CRDs regenerated. **Not done: the live migration itself, and this
milestone's own definition of done is explicitly not met.**

Found live, before any migration work could even start: `acme-worker`
(this plan's own cited "known live case") no longer exists on
`k3d-hyve-local` — `kubectl get clusterdefinition -A` shows only `local`.
It appears to have expired the same way the unrelated `civo-test` cluster
did earlier in this project's history, not been deliberately migrated.

This also surfaced a real gap the original plan text didn't fully resolve:
milestone 9's title bundles `primary` in with `AccessMethod`/`tunnel` as
if all three have an equally straightforward agent-based replacement, but
`primary` doesn't — it's the *host* cluster's own access path (the one
`hyve-controller`/`hyve-api` run on), and "every cluster including the
host is agent-connected symmetrically" (the architecture proposal's own
resolved direction) still leaves open exactly how the current
superadmin-only gate and `PrimaryClusterProvider`'s other behavior map
onto that. Raised with the user directly; explicit decision: **write the
deprecation notices and migration guide now (this document), but do not
attempt a live migration or start milestone 9's removal yet** — see
`docs/HYVE-AGENT-MIGRATION-GUIDE.md`'s own "Host cluster access" section
for the honest, still-open state of that specific question, and its own
top-of-file "Status of this guide, honestly" note for the
`AccessMethod`/`tunnel` half (a reasoned-through procedure, not yet
proven against a live cluster).

## Milestone 9: remove `AccessMethod`, and `PrimaryClusterProvider`'s minting

**Actually done — but under a materially different, user-clarified scope
than this section originally specified.** Milestone 8's own gate (a real
live migration proven, not just docs) was never actually met —
`acme-worker` had already expired, and no replacement live case
materialized. Rather than stay blocked on that indefinitely, the scope was
renegotiated directly with the user, who gave two explicit, load-bearing
decisions that supersede this section's original plan:

1. *"AccessMethod should be removed. If admin wants cluster to accessed
   via tunnel it should be specified in the clusterdefinition."* — the
   `AccessMethod` CRD is removed outright; `access.method: tunnel`
   (the pre-existing inline field, a distinct thing from the `AccessMethod`
   CRD) is explicitly **kept, unchanged, not deprecated**. The milestone's
   own title ("remove `AccessMethod`, `primary`, and `tunnel`") was wrong
   about `tunnel`.
2. *"host cluster should default to the auth method defined in the
   module"* — `PrimaryClusterProvider` and its hardcoded
   `TokenRequest`/`/proxy`-minting mechanism are removed entirely, but
   `access.method: primary`/`AccessMethodPrimary` itself stays, now as a
   pure identifying marker (see below for why removing the value too would
   have silently broken something real). Live-verified: `hyve cluster auth
   local` no longer 409s at `handleAuthContext`'s Method-set check, reaches
   the same client-side auth-context path as any other default-auth
   cluster, and correctly fails with "failed to resolve driver module" (not
   a 409, not a panic) since `local` has no real driver assigned yet — the
   expected, documented state until an admin gives it one (see
   `docs/HYVE-AGENT-MIGRATION-GUIDE.md`'s "Host cluster access" section).

**Why `primary` the marker survived when `primary` the mechanism didn't:**
an audit before writing any code (following this section's own original
"don't remove code this milestone hasn't confirmed is dead" instruction)
found `cmd/migrate_resolve.go`'s `resolveCurrentHostKubeconfigPath` — `hyve
migrate cluster`'s host-resolution — depends on `access.method: primary` as
a structural convention to find "the one ClusterDefinition that represents
this install's own host cluster," completely independent of
`PrimaryClusterProvider`'s minting behavior. Removing the value along with
the mechanism would have silently broken `hyve migrate cluster` with no
warning. `resolveCurrentHostKubeconfigPath` was rewritten to resolve a
kubeconfig via the same client-side auth-context flow `cmd/cluster/auth.go`
uses (new `resolveViaAuthContext` helper) instead of the old
`GET /api/kubeconfig` call, since that endpoint no longer serves `primary`
at all.

**Files actually removed:** `internal/apis/hyve/v1alpha1/accessmethod_types.go`;
`internal/api/accessmethods.go`, `internal/api/accessmethod_mint.go` (+
their `_test.go` files); `internal/k8sjob/push.go` (its only caller was the
deleted mint handler); `deploy/helm/hyve/templates/api-internal-service.yaml`
(the relay-only internal Service); `deploy/helm/hyve/crds/hyve.io_accessmethods.yaml`;
the `hyve-host-admin` ServiceAccount+ClusterRoleBinding in
`api-access-roles.yaml`; `cmd/cluster/auth_access_method.go` (+ its
`_test.go`); the web console's `AccessMethodsPage.tsx`,
`AccessMethodDetailPage.tsx`, `web/src/lib/api/accessMethods.ts`, and the
"Access methods" nav entry in `AppShell.tsx`.

**Files substantially edited:** `internal/api/access.go` (`PrimaryClusterProvider`
and `ServiceAccountRefConfig` types deleted outright — audited first and
confirmed the whole type's only real caller was the primary/host-cluster
case, so unlike the original plan's caution, nothing needed to survive
piecemeal); `internal/apis/hyve/v1alpha1/clusterdefinition_types.go`
(`AccessMethodRef`/`AccessMethodClusterID` fields removed from `AccessSpec`;
`AccessMethodPrimary`'s doc comment rewritten to describe the marker-only
semantics; `AccessMethodTunnel`/`TunnelSpec`/`TunnelProviderRancher`/
`TunnelProviderTeleport`'s `Deprecated:` notices from milestone 8 reverted,
since tunnel isn't being removed after all); `internal/types/types.go`
(same field removal; `AccessMethod` kept, its doc comment rewritten);
`internal/reconcile/manager.go`'s `ReconcileOne` (the `access.method:
primary` → skip-driver-reconciliation special case deleted — a
primary-marked cluster is validated/reconciled exactly like any other now);
`internal/api/server.go` (`PrimaryProvider`/`RelayBaseURL`/`MintTimeout`/
`mintPending` fields, `RelayRoutes()`, `contextKeyServiceAccountRef`/
`ServiceAccountRefFromContext` all removed — confirmed via grep the
service-account-ref context value had exactly one reader, the deleted
`PrimaryClusterProvider`); `internal/api/kubeconfig_handler.go` (the
`AccessMethodPrimary` dispatch case removed from `handleKubeconfig`'s
switch — a primary-marked cluster now falls through to the same 409 every
other default-auth cluster gets); `internal/api/auth_context.go`
(`handleAuthContext`'s Method-set rejection gained a carve-out: `Method ==
AccessMethodPrimary` is treated as default/client-side-compatible, not
rejected); `cmd/api/run.go` (relay listener goroutine, `--relay-*`/
`--host-service-account` flags, `PrimaryClusterProvider`/
`ServiceAccountRefConfig` construction all removed; in-cluster CA reading
and `/proxy` construction kept — `/proxy` stays generic, reusable
infrastructure any driver module's `auth.yaml` can route through, not
wired to any one mechanism); `internal/crdconv/clusterdefinition.go` +
`totypes_reverse.go` (drop the `AccessMethodRef`/`AccessMethodClusterID`
field conversions both directions); `cmd/cluster/auth.go` (the
`--set KEY=VALUE`/`credentialParams` flag and the `AccessMethod`-dispatch
branch in `authClusterAPI` removed — nothing else used `credentialParams`);
`cmd/shared/apiclient.go` (`AccessMethodDTO`/`GetAccessMethod`/
`MintAccessMethodKubeconfig`(`Response`) removed; `ClusterDTO`'s
`AccessMethodRef`/`AccessMethodClusterID` fields removed, `AccessMethod`
itself kept); `deploy/helm/hyve/templates/api-rbac.yaml` (the `accessmethods`
resource rule removed; `jobs`/`serviceaccounts/token` verbs narrowed back
to what's actually still used — `create`/`delete` on `jobs` and `create` on
`serviceaccounts/token` were solely for the deleted mint machinery);
`deploy/helm/hyve/values.yaml` + `api-deployment.yaml` + `api-service.yaml`
+ `api-ingress.yaml` (relay/host-service-account values, args, and doc
comments removed); doc-comment-only fixes in
`hyveaccessbinding_types.go`, `agentpki/ca.go`, `kubeconfig/merge.go`,
`internal/api/agent_proxy.go`, `internal/api/proxy.go`,
`internal/apis/hyve/v1alpha1/template_types.go` (all stale mentions of the
removed types).

**Deliberately NOT removed:** `HyveAccessBinding.Spec.ServiceAccountRef`
(the field, its two writers in `cmd/api/create_user.go`/
`internal/api/accounts.go`, and the `hyve-access-admin`/
`hyve-access-readonly` ServiceAccount+RoleBinding scaffolding) — nothing
reads it back via the deleted `ServiceAccountRefFromContext` anymore, but
it's a plausible role→ServiceAccount convention independent of
`PrimaryClusterProvider`'s fate, wasn't named in this milestone's file
list, and removing it would have been a broader, unrequested change. Only
`hyve-host-admin` (purpose-built for `PrimaryClusterProvider`'s
`HostServiceAccountRef`, with no purpose independent of it) was removed.

**Tests:** deleted the access-method-specific test files/cases outright
(`internal/api/access_test.go`'s `PrimaryClusterProvider` block,
`cmd/cluster/auth_access_method_test.go` in full); rewrote
`TestHandleKubeconfig_DispatchesToPrimaryProvider` →
`TestHandleKubeconfig_PrimaryMarkerIsNotServedHere` and
`TestHandleAuthContext_RejectsPrimaryCluster` →
`TestHandleAuthContext_AllowsPrimaryCluster`, both asserting the new,
inverted behavior rather than just deleting the coverage. No stray
`AccessMethodRef` sample-data fixtures found elsewhere in the suite (the
compiler would have caught any, since the field no longer exists on either
struct).

**Manual verification:** `go build ./... && go vet ./... && gofmt -l .`
clean; full `go test ./...` clean; `cd web && npx tsc -b` clean; `helm
lint`/`helm template` clean on the pruned chart; a real redeploy to
`k3d-hyve-local` via `scripts/install-local.sh`; confirmed live that
`local` (the pre-existing `primary`-marked `ClusterDefinition`, with
`spec.driver: {}` left over from before this milestone) now produces a
real, continuous `"no driver specified"` reconcile error instead of being
silently skipped — this is the expected cost of the new design, not a
bug, and is documented in `docs/HYVE-AGENT-MIGRATION-GUIDE.md`'s "Host
cluster access" section as a required follow-up (give `local` a real
driver module) rather than something this milestone silently papered
over; confirmed live via a throwaway superadmin test account that `hyve
cluster auth local` reaches the client-side auth-context path (not a 409)
and fails for the expected reason; confirmed `GET /healthz` and the web
console's Access Methods nav entry/pages are gone.

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
