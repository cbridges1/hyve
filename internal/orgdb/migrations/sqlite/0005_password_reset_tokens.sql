-- password_reset_tokens backs the self-service forgot-password flow (see
-- HYVE-EMAIL-IMPLEMENTATION-PLAN.md, Milestone 4). At most one live token
-- per binding, enforced at the application level (delete-then-insert
-- inside one call — see Store.CreatePasswordResetToken) rather than a
-- UNIQUE constraint here, the same "safe to re-run" shape
-- CreateBinding/the create-user CLI already use. token_hash, never the
-- raw token — it's a bearer secret good for a live password reset, same
-- treatment as bindings.password_hash.
-- No ON DELETE CASCADE — matches every other REFERENCES in this schema
-- (SQLite only enforces FKs at all when PRAGMA foreign_keys=ON is set per
-- connection, see 0001_init.sql's own comment on organizations.
-- reconciling_cluster_id). DeleteBinding cleans up any live token
-- explicitly instead — see Store.DeleteBinding.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id TEXT PRIMARY KEY,
    binding_id TEXT NOT NULL REFERENCES bindings(id),
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS password_reset_tokens_binding_id
    ON password_reset_tokens (binding_id);
