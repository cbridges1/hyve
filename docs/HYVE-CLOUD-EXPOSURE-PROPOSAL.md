# Proposal: cloud-portable exposure and host-cluster access

**Status:** partially implemented. The chart/ingress cleanup below is
done; retiring `HostProvider` in favor of routing the host cluster through
hyve-agent is still a proposal, not implemented. Written to capture a
design direction worked out in conversation, prompted by a live failure
found while debugging `hyve cluster auth host` against a local k3d
cluster with no TLS in front of it.

## Summary

Two related decisions:

1. **hyve's helm chart owns no Ingress and makes no exposure decision at
   all.** `deploy/helm/hyve/templates/api-ingress.yaml`/`ui-ingress.yaml`
   are deleted; the chart creates only `Service`s (`hyve-api`, `hyve-ui`)
   for a deployer to point their own Ingress/LoadBalancer/routing at.
   Local dev's Ingress (Traefik-specific, nip.io-hostname, k3d-only) moved
   into `scripts/install-local.sh` as a plain `kubectl apply`, entirely
   outside the helm release.
2. **The host cluster (`access.method: primary`, `HostProvider`) should
   stop being a special case and instead go through hyve-agent like every
   other cluster** — proposed, not yet built. `HostProvider`'s `/proxy`
   path pins trust to the in-cluster Kubernetes API server's own CA, which
   is fundamentally incompatible with EKS, GKE, and AKS: none of them
   expose their control plane's CA private key to anyone, ever. There is
   no configuration that fixes this — it's a hard architectural ceiling on
   `HostProvider`, not a deployment detail to work around.

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

## Proposed, not yet implemented: retire `HostProvider`'s `/proxy` path

**The host cluster should register hyve-agent and go through
`AgentProvider`'s `/api/agent-proxy/host` path, exactly like every other
managed cluster.** Concretely:

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

## Open questions

- Does `access.method: primary` get removed outright once the agent path
  covers the host-cluster case, or kept indefinitely as a documented
  fallback for self-managed clusters that don't want an agent running on
  their own control-plane node? (Mirrors the same question already
  resolved for `AccessMethod` more broadly in
  `HYVE-AGENT-ARCHITECTURE-PROPOSAL.md` — likely the same answer: keep it,
  clearly marked as the exception, not the default.)
- Should the chart optionally support rendering a cert-manager
  `Certificate` when a value explicitly opts in (e.g.
  `api.tls.certManager.enabled: true`), gated so it only ever renders when
  requested, or should that stay pure documentation with zero chart
  support at all? Leaning toward pure documentation, given the "the chart
  makes no exposure decision for you" stance adopted above — an opt-in
  flag is still a chart-owned exposure decision, just a quieter one.
- Should `scripts/install-local.sh`'s own Ingress gain an option to mint
  its TLS cert from the local cluster's own CA automatically (formalizing
  what was done manually this session), or does that become unnecessary
  once local dev also exercises the agent-based host-cluster path instead
  of `HostProvider`, at which point any cert (even a plain self-signed one
  with `insecure-skip-tls-verify`-equivalent handling) would do? Leaning
  toward the latter — not worth automating a workaround for a path being
  deprecated.
