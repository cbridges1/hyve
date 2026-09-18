-- See migrations/sqlite/0004_email_settings.sql's own comment for the
-- full reasoning. Only difference from that file: BOOLEAN vs INTEGER and
-- TIMESTAMPTZ vs TIMESTAMP, same as every other table in this schema.
CREATE TABLE IF NOT EXISTS email_settings (
    id TEXT PRIMARY KEY,
    smtp_host TEXT NOT NULL DEFAULT '',
    smtp_port INTEGER NOT NULL DEFAULT 587,
    smtp_username TEXT,
    smtp_password TEXT,
    use_tls BOOLEAN NOT NULL DEFAULT FALSE,
    skip_verify BOOLEAN NOT NULL DEFAULT FALSE,
    from_address TEXT NOT NULL DEFAULT '',
    from_name TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
