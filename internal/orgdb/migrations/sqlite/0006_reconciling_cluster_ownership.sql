-- Reconciling clusters gain an owner. organization_id NULL is the
-- superadmin-managed pool (POST /reconciling-clusters), exactly what every
-- row used to be; set, the row belongs to that one organization, which can
-- keep several registered and switch between them by name rather than
-- re-entering a kubeconfig each time.
--
-- name is no longer globally unique: two organizations can each call their
-- own cluster "prod". Uniqueness is per owner instead — see the two partial
-- indexes below (NULLs compare distinct in a plain composite UNIQUE, so the
-- pool needs its own index).
--
-- SQLite can't drop the old inline UNIQUE in place, so this rebuilds the
-- table (the documented create/copy/drop/rename sequence — migrate runs it
-- with foreign_keys off, then checks them before commit). Rows named
-- identically to an organization's namespace were that organization's own
-- dedicated cluster under the previous one-per-organization convention
-- (handlePutOrgReconcilingCluster), so they're backfilled as owned by it.
CREATE TABLE reconciling_clusters_new (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    organization_id TEXT REFERENCES organizations(id),
    kubeconfig TEXT NOT NULL,
    reachable INTEGER,
    last_checked_at TIMESTAMP,
    last_error TEXT,
    kubernetes_version TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO reconciling_clusters_new
    (id, name, organization_id, kubeconfig, reachable, last_checked_at, last_error, kubernetes_version, created_at)
SELECT rc.id, rc.name,
       (SELECT o.id FROM organizations o WHERE o.namespace = rc.name),
       rc.kubeconfig, rc.reachable, rc.last_checked_at, rc.last_error, rc.kubernetes_version, rc.created_at
FROM reconciling_clusters rc;

DROP TABLE reconciling_clusters;

ALTER TABLE reconciling_clusters_new RENAME TO reconciling_clusters;

CREATE UNIQUE INDEX IF NOT EXISTS reconciling_clusters_pool_name
    ON reconciling_clusters (name) WHERE organization_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS reconciling_clusters_org_name
    ON reconciling_clusters (organization_id, name) WHERE organization_id IS NOT NULL;
