-- Postgres counterpart of migrations/sqlite/0001_init.sql — see that
-- file's own header comment for why both exist rather than one portable
-- file, and HYVE-ORGANIZATION-MODEL-PROPOSAL.md (nexus-config/docs) for
-- the design. Keep these two files in schema-equivalent lockstep; the
-- only intended differences are BOOLEAN vs INTEGER and TIMESTAMPTZ vs
-- TIMESTAMP, both handled transparently by database/sql's Go type
-- scanning either way. Table order matters here — Postgres rejects a
-- forward reference to a not-yet-existing table.

CREATE TABLE IF NOT EXISTS reconciling_clusters (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kubeconfig_secret_namespace TEXT NOT NULL,
    kubeconfig_secret_name TEXT NOT NULL,
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
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS bindings_namespace_env_identity
    ON bindings (namespace, COALESCE(environment_id, ''), subject_type, identity);
