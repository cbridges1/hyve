import { useState } from 'react'
import { AdminOnly } from '../components/RoleGate'
import { RoleAdmin } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { secretsApi } from '../lib/api/secrets'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'
import { useWhoami } from '../lib/useWhoami'

export function SecretsPage() {
  const { data: who } = useWhoami()
  const isAdmin = who?.role === RoleAdmin
  const confirm = useConfirm()
  const { data: names, loading, error, reload } = useApi(() => secretsApi.listNames())
  const [values, setValues] = useState<Record<string, string> | null>(null)
  const [revealError, setRevealError] = useState<string | null>(null)
  const [newKey, setNewKey] = useState('')
  const [newValue, setNewValue] = useState('')
  const [formError, setFormError] = useState<string | null>(null)

  async function reveal() {
    setRevealError(null)
    try {
      setValues(await secretsApi.listValues())
    } catch (err) {
      setRevealError(err instanceof ApiError ? err.message : 'Failed to load values')
    }
  }

  async function onSet() {
    setFormError(null)
    try {
      await secretsApi.set(newKey, newValue)
      setNewKey('')
      setNewValue('')
      reload()
      if (values) reveal()
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Failed to set secret')
    }
  }

  async function onUnset(key: string) {
    const ok = await confirm({
      title: `Unset "${key}"?`,
      message: 'Any cluster or workflow currently depending on this secret will lose access to it immediately.',
      confirmLabel: 'Unset',
      danger: true,
    })
    if (!ok) return
    await secretsApi.unset(key)
    reload()
    if (values) reveal()
  }

  return (
    <div>
      <h1 className="mb-1 text-lg font-semibold text-neutral-900 dark:text-neutral-100">Secrets</h1>
      <p className="mb-4 text-sm text-neutral-500">
        Backed by a single shared <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">hyve-cli-secrets</code>{' '}
        Kubernetes Secret — key names are visible to any authenticated caller, values require the admin role.
      </p>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <AdminOnly>
        <div className="mb-4 rounded-xl border border-neutral-200 bg-white p-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
            <label className="text-sm sm:flex-1">
              <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Key</span>
              <input value={newKey} onChange={(e) => setNewKey(e.target.value)} className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
            </label>
            <label className="text-sm sm:flex-1">
              <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Value</span>
              <input type="password" value={newValue} onChange={(e) => setNewValue(e.target.value)} className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
            </label>
            <div className="flex gap-2">
              <button type="button" disabled={!newKey} onClick={onSet} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
                Set
              </button>
              {!values && (
                <button type="button" onClick={reveal} className="rounded-lg px-3 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800">
                  Reveal values
                </button>
              )}
            </div>
          </div>
        </div>
        {formError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{formError}</p>}
        {revealError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{revealError}</p>}
      </AdminOnly>

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {names?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No secrets set.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {names?.map((key) => (
            <div key={key} className="flex items-center justify-between gap-3 px-4 py-3">
              <span className="font-mono text-xs text-neutral-800 dark:text-neutral-200">{key}</span>
              <div className="flex items-center gap-3">
                {values && <span className="font-mono text-xs text-neutral-500">{values[key]}</span>}
                {isAdmin && (
                  <button
                    type="button"
                    onClick={() => onUnset(key)}
                    className="rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                  >
                    Unset
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
