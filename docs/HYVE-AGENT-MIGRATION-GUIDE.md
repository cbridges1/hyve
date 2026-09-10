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

`access.method: primary`'s hardcoded minting mechanism
(`PrimaryClusterProvider`, its superadmin-only gate, and its ad hoc
`ServiceAccountRef`-based `TokenRequest`/`/proxy` path) has been removed
entirely — not deferred, not replaced by hyve-agent, but deleted. The
resolved design (per an explicit decision on this project, "host cluster
should default to the auth method defined in the module") is simpler than
either the old mechanism or a full agent-symmetric model: `primary` is now
a pure identifying marker, and a `primary`-marked `ClusterDefinition` is
authenticated exactly like any other cluster, through its own real
`spec.driver`'s `auth` operation.

**What this means for the cluster conventionally named `local`:** it now
needs a real `spec.driver` (and a matching `auth.yaml` in that driver
module) for `hyve cluster auth local` / `hyve migrate cluster` to actually
mint anything. Before this milestone, `local` had `spec.driver: {}` and
relied entirely on the special case this milestone removed — reconciling
it today (confirmed live, `k3d-hyve-local`) produces a real, continuous
`"no driver specified"` reconcile error until an admin assigns it one.
This is expected, not a bug: give the host cluster's `ClusterDefinition` a
driver module whose `auth.yaml` produces a kubeconfig for the cluster
hyve is already running on — for an in-cluster case, one that mints a
token against `hyve-access-admin`/`hyve-access-readonly`
(`deploy/helm/hyve/templates/api-access-roles.yaml`) and points `server:`
at this API's own `/proxy` path (still live, generic infrastructure — see
`internal/api/proxy.go`) is the direct equivalent of what
`PrimaryClusterProvider` used to do automatically, just expressed as an
ordinary module instead of hardcoded Go.

`access.method: primary`'s only remaining consumer is
`hyve migrate cluster`'s host-resolution (`cmd/migrate_resolve.go`'s
`resolveCurrentHostKubeconfigPath`), which still looks for exactly one
`ClusterDefinition` with this marker set — that convention is unchanged.
