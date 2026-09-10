# Proposal: cloud-portable exposure and host-cluster access

**Status:** implemented. The chart/ingress cleanup, and the reconciler
change routing the host cluster through hyve-agent, are both done — see
"Implementation notes" at the end for exactly what changed and what
wasn't (couldn't be) verified live. Written to capture a design direction
worked out in conversation, prompted by a live failure found while
debugging `hyve cluster auth host` against a local k3d cluster with no
TLS in front of it.

## Summary

Two related decisions:

1. **hyve's helm chart owns no Ingress and makes no exposure decision at
   all.** `deploy/helm/hyve/templates/api-ingress.yaml`/`ui-ingress.yaml`
   are deleted; the chart creates only `Service`s (`hyve-api`, `hyve-ui`)
   for a deployer to point their own Ingress/LoadBalancer/routing at.
   Local dev's Ingress (Traefik-specific, nip.io-hostname, k3d-only) moved
   into `scripts/install-local.sh` as a plain `kubectl apply`, entirely
   outside the helm release.
2. **The host cluster (`access.method: primary`, `HostProvider`) stops
   being a special case and goes through hyve-agent like every other
   cluster, when asked to.** `HostProvider`'s `/proxy` path pins trust to
   the in-cluster Kubernetes API server's own CA, which is fundamentally
   incompatible with EKS, GKE, and AKS: none of them expose their control
   plane's CA private key to anyone, ever. There is no configuration that
   fixes this — it's a hard architectural ceiling on `HostProvider`, not a
   deployment detail to work around. `access.method: primary` is kept
   working exactly as before for anyone who doesn't opt into the agent —
   this is additive, not a breaking change.

## Why now

The user's stated bar is explicit: this needs to work on EKS, GCP
(GKE), and Azure (AKS) "in a universal way like Rancher or Teleport,"
not just on self-managed clusters where the operator happens to control
the control plane's own PKI. That bar ruled out the fix that was actually
in flight (mint an Ingress TLS cert signed by the cluster's own CA — see
`docs/HYVE-AGENT-MIGRATION-GUIDE.md`'s TLS discussion) the moment it was
stated, because that fix only works where the CA's private key is
reachable at all, and the three major managed control planes never expose
it.

## How Rancher and Teleport actually solve this

Worth being precise about this, since it's the concrete reason the
recommendation below is "use the agent path for everything, including the
host cluster" rather than "make `HostProvider` smarter":

- **Neither product ever trusts the local cluster's own apiserver CA for
  its own public-facing TLS** — not even for the cluster their own
  control plane happens to run on. Rancher's "local" cluster is
  registered through `cattle-cluster-agent` exactly like every downstream
  cluster: an outbound connection authenticated with a Rancher-issued
  token, decoupled from whatever PKI that cluster's apiserver uses.
  Teleport's `kubernetes_service` agent and join tokens work the same
  way.
- There is no special case for "the cluster I'm running on" in either
  product. One mechanism, used everywhere, and it's cloud-agnostic as a
  direct consequence of never touching the local CA at all.
- Teleport's own chart defaults to a `Service: LoadBalancer`, not a
  Kubernetes `Ingress` — Ingress controllers aren't a given across clouds
  (vanilla EKS ships none; GKE and AKS default to different controllers
  with different annotation dialects), while a cloud LoadBalancer Service
  is natively provisioned on all three with no extra installation.
  Teleport's own proxy process terminates TLS itself, rather than relying
  on cloud-specific LoadBalancer TLS-termination annotations (which vary
  enough between AWS/GCP/Azure that depending on them would reintroduce
  the exact portability problem this is trying to solve).

hyve already has the equivalent of Rancher/Teleport's one mechanism — it's
`AgentProvider`. Confirmed by reading it: `buildKubeconfig(server, nil,
token)` — no CA pinning at all, just hyve's own session token over
whatever TLS the public endpoint happens to have. It is already the
cloud-agnostic path; `HostProvider` is the odd one out.

## Decisions already made, with the reasoning

**No Ingress in the helm chart, at all — not even for local dev, not even
disabled-by-default.** The chart previously shipped
`api-ingress.yaml`/`ui-ingress.yaml`, disabled by default
(`api.ingress.enabled: false`) but hard-coded to Traefik-specific
`ingressClassName` semantics and local-dev nip.io hostnames the moment
anyone turned it on. Keeping a bundled Ingress "just in case," even
off by default, invites exactly the failure mode this whole investigation
started from: someone enables it, gets a working-looking Ingress object,
and only discovers the TLS/portability gaps live. Removed outright.
`scripts/install-local.sh` now applies its own Ingress directly via
`kubectl apply`, entirely outside the helm release (`helm
upgrade`/`uninstall` never touches it) — this is the right home for
something that is deliberately Traefik-specific, nip.io-specific, and
k3d-specific, none of which a real chart consumer should ever see.

**Real installs bring their own exposure — documented, not scripted.**
`values.yaml`, `values-tenant-example.yaml`, and `README.md`'s
multi-tenant section were updated to stop mentioning `api.ingress.*`
entirely and instead point at the `hyve-api`/`hyve-ui` `Service`s the
chart does create, which any exposure mechanism (a cloud LoadBalancer, an
existing Ingress controller, a service mesh gateway) can already target
without any chart changes.

**TLS/certificate sourcing is the deployer's problem, documented not
depended on.** The chart should never render a `cert-manager` `Certificate`
CRD unconditionally — that hard-fails on any cluster without cert-manager
installed, and plenty of real EKS/GKE/AKS clusters won't have it. Mirror
Rancher's own `ingress.tls.source` pattern instead: support a plain
existing-Secret reference (already what `scripts/install-local.sh`'s
`HYVE_INSTALL_TLS_SECRET` gives locally) as the one thing the chart
understands, and *document* a cert-manager `ClusterIssuer`+`Certificate`
snippet for anyone who has it, rather than baking in a dependency.

## Retiring `HostProvider`'s `/proxy` path as the default

**The host cluster registers hyve-agent and goes through `AgentProvider`'s
`/api/agent-proxy/host` path, exactly like every other managed cluster,
once an operator sets `spec.access.agent.enabled/proxy: true` on it.**
Concretely:

- `access.method: primary` (`HostProvider`, `internal/api/access.go`)
  stops being the recommended way to reach the cluster hyve's own control
  plane runs on. It's kept as a documented, explicitly-opt-in fallback for
  operators who deliberately don't want to run an agent on their own
  control-plane cluster and are on a self-managed cluster where borrowing
  the apiserver CA is at least *possible* — not the default path, and not
  something that can ever be made to work on EKS/GKE/AKS no matter how
  it's configured.
- Running hyve-agent on the cluster hyve-api/hyve-controller themselves
  run on is *less* setup than `HostProvider`'s original zero-driver-module
  convenience, not more — the whole point of the agent is exactly zero
  driver-module/credential setup per cluster. There's no real friction
  argument against this that `access.method: primary` was originally
  solving for (see `docs/HYVE-AGENT-MIGRATION-GUIDE.md`'s "Host cluster
  access" section for that history) that survives once the agent exists.
- This removes the CA-pinning requirement entirely for host-cluster
  access — `AgentProvider` has none — which is what actually makes this
  work uniformly on EKS, GKE, and AKS, not any change to how TLS is
  terminated in front of hyve-api.

## Resolved decisions

**`access.method: primary` is kept, not removed**, mirroring the decision
already made for `access.method: tunnel` when `AccessMethod` itself was
retired (`HYVE-AGENT-MIGRATION-GUIDE.md`: "a normal, supported access
path... it never needed hyve-agent as a replacement and isn't going
anywhere"). There's a real, narrow audience — an operator on a small
self-managed cluster who'd rather not add hyve-agent's Deployment/
ServiceAccount/RBAC footprint to their own control-plane node — and the
mechanism still works there. The only change is which path is
recommended/defaulted toward, not which paths exist.

**No chart support for cert-manager, not even opt-in — documentation
only.** A `Certificate` resource needs an `issuerRef` naming a specific
`ClusterIssuer` (ACME/HTTP-01, ACME/DNS-01 with a specific DNS provider's
credentials, a private CA, a cloud-specific issuer), and which shape a
given deployer has varies enough that "supporting" it means exposing a
pile of mostly-dead values per install. That's the same shape of problem
that produced the original bug (a chart-owned Ingress carrying Traefik-
specific, locally-scoped assumptions that only surfaced as broken once
enabled). An opt-in flag is still a chart-owned exposure decision, just a
quieter one — stays out entirely.

**No automatic TLS-cert-minting added to `scripts/install-local.sh`.**
Building convenience automation around `HostProvider`'s CA requirement
would invest in the path this proposal just demoted. `AgentProvider`
doesn't pin CA at all, so once local dev also exercises the agent-based
host path, testing it needs only *some* real, system- (or
`mkcert`-)trusted TLS — a much lower bar than "signed by this exact
cluster's own apiserver CA." `mkcert` is the right building block if that
friction turns out to matter later, not the cluster-PKI trick used to
unblock testing this session (kept working, documented, for anyone still
using `HostProvider` directly).

## Implementation notes

- `deploy/helm/hyve/templates/api-ingress.yaml`/`ui-ingress.yaml` deleted;
  `api.ingress.*`/`ui.ingress.*` values removed. `scripts/
  install-local.sh` now applies an equivalent Ingress directly via
  `kubectl apply` (`apply_ingress`), entirely outside the helm release.
  Verified live: `helm lint` passes, `helm template` renders zero
  `Ingress` objects, and the running k3d-hyve-local install kept working
  end to end after the cutover (`hyve cluster auth host` +
  `kubectl --context host get nodes` + the UI all still reachable).
- `internal/reconcile/host.go`'s `reconcileHostCluster` (the dispatch
  target for a driver-less `primary`-marked cluster) now calls
  `reconcileAgent` using the same in-cluster kubeconfig it already mints
  for `spec.resources`. Before this change, a driver-less host cluster
  never reached `reconcileAgent` at all — it has its own dispatch branch
  in `ReconcileOne`, separate from `reconcileCluster` (the only other call
  site) — so `spec.access.agent` silently had no effect on it regardless
  of what it was set to. `internal/api/kubeconfig_handler.go`'s provider
  dispatch already checked `Agent.Proxy` ahead of `Method` before this
  change, so no API-side change was needed once the reconciler actually
  installs the agent.
- New test: `TestReconcileHostCluster_AgentEnabled_NoAgentConfig_SoftNoOp`
  (`internal/reconcile/host_test.go`) confirms `reconcileHostCluster` now
  reaches `reconcileAgent`'s own "not configured" branch rather than
  skipping it entirely. `go build ./...` and `go test
  ./internal/reconcile/...` both pass.
- **Not verified live: an actual hyve-agent installation and working
  `AgentProvider`-served kubeconfig for the host cluster.** The running
  k3d-hyve-local install has no `controller.agent.controlPlaneURL`/
  `tunnelAddress` configured (confirmed via `helm get values`), and
  standing those up needs real external reachability this local dev
  environment doesn't have (see `HYVE-AGENT-MIGRATION-GUIDE.md`'s own
  point 1 on this exact requirement) — the same gap that doc already
  flags for testing the agent path generally, not something new this
  change introduced. The code path is confirmed reached and exercised by
  the new test; the full connect-and-proxy behavior needs a real
  externally-reachable control plane to verify, same as any other
  hyve-agent installation.
