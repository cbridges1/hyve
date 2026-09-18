-- email_settings is a true singleton — one install-wide SMTP config, not
-- namespace-scoped (unlike everything else in this schema). Lives here,
-- not the Kubernetes-CRD-backed HyveConfig, specifically so it works
-- under --home-cluster=none the same way reconciling_clusters' own
-- kubeconfig storage does — see HYVE-EMAIL-IMPLEMENTATION-PLAN.md
-- (nexus-config/docs) for the full reasoning. smtp_password is
-- plaintext-at-rest, the same accepted tradeoff reconciling_clusters.
-- kubeconfig already documents (see migrations/sqlite/0001_init.sql's own
-- comment on that column) — not yet mitigated by application-level
-- encryption, flagged as a future hardening candidate, not silently
-- ignored.
--
-- id is a fixed constant ('singleton'), not a generated UUID — enforces
-- "at most one row" by construction (a second INSERT with the same
-- primary key fails) rather than a separate application-level check.
CREATE TABLE IF NOT EXISTS email_settings (
    id TEXT PRIMARY KEY,
    smtp_host TEXT NOT NULL DEFAULT '',
    smtp_port INTEGER NOT NULL DEFAULT 587,
    smtp_username TEXT,
    smtp_password TEXT,
    -- Implicit TLS (connect already encrypted, typically port 465) when
    -- true; STARTTLS-upgraded plaintext connect (typically port 587)
    -- when false — mirrors Pangolin's own smtp_secure flag exactly (see
    -- the implementation plan's "Baseline" section).
    use_tls INTEGER NOT NULL DEFAULT 0,
    -- Inverted sense from Pangolin's smtp_tls_reject_unauthorized so the
    -- zero value (0/false) is the safe default: skip_verify=false means
    -- certificates ARE validated unless an operator explicitly opts out
    -- (e.g. an internal relay with a self-signed cert).
    skip_verify INTEGER NOT NULL DEFAULT 0,
    from_address TEXT NOT NULL DEFAULT '',
    from_name TEXT,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
