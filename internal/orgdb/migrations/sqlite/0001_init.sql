-- Initial schema for organizations, environments, RBAC bindings, and
-- reconciling clusters — see HYVE-ORGANIZATION-MODEL-PROPOSAL.md
-- (nexus-config/docs) for the design this implements.
--
-- Primary keys are app-generated TEXT (UUIDs), not an autoincrement
-- integer, specifically so this schema is byte-for-byte identical between
-- the SQLite and Postgres migration files aside from the few genuinely
-- dialect-specific bits (BOOLEAN/TIMESTAMP typing) — SQLite's dynamic
-- typing accepts these declarations without enforcing them, so the same
-- application-level Go types work unmodified against either backend.
--
-- reconciling_clusters is created first, ahead of organizations, purely
-- because organizations.reconciling_cluster_id references it — Postgres
-- (unlike SQLite) rejects a forward reference to a not-yet-existing
-- table, so table order here matters for real, not just cosmetically.

CREATE TABLE IF NOT EXISTS reconciling_clusters (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kubeconfig_secret_namespace TEXT NOT NULL,
    kubeconfig_secret_name TEXT NOT NULL,
    -- NULL = never checked yet, not "unreachable" — distinct states.
    reachable INTEGER,
    last_checked_at TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS organizations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    namespace TEXT NOT NULL UNIQUE,
    plan TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    -- NULL means "this control plane's own home cluster" — see
    -- HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization reconciling
    -- cluster" section. Not a foreign key constraint in this file (SQLite
    -- enforces FKs only when PRAGMA foreign_keys=ON is set per-connection,
    -- which internal/orgdb.Open does) but is one logically, and is one for
    -- real in the Postgres migration.
    reconciling_cluster_id TEXT REFERENCES reconciling_clusters(id),
    -- NULL = not migrating. 'migrating' = every request against this org
    -- returns 423 Locked until the copy is confirmed complete and this
    -- flips back to NULL alongside reconciling_cluster_id's own update.
    reconciling_cluster_migration_status TEXT,
    pending_deletion INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS environments (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (organization_id, name)
);

-- namespace is the actual isolation boundary — always set, and the primary
-- key every lookup (FindBindingBySubject/ListBindingsForScope) filters by,
-- exactly mirroring the retired CRD-based HyveAccessBinding's own
-- namespace scoping (client.InNamespace(...)), so isolation between two
-- namespaces never depends on whether either one has a registered
-- Organization. organization_id/environment_id are optional metadata used
-- only for the environment-resolution feature (which Environment an
-- unscoped grant defaults to — see "What moves to Postgres" in the
-- proposal doc's "no org-wide/wildcard grant" decision) — set together
-- when namespace has a matching Organization, both nil otherwise (a
-- self-hosted single-tenant install that never ran POST /organizations,
-- or a superadmin binding, which has no Organization by definition). This
-- was revised from an earlier draft that scoped bindings by
-- organization_id alone and nil'd both fields for any namespace lacking
-- an Organization — that collapsed every such namespace into one shared
-- scope, a real cross-tenant leak between two *different* org-less
-- namespaces; confirmed by a failing regression test before this fix.
--
-- service_account_name/namespace (the latter, confusingly, the
-- ServiceAccount's *own* namespace, not this binding's namespace column
-- above — usually the same value, but named separately since they answer
-- different questions) preserve HyveAccessBindingSpec's own
-- ServiceAccountRef — a role -> ServiceAccount convention (admin ->
-- hyve-access-admin, read-only -> hyve-access-readonly, custom ->
-- operator-defined) kept for the same reason its CRD predecessor's doc
-- comment gave: a still-plausible convention independent of which
-- mechanism (if any) currently reads it back.
CREATE TABLE IF NOT EXISTS bindings (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    organization_id TEXT REFERENCES organizations(id),
    environment_id TEXT REFERENCES environments(id),
    subject_type TEXT NOT NULL DEFAULT 'local',
    identity TEXT NOT NULL,
    role TEXT NOT NULL,
    service_account_name TEXT NOT NULL,
    service_account_namespace TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- COALESCE(environment_id, '') rather than a plain column list: a bare
-- UNIQUE(namespace, environment_id, ...) would let two bindings for the
-- same (namespace, identity) both with NULL environment_id (the
-- no-organization / superadmin case) coexist, since SQL treats NULL as
-- distinct from itself — exactly the duplicate this index exists to
-- reject. The expression form closes that gap in one index instead of a
-- second application-level check.
CREATE UNIQUE INDEX IF NOT EXISTS bindings_namespace_env_identity
    ON bindings (namespace, COALESCE(environment_id, ''), subject_type, identity);
