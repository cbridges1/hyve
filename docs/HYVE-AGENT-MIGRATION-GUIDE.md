# Migrating off `AccessMethod`/`primary`'s old minting onto hyve-agent

**Status, as of `docs/HYVE-AGENT-IMPLEMENTATION-PLAN.md`'s milestone 9:**
the `AccessMethod` CRD (`spec.access.accessMethodRef`) has been removed
outright — it no longer exists, in code or in the cluster's schema.
`access.method: primary`'s old hardcoded minting mechanism
(`PrimaryClusterProvider`) has also been removed, though the `primary`
*value* itself stays, now as a pure identifying marker (see this guide's
own "Host cluster access" section below). **`access.method: tunnel` was
NOT removed** — an explicit decision kept it as a normal, supported access
path (an admin who wants tunnel access specifies it directly on the
`ClusterDefinition`, same as always); it never needed hyve-agent as a
replacement and isn't going anywhere.

This guide is for anyone who still has a `ClusterDefinition` referencing
the now-removed `AccessMethod` CRD (`spec.access.accessMethodRef`/
`accessMethodClusterID` — these fields are gone from the schema, so
applying such a `ClusterDefinition` now silently drops them) and needs to
migrate it onto hyve-agent instead — the steps below were written before
the removal happened and are the correct, reasoned-through procedure, but
were not proven against a live case before milestone 9's removal landed
(the one concrete case this plan was originally written against,
`acme-worker`, had already expired independently — see this repo's own
memory of that). Treat them as a starting point, not a guarantee.

## Migrating an `AccessMethod`-mediated cluster

1. **Confirm your control plane can actually run an agent tunnel at all.**
   `spec.access.agent` only does anything if hyve-controller was started
   with `--agent-control-plane-url`/`--agent-tunnel-address` pointing at a
   real, externally-reachable address the managed cluster can dial out
   to — see `deploy/helm/hyve/values.yaml`'s `controller.agent.*` block.
   Unlike everything else in this project's own local-dev testing so far,
   this genuinely needs real external reachability (a public HTTPS
   endpoint for the bootstrap call, a reachable raw-TCP endpoint for the
   SSH tunnel itself) — a plain `http://…nip.io` local dev Ingress, with no
   TLS termination, is not enough on its own (confirmed live during this
   plan's own milestone 5: `kubectl`/client-go refuse to attach any
   credential to a request over a bare `http://` URL). If you don't have
   this yet, stop here — enabling `spec.access.agent` without it just logs
   a warning and does nothing (see `internal/reconcile/agent.go`'s own
   soft-fail behavior), which is safe, but also means there's nothing to
   verify yet.

2. **Enable the agent on the cluster, without touching its existing access
   path yet:**

   ```yaml
   spec:
     access:
       accessMethodRef: corp-rancher # still present, pre-removal — leave as-is for now
       accessMethodClusterID: c-abc123
       agent:
         enabled: true
         proxy: true
   ```

   The next reconcile installs hyve-agent onto the cluster (a `Deployment`,
   `ServiceAccount`, the `impersonate`-only `ClusterRole`, and — because
   `proxy: true` — the two `ClusterRoleBinding`s mapping hyve's `admin`/
   `read-only` roles onto `cluster-admin`/`view` there). Your existing
   `AccessMethod` path keeps working unchanged throughout this whole
   process — nothing about enabling the agent touches it.

3. **Confirm it actually connects and proxies correctly, using the real
   thing, not just a status field:**

   ```sh
   kubectl get clusterdefinition <name> -o jsonpath='{.status.agent}'
   # {"connected":true,"lastConnectedAt":"...","version":"..."}

   hyve cluster auth <name>
   # "kubectl context for '<name>' configured (via hyve-agent's proxy
   #  tunnel — kubectl traffic is relayed through the API to this
   #  cluster's own agent, not a direct connection)"

   kubectl get pods -A
   kubectl exec -it <some-pod> -- sh   # confirm the streaming/upgrade
                                        # path works, not just plain GETs —
                                        # this is the whole reason a raw
                                        # connection-level tunnel was
                                        # chosen over anything HTTP-level
                                        # (see the architecture proposal's
                                        # own "Tunnel protocol" resolution)
   ```

   Do this with both an `admin`-role and a `read-only`-role hyve account if
   you can — confirm read-only genuinely can't write (a `kubectl apply`
   should come back `Forbidden`, naming the impersonated identity), not
   just that it can read.

4. **Only once step 3 is genuinely confirmed working**, clear the old
   access configuration:

   ```yaml
   spec:
     access:
       agent:
         enabled: true
         proxy: true
       # accessMethodRef / accessMethodClusterID / method: removed
   ```

   If the cluster's `Template` (or its own `spec.workflows`) still wires up
   whatever put it on the old path in the first place — e.g. an
   `afterCreate`/`onDelete` hook pair like `register-with-rancher`/
   `deregister-from-rancher` for a Rancher-backed `AccessMethod` — remove
   those too, and stop running whatever external service (Rancher,
   Teleport, ...) was solving connectivity on the cluster's behalf, if
   nothing else still needs it for an unrelated reason. See
   `template-civo-agent.yaml` (nexus-config) for what the equivalent
   agent-only template looks like end to end, alongside
   `template-civo-rancher-agent.yaml`'s own still-live reference shape for
   comparison.

5. **Confirm the old path is actually gone, not just unused:** re-run step
   3's own verification once more after step 4, to make sure nothing
   quietly regressed now that the fallback is gone.

## Host cluster access (`access.method: primary`) — resolved in milestone 9

**Naming convention:** the host cluster's self-registered `ClusterDefinition`
is now conventionally named `host` (previously suggested as `local` —
renamed because "local" already means something else entirely in this
project's own vocabulary: local/file mode, no live cluster at all. The
host cluster is the opposite of that — a very real, live cluster, the one
hyve-controller/hyve-api themselves run on). Nothing in the code requires
this exact name; it's purely a convention `scripts/install-local.sh`'s own
suggested snippet uses.

`access.method: primary`'s original hardcoded minting mechanism
(`PrimaryClusterProvider`, gated to `RoleSuperadmin`, minting a token
against a caller's resolved `ServiceAccountRef` or a dedicated host
ServiceAccount) was removed once in this milestone, then **restored in a
scoped-down form** after a real regression was found live: an earlier
design for this milestone required an admin to hand-write a driver module
just to get a kubeconfig for the cluster hyve is already running on —
reconciling `k3d-hyve-local`'s own host `ClusterDefinition` after that
change produced a real, continuous `"no driver specified"` reconcile
error, since it had `spec.driver: {}` and nothing to fill it with. That
was an acknowledged oversight, corrected the same day: the host cluster
needs no module at all for its own lifecycle (auth/create/delete).

**The resolved design:** `internal/api.HostProvider` (the restored,
scoped-down `PrimaryClusterProvider`) mints a short-lived token against a
dedicated `hyve-host-admin` `ServiceAccount` (bound to the built-in
`cluster-admin` `ClusterRole` — see
`deploy/helm/hyve/templates/api-access-roles.yaml`) via `TokenRequest`,
gated to `RoleSuperadmin` only, and returns a kubeconfig whose `server:`
points at hyve-api's own `/proxy` path — reachable because hyve-api
already runs inside the host cluster itself, no tunnel of any kind
needed (unlike `access.method: tunnel` or hyve-agent's own SSH tunnel,
both of which solve a genuinely different problem: reaching a cluster
hyve-api has *no* direct network path to at all). `hyve-controller` mints
its own token the same way, directly against
`https://kubernetes.default.svc` (no `/proxy` hop needed — it's already
inside the cluster too), to reconcile `spec.resources` on the host
cluster with no module involved (see `internal/reconcile/host.go`). Ad
hoc `hyve workflow run --cluster host` needs nothing extra either — it
only depends on `hyve cluster auth host` having produced a working
kubeconfig, which it now does automatically.

This all applies only to a `primary`-marked `ClusterDefinition` with **no
real `spec.driver`** — the automatic, zero-config case. An admin who sets
a real `spec.driver` on a `primary`-marked cluster deliberately opts out
of this automatic path: it's then reconciled exactly like any other
driver-having cluster (client-side auth via
`GET /clusters/<name>/auth-context`, the same as the default case), and
`access.method: primary` stays set purely as `hyve migrate cluster`'s own
host-identification marker (`cmd/migrate_resolve.go`'s
`resolveCurrentHostKubeconfigPath`) — that convention is unchanged either
way.

**Superseded as the *recommended* path (not removed) — see
`docs/HYVE-CLOUD-EXPOSURE-PROPOSAL.md`.** `HostProvider`'s `/proxy` path
pins trust to the host cluster's own apiserver CA, which works fine here
(hyve-api runs inside the cluster it's minting credentials for) but is a
hard ceiling on EKS/GKE/AKS: none of the three major managed control
planes expose that CA's private key to anyone, at all — no configuration
fixes this. The recommended way to reach the host cluster is now to
enable hyve-agent on it too, exactly like any other managed cluster:

```yaml
spec:
  access:
    method: primary
    agent:
      enabled: true
      proxy: true
```

`internal/reconcile/host.go`'s driver-less dispatch path now calls
`reconcileAgent` using the same in-cluster kubeconfig it already mints for
`spec.resources` (previously it didn't — a driver-less host cluster's own
dispatch branch bypassed `reconcileAgent` entirely, so this had silently
no effect before). Once installed, `GET /kubeconfig`'s own provider
dispatch (`internal/api/kubeconfig_handler.go`) already checks
`Agent.Proxy` ahead of `Method`, so `hyve cluster auth host` automatically
starts going through `AgentProvider` instead of `HostProvider` — no other
config change needed. `access.method: primary` with no agent enabled is
kept working as documented above, for anyone deliberately staying off the
agent on a self-managed cluster where borrowing the CA is at least
possible — it's the fallback now, not the default recommendation.
