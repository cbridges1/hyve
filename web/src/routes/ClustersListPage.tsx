import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AdminOnly } from '../components/RoleGate'
import { ReadyBadge } from '../components/ConditionBadge'
import { clustersApi } from '../lib/api/clusters'
import { templatesApi } from '../lib/api/templates'
import { ApiError } from '../lib/api/client'
import { useApi } from '../lib/useApi'

function NewClusterForm({ onCreated }: { onCreated: () => void }) {
  const { data: templates } = useApi(() => templatesApi.list())
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [templateName, setTemplateName] = useState('')
  const [region, setRegion] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        New cluster
      </button>
    )
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await clustersApi.create({
        name,
        template: templateName ? { name: templateName, region: region || undefined } : undefined,
      })
      setOpen(false)
      setName('')
      setTemplateName('')
      setRegion('')
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create cluster')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mb-4 rounded-xl border border-neutral-200 bg-white p-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Template</span>
          <select
            value={templateName}
            onChange={(e) => setTemplateName(e.target.value)}
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
          >
            <option value="">— select —</option>
            {templates?.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Region (optional)</span>
          <input
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder={templates?.find((t) => t.name === templateName)?.spec.region ?? 'template default'}
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
      </div>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button
          type="button"
          disabled={!name || !templateName || submitting}
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
    </div>
  )
}

export function ClustersListPage() {
  const navigate = useNavigate()
  const { data: clusters, loading, error, reload } = useApi(() => clustersApi.list())

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Clusters</h1>
        <AdminOnly>
          <NewClusterForm onCreated={reload} />
        </AdminOnly>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {clusters?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No clusters yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {clusters?.map((c) => (
            <div
              key={c.name}
              onClick={() => navigate(`/clusters/${encodeURIComponent(c.name)}`)}
              className="flex cursor-pointer flex-col gap-2 px-4 py-3 transition-colors hover:bg-neutral-50 sm:flex-row sm:items-center sm:justify-between dark:hover:bg-neutral-800/50"
            >
              <div className="min-w-0">
                <div className="font-medium text-neutral-900 dark:text-neutral-100">{c.name}</div>
                <div className="truncate text-xs text-neutral-500 dark:text-neutral-500">{c.driver}</div>
              </div>
              <div className="flex shrink-0 items-center gap-3">
                <span className="text-xs text-neutral-500 dark:text-neutral-500">{c.accessMethod || 'client-side (default)'}</span>
                <ReadyBadge conditions={c.conditions} />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
