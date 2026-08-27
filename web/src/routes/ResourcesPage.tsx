import { useState } from 'react'
import { AdminOnly } from '../components/RoleGate'
import { RefStatusBadge } from '../components/ConditionBadge'
import { ApiError } from '../lib/api/client'
import { resourcesApi } from '../lib/api/resources'
import { useApi } from '../lib/useApi'

function NewResourceForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [manifest, setManifest] = useState('apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white dark:bg-neutral-100 dark:text-neutral-900">
        New resource
      </button>
    )
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await resourcesApi.create({ name, spec: { manifest } })
      setOpen(false)
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create resource')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mb-4 rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
      </label>
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Manifest (YAML)</span>
        <textarea
          value={manifest}
          onChange={(e) => setManifest(e.target.value)}
          rows={6}
          className="w-full rounded border border-neutral-300 px-2 py-1.5 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
      </label>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button type="button" disabled={!name || submitting} onClick={submit} className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900">
          {submitting ? 'Creating…' : 'Create'}
        </button>
        <button type="button" onClick={() => setOpen(false)} className="rounded px-3 py-1.5 text-sm text-neutral-600 dark:text-neutral-400">
          Cancel
        </button>
      </div>
    </div>
  )
}

export function ResourcesPage() {
  const { data: resources, loading, error, reload } = useApi(() => resourcesApi.list())

  async function onDelete(name: string) {
    if (!confirm(`Delete resource "${name}"?`)) return
    await resourcesApi.delete(name)
    reload()
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Resources</h1>
        <AdminOnly>
          <NewResourceForm onCreated={reload} />
        </AdminOnly>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="space-y-2">
        {resources?.map((r) => (
          <div key={r.name} className="flex items-center justify-between rounded border border-neutral-200 bg-white p-3 dark:border-neutral-800 dark:bg-neutral-900">
            <div className="flex items-center gap-2">
              <span className="font-medium">{r.name}</span>
              {r.refStatus && (
                <>
                  <RefStatusBadge resolved={r.refStatus.resolved} error={r.refStatus.error} />
                  <span className="text-sm text-neutral-500">{r.refStatus.source}</span>
                </>
              )}
            </div>
            {!r.refStatus && (
              <AdminOnly>
                <button type="button" onClick={() => onDelete(r.name)} className="text-sm text-red-600 hover:underline dark:text-red-400">
                  Delete
                </button>
              </AdminOnly>
            )}
          </div>
        ))}
      </div>
      <p className="mt-3 text-xs text-neutral-400">
        This is the standalone Resource CRD type — distinct from a cluster's own spec.resources[] entries, shown on each cluster's detail page.
      </p>
    </div>
  )
}
