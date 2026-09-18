-- See migrations/sqlite/0003_binding_email.sql's own comment for the full
-- reasoning.
ALTER TABLE bindings ADD COLUMN email TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS bindings_namespace_email
    ON bindings (namespace, email) WHERE email IS NOT NULL;
