import { useState } from 'react'
import { AdminOnly } from '../components/RoleGate'
import { RoleAdmin } from '../lib/api/auth'
import { ApiError } from '../lib/api/client'
import { secretsApi } from '../lib/api/secrets'
import { useApi } from '../lib/useApi'
import { useWhoami } from '../lib/useWhoami'

export function SecretsPage() {
  const { data: who } = useWhoami()
  const isAdmin = who?.role === RoleAdmin
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
    if (!confirm(`Unset "${key}"?`)) return
    await secretsApi.unset(key)
    reload()
    if (values) reveal()
  }

  return (
    <div>
      <h1 className="mb-4 text-lg font-semibold text-neutral-900 dark:text-neutral-100">Secrets</h1>
      <p className="mb-4 text-sm text-neutral-500">
        Backed by a single shared <code className="rounded bg-neutral-100 px-1 dark:bg-neutral-800">hyve-cli-secrets</code>{' '}
        Kubernetes Secret — key names are visible to any authenticated caller, values require the admin role.
      </p>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <AdminOnly>
        <div className="mb-4 flex items-end gap-2 rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Key</span>
            <input value={newKey} onChange={(e) => setNewKey(e.target.value)} className="rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Value</span>
            <input type="password" value={newValue} onChange={(e) => setNewValue(e.target.value)} className="rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
          </label>
          <button type="button" disabled={!newKey} onClick={onSet} className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900">
            Set
          </button>
          {!values && (
            <button type="button" onClick={reveal} className="ml-auto rounded px-3 py-1.5 text-sm text-neutral-600 dark:text-neutral-400">
              Reveal values
            </button>
          )}
        </div>
        {formError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{formError}</p>}
        {revealError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{revealError}</p>}
      </AdminOnly>

      <div className="overflow-hidden rounded border border-neutral-200 dark:border-neutral-800">
        <table className="w-full text-sm">
          <thead className="bg-neutral-100 text-left text-neutral-600 dark:bg-neutral-900 dark:text-neutral-400">
            <tr>
              <th className="px-3 py-2 font-medium">Key</th>
              {values && <th className="px-3 py-2 font-medium">Value</th>}
              {isAdmin && <th className="px-3 py-2" />}
            </tr>
          </thead>
          <tbody>
            {names?.length === 0 && (
              <tr>
                <td colSpan={3} className="px-3 py-6 text-center text-neutral-500">
                  No secrets set.
                </td>
              </tr>
            )}
            {names?.map((key) => (
              <tr key={key} className="border-t border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-950">
                <td className="px-3 py-2 font-mono text-xs">{key}</td>
                {values && <td className="px-3 py-2 font-mono text-xs text-neutral-500">{values[key]}</td>}
                {isAdmin && (
                  <td className="px-3 py-2 text-right">
                    <button type="button" onClick={() => onUnset(key)} className="text-sm text-red-600 hover:underline dark:text-red-400">
                      Unset
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
