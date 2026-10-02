-- Postgres counterpart of migrations/sqlite/0001_init.sql — see that
-- file's own header comment for why both exist rather than one portable
-- file, and HYVE-ORGANIZATION-MODEL-PROPOSAL.md (nexus-config/docs) for
-- the design. Keep these two files in schema-equivalent lockstep; the
-- only intended differences are BOOLEAN vs INTEGER and TIMESTAMPTZ vs
-- TIMESTAMP, both handled transparently by database/sql's Go type
-- scanning either way. Table order matters here — Postgres rejects a
-- forward reference to a not-yet-existing table.

-- kubeconfig holds the registered cluster's kubeconfig content directly —
-- see migrations/sqlite/0001_init.sql's own reconciling_clusters comment
-- for the full Milestone 10 Part C reasoning (a deliberate, flagged
-- plaintext-at-rest tradeoff, not yet mitigated by encryption).
CREATE TABLE IF NOT EXISTS reconciling_clusters (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kubeconfig TEXT NOT NULL,
    reachable BOOLEAN,
    last_checked_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS organizations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    namespace TEXT NOT NULL UNIQUE,
    plan TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    reconciling_cluster_id TEXT REFERENCES reconciling_clusters(id),
    reconciling_cluster_migration_status TEXT,
    pending_deletion BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS environments (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

-- See migrations/sqlite/0001_init.sql's own bindings comment for the full
-- reasoning: namespace is the actual isolation boundary (always set,
-- mirrors the retired CRD's own namespace scoping); organization_id/
-- environment_id are optional metadata for environment resolution only,
-- nil together whenever namespace has no matching Organization (a
-- self-hosted single-tenant install, or a superadmin binding).
-- password_hash — see migrations/sqlite/0001_init.sql's own bindings
-- comment for the full Milestone 10 Part C reasoning.
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
    password_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS bindings_namespace_env_identity
    ON bindings (namespace, COALESCE(environment_id, ''), subject_type, identity);

-- signing_keys — see migrations/sqlite/0001_init.sql's own comment.
CREATE TABLE IF NOT EXISTS signing_keys (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL UNIQUE,
    key_material TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- sessions (Milestone 10 Part D) — see migrations/sqlite/0001_init.sql's
-- own comment for the full design this preserves from the retired
-- HyveSession CRD.
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    tenant_namespace TEXT NOT NULL DEFAULT '',
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
