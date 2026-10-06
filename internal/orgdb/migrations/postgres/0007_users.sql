-- users is one row per person: the login credential (username, email,
-- password) that used to be copied onto every binding. A binding is now
-- only a membership — (namespace, environment, role) for users.username
-- = bindings.identity — so one account can belong to several
-- organizations without a separate password for each. Existing per-binding
-- credentials are carried over once at startup by
-- Store.EnsureUsersFromBindings (same-username bindings merge into one
-- user); bindings.password_hash/email are no longer read or written.
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT,
    password_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email
    ON users (email) WHERE email IS NOT NULL;

-- Password reset tokens now belong to a user, not a binding. One live
-- token per user, enforced by Store.CreatePasswordResetToken; no ON DELETE
-- CASCADE, matching the rest of this schema — Store.DeleteUser removes a
-- user's token itself. password_reset_tokens (per binding) is retired.
CREATE TABLE IF NOT EXISTS user_password_reset_tokens (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS user_password_reset_tokens_user_id
    ON user_password_reset_tokens (user_id);
