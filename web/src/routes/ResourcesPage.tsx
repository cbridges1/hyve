import { useState, type MouseEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { AdminOnly } from '../components/RoleGate'
import { RefStatusBadge } from '../components/ConditionBadge'
import { Modal } from '../components/Modal'
import { ApiError } from '../lib/api/client'
import { resourcesApi } from '../lib/api/resources'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'

function NewResourceForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [manifest, setManifest] = useState('apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
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
    <Modal title="New resource" onClose={() => setOpen(false)}>
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
      </label>
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Manifest (YAML)</span>
        <textarea
          value={manifest}
          onChange={(e) => setManifest(e.target.value)}
          rows={6}
          className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
      </label>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex justify-end gap-2">
        <button type="button" onClick={() => setOpen(false)} className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700">
          Cancel
        </button>
        <button type="button" disabled={!name || submitting} onClick={submit} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  )
}

export function ResourcesPage() {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: resources, loading, error, reload } = useApi(() => resourcesApi.list())

  async function onDelete(e: MouseEvent, name: string) {
    e.stopPropagation()
    const ok = await confirm({
      title: `Delete resource "${name}"?`,
      message: 'This cannot be undone. Any cluster still referencing this resource by name will fail to resolve it on its next reconcile.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
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

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {resources?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No resources yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {resources?.map((r) => (
            <div
              key={r.name}
              onClick={() => navigate(`/resources/${encodeURIComponent(r.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className="font-medium text-neutral-900 dark:text-neutral-100">{r.name}</span>
                {r.refStatus && (
                  <>
                    <RefStatusBadge resolved={r.refStatus.resolved} error={r.refStatus.error} />
                    <span className="truncate text-xs text-neutral-500">{r.refStatus.source}</span>
                  </>
                )}
              </div>
              {!r.refStatus && (
                <AdminOnly>
                  <button
                    type="button"
                    onClick={(e) => onDelete(e, r.name)}
                    className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                  >
                    Delete
                  </button>
                </AdminOnly>
              )}
            </div>
          ))}
        </div>
      </div>
      <p className="mt-3 text-xs text-neutral-400">
        This is the standalone Resource CRD type — distinct from a cluster's own spec.resources[] entries, shown on
        each cluster's detail page.
      </p>
    </div>
  )
}
