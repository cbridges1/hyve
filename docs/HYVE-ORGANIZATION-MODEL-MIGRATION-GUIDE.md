# Migrating onto the organization model

For anyone running a hyve cluster-mode install that predates
`HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md` (nexus-config/docs) — a
real install this old is unlikely by now, since every milestone below
landed and this session's own dev/test installs were dogfooded through
each transition live, but if you have one, this is the concrete,
operator-facing counterpart to that plan's own "Migration note" for each
milestone. See `HYVE-ORGANIZATION-MODEL-PROPOSAL.md` (nexus-config/docs)
for *why* each of these moves happened; this guide only covers *what to do
about it* if you're still on the old shape.

**The short version**: every milestone below retired a Kubernetes CRD
outright (no coexisting fallback, no in-place conversion) in favor of a
row in hyve-api's own Postgres/SQLite datastore (`internal/orgdb`). None of
these migrations preserve data automatically — each is an explicit,
accepted breaking change, the same precedent this whole plan followed
throughout. **Do the export steps below before upgrading past the
milestone that removes the CRD in question** — once it's gone, so is
every object that lived in it.

## 1. `HyveEnvironment` → `Organization` (Milestone 2)

**Before**: one `HyveEnvironment` custom resource per tenant, living in
`hyve-system`, `spec.namespace` naming the tenant's own namespace.

**Export what you have**, before upgrading past Milestone 2:

```bash
kubectl get hyveenvironments -n hyve-system -o yaml > hyveenvironments-backup.yaml
```

**After upgrading**, recreate each one as a real Organization — same
namespace-per-tenant shape, now a database row instead of a CRD:

```bash
# Once per tenant, reading spec.namespace from your backup above:
hyve organization create <tenant-name>
# — or, equivalently, against the API directly:
curl -X POST $API_URL/api/organizations -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"<tenant-name>"}'
```

This provisions the same Kubernetes Namespace + RBAC scaffolding
`HyveEnvironment`'s own controller logic used to (`ensureNamespace`/
`ensureAccessRoleScaffolding`), just through `POST /organizations`'s one
atomic transaction instead of three separate, non-atomic Kubernetes calls.
The underlying `Namespace` object itself was always real Kubernetes
infrastructure, never CRD-wrapped — if it still exists, `hyve organization
create` reconciles onto it rather than failing on an already-exists
conflict.

## 2. `HyveAccessBinding` → `Binding` (Milestone 4)

**Before**: one `HyveAccessBinding` custom resource per identity+role
grant — namespaced as of `HYVE-MULTI-TENANCY-PLAN.md`'s own earlier Phase
1 work, `spec.subject`/`spec.role`/`spec.serviceAccountRef` fields.

**Export what you have**, before upgrading past Milestone 4:

```bash
kubectl get hyveaccessbindings -A -o yaml > hyveaccessbindings-backup.yaml
```

**After upgrading**, recreate each local-subject binding via
`create-user` (the paired credentials Secret this command used to also
emit is itself retired as of Milestone 10 Part C — see item 4 below; a
fresh install past that point never needs `kubectl apply` for this at
all):

```bash
kubectl exec -n hyve-system deployment/hyve-api -- \
  hyve cluster-config api create-user <username> --role <role> --namespace <tenant-ns>
# prompts for a new password interactively — every account needs one
# chosen fresh, there is no way to recover or migrate the old bcrypt hash
# out of a deleted Secret
```

An OIDC-subject binding (`spec.subject.type: oidc`) has no password to
re-enter, but still needs recreating — there is no bulk-import tool for
this; each identity's grant is re-established one at a time via `POST
/accounts` (or the equivalent direct `orgdb` write, for an operator
scripting many accounts at once).

## 3. Local account credentials Secrets and reconciling-cluster kubeconfig
   Secrets (Milestone 10 Part C)

**Before**: every local account had a paired `<identity>-credentials`
Kubernetes Secret holding its bcrypt password hash; every registered
reconciling cluster (Milestone 6) had its kubeconfig content stored in a
`reconciling-cluster-<name>-kubeconfig` Secret on the control plane's own
home cluster.

**Both are now columns in `orgdb`** (`bindings.password_hash`,
`reconciling_clusters.kubeconfig`) — see
`HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md`'s Milestone 10 Part C for
the full reasoning (this is what makes hyve-api deployable with zero
Kubernetes access of its own at all).

Neither migrates automatically — `internal/migrate.AccessBindings` (the
one remaining Kubernetes-native piece of the old design, used by `hyve
migrate cluster`) still copies a *legacy* `*-credentials` Secret verbatim
if one still exists, as a bridge for an account created before this
change; but for accounts/reconciling clusters registered fresh against a
post-Milestone-10 install, there is nothing to export — they were never
stored as a Secret in the first place. If you have pre-Milestone-10
accounts you haven't yet touched, their credentials Secret keeps working
exactly as before until you next reset that account's password (which
now writes straight to `bindings.password_hash`, no Secret involved) —
this is an entirely lazy, non-breaking transition, not something you need
to act on immediately the way items 1/2 above are.

A reconciling cluster registered before Milestone 10 Part C needs
re-registering once, to move its kubeconfig from the old Secret into the
new `orgdb` column:

```bash
kubectl get secret reconciling-cluster-<name>-kubeconfig -n hyve-system \
  -o jsonpath='{.data.kubeconfig}' | base64 -d > kubeconfig.yaml
hyve reconciling-cluster create <name> --kubeconfig-file kubeconfig.yaml
# idempotent by name — this rotates the stored content, doesn't duplicate
kubectl delete secret reconciling-cluster-<name>-kubeconfig -n hyve-system
```

## 4. `HyveSession` → `Session` (Milestone 10 Part D)

**Before**: one `HyveSession` custom resource per active `hyve env login`,
`spec.tokenHash`/`spec.expiresAt`, listed with `kubectl get hyvesessions`.

**Nothing to export.** A session is short-lived, disposable
authentication state, not durable data — every currently-logged-in
session simply stopped working the moment the `HyveSession` CRD was
deleted (the same explicit, accepted breaking change as items 1/2 above,
just with lower stakes: `hyve env login` again is the entire recovery
path, no data was lost). If you're upgrading past this milestone, expect
every teammate/CI job using this install to need to re-authenticate once.

## Summary table

| Old (Kubernetes CRD/Secret) | New (`internal/orgdb` row) | Migrated automatically? |
|---|---|---|
| `HyveEnvironment` | `Organization` | No — recreate via `hyve organization create` |
| `HyveAccessBinding` | `Binding` | No — recreate via `create-user`/`POST /accounts` |
| `<identity>-credentials` Secret | `bindings.password_hash` | Lazily, on next password reset — no immediate action needed |
| `reconciling-cluster-<name>-kubeconfig` Secret | `reconciling_clusters.kubeconfig` | No — re-register via `hyve reconciling-cluster create` |
| `HyveSession` | `Session` | No — re-`hyve env login` (nothing to lose, sessions are disposable) |
