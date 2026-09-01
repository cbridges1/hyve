import { useState } from 'react'
import { RoleAdmin, RoleReadOnly } from '../lib/api/auth'
import { accountsApi } from '../lib/api/accounts'
import { ApiError } from '../lib/api/client'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'
import { useSession } from '../lib/useAuth'

function NewAccountForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState(RoleReadOnly)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        New account
      </button>
    )
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await accountsApi.create({ username, password, role })
      setOpen(false)
      setUsername('')
      setPassword('')
      setRole(RoleReadOnly)
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create account')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mb-4 rounded-xl border border-neutral-200 bg-white p-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Username</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="off"
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Role</span>
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
          >
            <option value={RoleReadOnly}>read-only</option>
            <option value={RoleAdmin}>admin</option>
          </select>
        </label>
      </div>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button
          type="button"
          disabled={!username || !password || submitting}
          onClick={submit}
          className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800"
        >
          Cancel
        </button>
      </div>
      <p className="mt-3 text-xs text-neutral-500">
        Only the built-in admin/read-only roles are supported here — a custom role with its own ServiceAccount still
        needs <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">hyve cluster-config api create-user --role custom</code>.
      </p>
    </div>
  )
}

export function AccountsPage() {
  const session = useSession()
  const confirm = useConfirm()
  const { data: accounts, loading, error, reload } = useApi(() => accountsApi.list())

  async function onDelete(username: string) {
    const ok = await confirm({
      title: `Delete account "${username}"?`,
      message: 'This immediately revokes their access — any active session they have will stop working on its next request.',
      confirmLabel: 'Delete account',
      danger: true,
    })
    if (!ok) return
    await accountsApi.delete(username)
    reload()
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Accounts</h1>
        <NewAccountForm onCreated={reload} />
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {accounts?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No local accounts yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {accounts?.map((a) => {
            const isSelf = a.username === session?.username
            return (
              <div key={a.username} className="flex items-center justify-between gap-3 px-4 py-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-neutral-900 dark:text-neutral-100">{a.username}</span>
                  {isSelf && <span className="text-xs text-neutral-400">(you)</span>}
                  <span className="rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
                    {a.role}
                  </span>
                </div>
                {!isSelf && (
                  <button
                    type="button"
                    onClick={() => onDelete(a.username)}
                    className="rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                  >
                    Delete
                  </button>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
