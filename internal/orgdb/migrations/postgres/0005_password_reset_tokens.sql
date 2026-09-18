-- See migrations/sqlite/0005_password_reset_tokens.sql's own comment for
-- the full reasoning. Only difference: TIMESTAMPTZ vs TIMESTAMP.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id TEXT PRIMARY KEY,
    binding_id TEXT NOT NULL REFERENCES bindings(id),
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS password_reset_tokens_binding_id
    ON password_reset_tokens (binding_id);
