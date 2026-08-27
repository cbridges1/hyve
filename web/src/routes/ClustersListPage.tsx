import { useState } from 'react'
import { Link } from 'react-router-dom'
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
        className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white dark:bg-neutral-100 dark:text-neutral-900"
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
    <div className="mb-4 rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-3 grid grid-cols-3 gap-3">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full rounded border border-neutral-300 px-2 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Template</span>
          <select
            value={templateName}
            onChange={(e) => setTemplateName(e.target.value)}
            className="w-full rounded border border-neutral-300 px-2 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
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
            className="w-full rounded border border-neutral-300 px-2 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
      </div>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button
          type="button"
          disabled={!name || !templateName || submitting}
          onClick={submit}
          className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900"
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="rounded px-3 py-1.5 text-sm text-neutral-600 dark:text-neutral-400"
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

export function ClustersListPage() {
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

      {clusters && (
        <div className="overflow-hidden rounded border border-neutral-200 dark:border-neutral-800">
          <table className="w-full text-sm">
            <thead className="bg-neutral-100 text-left text-neutral-600 dark:bg-neutral-900 dark:text-neutral-400">
              <tr>
                <th className="px-3 py-2 font-medium">Name</th>
                <th className="px-3 py-2 font-medium">Driver</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 font-medium">Access</th>
              </tr>
            </thead>
            <tbody>
              {clusters.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-3 py-6 text-center text-neutral-500">
                    No clusters yet.
                  </td>
                </tr>
              )}
              {clusters.map((c) => (
                <tr
                  key={c.name}
                  className="border-t border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-950"
                >
                  <td className="px-3 py-2">
                    <Link to={`/clusters/${encodeURIComponent(c.name)}`} className="font-medium hover:underline">
                      {c.name}
                    </Link>
                  </td>
                  <td className="px-3 py-2 text-neutral-500">{c.driver}</td>
                  <td className="px-3 py-2">
                    <ReadyBadge conditions={c.conditions} />
                  </td>
                  <td className="px-3 py-2 text-neutral-500">{c.accessMethod || 'client-side (default)'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
