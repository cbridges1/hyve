-- See migrations/sqlite/0006_reconciling_cluster_ownership.sql's own
-- comment for the full reasoning. Postgres can drop the inline UNIQUE
-- constraint directly, so no table rebuild is needed here.
ALTER TABLE reconciling_clusters ADD COLUMN organization_id TEXT REFERENCES organizations(id);

UPDATE reconciling_clusters SET organization_id = o.id
FROM organizations o WHERE o.namespace = reconciling_clusters.name;

ALTER TABLE reconciling_clusters DROP CONSTRAINT IF EXISTS reconciling_clusters_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS reconciling_clusters_pool_name
    ON reconciling_clusters (name) WHERE organization_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS reconciling_clusters_org_name
    ON reconciling_clusters (organization_id, name) WHERE organization_id IS NOT NULL;
