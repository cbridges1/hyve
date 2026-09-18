-- email is an optional contact address for a local binding — groundwork
-- for the platform's move toward email-driven flows (login by email today;
-- notifications/invites/password-reset-by-email are future work, not yet
-- implemented). NULL for every binding created before this migration, and
-- for any binding an admin never bothers to set one on. Scoped unique
-- per-namespace, not globally — the same isolation boundary identity/
-- username already uses (see bindings_namespace_env_identity), so two
-- different tenants may each have a user at the same email address.
ALTER TABLE bindings ADD COLUMN email TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS bindings_namespace_email
    ON bindings (namespace, email) WHERE email IS NOT NULL;
