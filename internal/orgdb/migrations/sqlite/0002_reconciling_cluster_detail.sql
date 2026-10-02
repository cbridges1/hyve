-- kubernetes_version is the cluster's own reported Kubernetes version
-- (client-go's discovery.ServerVersion().GitVersion — the exact same live
-- call the periodic reachability health check already makes, see
-- checkReconcilingClusterHealth) — captured alongside reachable/
-- last_checked_at/last_error on the same sweep tick rather than a
-- separate round trip, and surfaced in the console's own reconciling-
-- cluster detail card, which previously showed nothing beyond a name and
-- a reachable/unreachable badge. NULL until the first successful check,
-- matching reachable's own "never checked yet" convention.
ALTER TABLE reconciling_clusters ADD COLUMN kubernetes_version TEXT;
