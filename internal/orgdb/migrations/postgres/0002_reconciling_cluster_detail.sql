-- See migrations/sqlite/0002_reconciling_cluster_detail.sql's own comment
-- for the full reasoning.
ALTER TABLE reconciling_clusters ADD COLUMN kubernetes_version TEXT;
