import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Modal } from '../components/Modal'
import { accountsApi, type Account } from '../lib/api/accounts'
import { roleLabel, RoleAdmin, RoleReadOnly, RoleSuperadmin } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'
import { useSession } from '../lib/useAuth'
import { useWhoami } from '../lib/useWhoami'

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-xs font-medium text-neutral-500 dark:text-neutral-500">{label}</div>
      <div className="mt-1">{children}</div>
    </div>
  )
}

// RoleField lets an admin/superadmin promote or demote a user in place.
// Crossing the superadmin boundary — either direction — needs a superadmin
// caller (enforced server-side too; this just keeps the UI from offering a
// control that would just 403) and, when demoting OUT of superadmin, an
// explicit destination namespace, since a superadmin binding has no tenant
// of its own to fall back to (see handleUpdateAccount's own doc comment).
//
// onSaved(crossedBoundary) rather than a single onChanged: crossing the
// superadmin boundary moves the binding to a different namespace than
// whichever one this page's own GET is currently scoped to (see
// Server.TenantNamespace) — confirmed live, reloading in place after such
// a move 404s ("account not found"), since the binding genuinely isn't
// visible at the old scope anymore. The caller navigates away instead in
// that case rather than trying to redisplay a record that just left view.
function RoleField({
  user,
  isSelf,
  onSaved,
}: {
  user: Account
  isSelf: boolean
  onSaved: (crossedSuperadminBoundary: boolean) => void
}) {
  const who = useWhoami().data
  const isSuperadminCaller = who?.role === RoleSuperadmin
  const [role, setRole] = useState(user.role)
  const [namespace, setNamespace] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const crossesSuperadmin = (user.role === RoleSuperadmin) !== (role === RoleSuperadmin)
  const demotingSuperadmin = user.role === RoleSuperadmin && role !== RoleSuperadmin
  const dirty = role !== user.role
  const canSubmit = dirty && (!demotingSuperadmin || namespace.trim() !== '')

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await accountsApi.update(user.username, { role, namespace: demotingSuperadmin ? namespace.trim() : undefined })
      setNamespace('')
      onSaved(crossesSuperadmin)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to change role')
    } finally {
      setSubmitting(false)
    }
  }

  if (isSelf) {
    return (
      <div className="flex items-center gap-2">
        <span className="rounded-full bg-neutral-100 px-2.5 py-1 text-sm font-medium text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
          {roleLabel(user.role)}
        </span>
        <span className="text-xs text-neutral-500">You can't change your own role.</span>
      </div>
    )
  }

  return (
    <div>
      <div className="flex items-center gap-2">
        <select
          value={role}
          onChange={(e) => setRole(e.target.value)}
          className="rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
        >
          <option value={RoleReadOnly}>{roleLabel(RoleReadOnly)}</option>
          <option value={RoleAdmin}>{roleLabel(RoleAdmin)}</option>
          {(isSuperadminCaller || user.role === RoleSuperadmin) && (
            <option value={RoleSuperadmin}>{roleLabel(RoleSuperadmin)}</option>
          )}
        </select>
        {dirty && (
          <button
            type="button"
            disabled={!canSubmit || submitting}
            onClick={submit}
            className="rounded-lg bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
          >
            {submitting ? 'Saving…' : 'Save'}
          </button>
        )}
      </div>
      {demotingSuperadmin && dirty && (
        <div className="mt-2">
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">
              Destination namespace <span className="text-xs text-neutral-500">— a superadmin has no tenant of its own, so moving out of it needs one</span>
            </span>
            <input
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              placeholder="e.g. acme"
              className="w-full max-w-xs rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
            />
          </label>
        </div>
      )}
      {role === RoleSuperadmin && dirty && user.role !== RoleSuperadmin && (
        <p className="mt-2 text-xs text-neutral-500">Moves this user into the control plane — a superadmin has no tenant of its own.</p>
      )}
      {error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}

function EmailField({ user, onChanged }: { user: Account; onChanged: () => void }) {
  const [editing, setEditing] = useState(false)
  const [email, setEmail] = useState(user.email ?? '')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await accountsApi.update(user.username, { email })
      setEditing(false)
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to update email')
    } finally {
      setSubmitting(false)
    }
  }

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        <span className="text-sm text-neutral-900 dark:text-neutral-100">{user.email || <span className="text-neutral-400">not set</span>}</span>
        <button
          type="button"
          onClick={() => {
            setEmail(user.email ?? '')
            setEditing(true)
          }}
          className="text-xs font-medium text-neutral-500 hover:text-neutral-900 dark:hover:text-neutral-100"
        >
          Edit
        </button>
      </div>
    )
  }

  return (
    <div>
      <div className="flex items-center gap-2">
        <input
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          autoFocus
          placeholder="name@example.com"
          className="w-full max-w-xs rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
        />
        <button
          type="button"
          disabled={submitting}
          onClick={submit}
          className="rounded-lg bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Saving…' : 'Save'}
        </button>
        <button
          type="button"
          onClick={() => setEditing(false)}
          className="text-sm text-neutral-500 hover:text-neutral-900 dark:hover:text-neutral-100"
        >
          Cancel
        </button>
      </div>
      {error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}

function ResetPasswordForm({ username, onClose }: { username: string; onClose: () => void }) {
  const [newPassword, setNewPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [done, setDone] = useState(false)

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await accountsApi.updatePassword(username, { newPassword })
      setDone(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to reset password')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title={`Reset password for "${username}"`} onClose={onClose}>
      {done ? (
        <>
          <p className="mb-4 text-sm text-neutral-600 dark:text-neutral-400">
            Password reset. Share it with <span className="font-medium">{username}</span> through a secure channel —
            it won't be shown again.
          </p>
          <div className="flex justify-end">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
            >
              Done
            </button>
          </div>
        </>
      ) : (
        <>
          <label className="mb-3 block text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">New password</span>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete="new-password"
              autoFocus
              className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
            />
          </label>
          {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={!newPassword || submitting}
              onClick={submit}
              className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
            >
              {submitting ? 'Resetting…' : 'Reset password'}
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}

export function UserDetailPage() {
  const { username = '' } = useParams()
  const navigate = useNavigate()
  const session = useSession()
  const confirm = useConfirm()
  const { data: user, loading, error, reload } = useApi(() => accountsApi.get(username), [username])
  const [resettingPassword, setResettingPassword] = useState(false)
  const isSelf = username === session?.username

  async function onDelete() {
    const ok = await confirm({
      title: `Delete user "${username}"?`,
      message: 'This immediately revokes their access — any active session they have will stop working on its next request.',
      confirmLabel: 'Delete user',
      danger: true,
    })
    if (!ok) return
    await accountsApi.delete(username)
    navigate('/users')
  }

  return (
    <div>
      <Link to="/users" className="mb-3 inline-block text-sm text-neutral-500 hover:text-neutral-900 dark:hover:text-neutral-100">
        ← Users
      </Link>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">{username}</h1>
        {!isSelf && (
          <button
            type="button"
            onClick={onDelete}
            className="rounded-lg px-2.5 py-1.5 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
          >
            Delete user
          </button>
        )}
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      {user && (
        <div className="space-y-5 rounded-xl border border-neutral-200 bg-white p-5 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          <Field label="Role">
            <RoleField
              user={user}
              isSelf={isSelf}
              onSaved={(crossedSuperadminBoundary) => (crossedSuperadminBoundary ? navigate('/users') : reload())}
            />
          </Field>
          <Field label="Email">
            <EmailField user={user} onChanged={reload} />
          </Field>
          <Field label="Password">
            <button
              type="button"
              onClick={() => setResettingPassword(true)}
              className="rounded-lg border border-neutral-300 px-3 py-1.5 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-100 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800"
            >
              Reset password
            </button>
          </Field>
        </div>
      )}

      {resettingPassword && <ResetPasswordForm username={username} onClose={() => setResettingPassword(false)} />}
    </div>
  )
}
