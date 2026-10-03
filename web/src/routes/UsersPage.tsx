import { useState } from "react";
import { Link } from "react-router-dom";
import {
  roleLabel,
  RoleAdmin,
  RoleReadOnly,
  RoleSuperadmin,
} from "../lib/api/auth";
import { Modal } from "../components/Modal";
import { accountsApi, type CreatedAccount } from "../lib/api/accounts";
import { ApiError } from "../lib/api/client";
import { useConfirm } from "../lib/confirm";
import { useApi } from "../lib/useApi";
import { useSession } from "../lib/useAuth";
import { useWhoami } from "../lib/useWhoami";

function NewUserForm({
  onCreated,
}: {
  onCreated: (account: CreatedAccount) => void;
}) {
  const who = useWhoami().data;
  const [open, setOpen] = useState(false);
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState(RoleReadOnly);
  // existing: add a user who already has a login (a member of another
  // organization) — just a username and role; they keep their own
  // password and email.
  const [existing, setExisting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        Add user
      </button>
    );
  }

  async function submit() {
    setError(null);
    setSubmitting(true);
    try {
      const created = await accountsApi.create(
        existing
          ? { username, role }
          : { username, password, role, email: email.trim() },
      );
      setOpen(false);
      setUsername("");
      setEmail("");
      setPassword("");
      setRole(RoleReadOnly);
      setExisting(false);
      onCreated(created);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create user");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal title="Add user" onClose={() => setOpen(false)}>
      <div className="mb-4 inline-flex rounded-lg border border-neutral-300 p-0.5 text-sm dark:border-neutral-700">
        {[
          { value: false, label: "New user" },
          { value: true, label: "Existing user" },
        ].map((opt) => (
          <button
            key={opt.label}
            type="button"
            onClick={() => setExisting(opt.value)}
            className={`rounded-md px-3 py-1 transition-colors ${
              existing === opt.value
                ? "bg-neutral-900 text-white dark:bg-white dark:text-neutral-900"
                : "text-neutral-600 hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>
      <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">
            Username
          </span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="off"
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
        {!existing && (
          <>
            <label className="text-sm">
              <span className="mb-1 block text-neutral-600 dark:text-neutral-400">
                Email
              </span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="off"
                className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
              />
            </label>
            <label className="text-sm">
              <span className="mb-1 block text-neutral-600 dark:text-neutral-400">
                Password
              </span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
              />
            </label>
          </>
        )}
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">
            Role
          </span>
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
          >
            <option value={RoleReadOnly}>{roleLabel(RoleReadOnly)}</option>
            <option value={RoleAdmin}>{roleLabel(RoleAdmin)}</option>
            {who?.role === RoleSuperadmin && (
              <option value={RoleSuperadmin}>
                {roleLabel(RoleSuperadmin)}
              </option>
            )}
          </select>
        </label>
      </div>
      {role === RoleSuperadmin && (
        <p className="mb-3 text-xs text-neutral-500">
          Always created in the control plane, regardless of what "Viewing" is
          currently set to — a superadmin has no tenant of their own.
        </p>
      )}
      <p className="mb-3 text-xs text-neutral-500">
        {existing
          ? 'They keep their own login and can switch to this organization from "Viewing".'
          : "They'll get an email saying their account was created. Send them the password separately."}
      </p>
      {error && (
        <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>
      )}
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={
            !username ||
            (!existing && (!email.trim() || !password)) ||
            submitting
          }
          onClick={submit}
          className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? "Adding…" : existing ? "Add" : "Create"}
        </button>
      </div>
      <p className="mt-3 text-xs text-neutral-500">
        A custom role with its own ServiceAccount still needs{" "}
        <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">
          hyve cluster-config api create-user --role custom
        </code>
        .
      </p>
    </Modal>
  );
}

export function UsersPage() {
  const session = useSession();
  const confirm = useConfirm();
  const {
    data: users,
    loading,
    error,
    reload,
  } = useApi(() => accountsApi.list());
  const [created, setCreated] = useState<CreatedAccount | null>(null);

  function onCreated(account: CreatedAccount) {
    setCreated(account);
    reload();
  }

  async function onDelete(username: string) {
    const ok = await confirm({
      title: `Delete user "${username}"?`,
      message:
        "This immediately revokes their access — any active session they have will stop working on its next request.",
      confirmLabel: "Delete user",
      danger: true,
    });
    if (!ok) return;
    await accountsApi.delete(username);
    reload();
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">
          Users
        </h1>
        <NewUserForm onCreated={onCreated} />
      </div>

      {created &&
        (created.existingUser ? (
          <p className="mb-4 text-sm text-green-700 dark:text-green-400">
            Added {created.username} to this organization.
          </p>
        ) : created.emailSent ? (
          <p className="mb-4 text-sm text-green-700 dark:text-green-400">
            Created {created.username} — a notification was emailed to{" "}
            {created.email}.
          </p>
        ) : (
          <p className="mb-4 text-sm text-amber-700 dark:text-amber-400">
            Created {created.username}, but the notification email to{" "}
            {created.email} couldn't be sent — check the SMTP settings under
            Control plane → Settings.
          </p>
        ))}

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && (
        <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
      )}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {users?.length === 0 && (
          <p className="p-6 text-center text-sm text-neutral-500">
            No local users yet.
          </p>
        )}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {users?.map((u) => {
            const isSelf = u.username === session?.username;
            return (
              <div
                key={u.username}
                className="flex items-center justify-between gap-3 px-4 py-3"
              >
                <Link
                  to={`/users/${encodeURIComponent(u.username)}`}
                  className="min-w-0 flex-1 group"
                >
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-neutral-900 group-hover:underline dark:text-neutral-100">
                      {u.username}
                    </span>
                    {isSelf && (
                      <span className="text-xs text-neutral-400">(you)</span>
                    )}
                    <span className="rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
                      {roleLabel(u.role)}
                    </span>
                  </div>
                  {u.email && (
                    <div className="mt-0.5 truncate text-xs text-neutral-500">
                      {u.email}
                    </div>
                  )}
                </Link>
                {!isSelf && (
                  <button
                    type="button"
                    onClick={() => onDelete(u.username)}
                    className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                  >
                    Delete
                  </button>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
