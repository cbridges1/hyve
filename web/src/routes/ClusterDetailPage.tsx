import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ReadyBadge } from '../components/ConditionBadge'
import { AdminOnly } from '../components/RoleGate'
import { clustersApi } from '../lib/api/clusters'
import { ApiError } from '../lib/api/client'
import { authContextApi, kubeconfigApi } from '../lib/api/kubeconfig'
import { usePolledApi } from '../lib/useApi'

const POLL_INTERVAL_MS = 5000

function KubeconfigPanel({ name }: { name: string }) {
  const [result, setResult] = useState<{ kind: 'kubeconfig'; text: string } | { kind: 'auth-context'; note: string } | null>(
    null,
  )
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  async function fetchAccess() {
    setError(null)
    setLoading(true)
    setResult(null)
    try {
      const kc = await kubeconfigApi.get(name)
      setResult({ kind: 'kubeconfig', text: kc })
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        // Default access method — client-side auth. Fetch auth-context for
        // inspection, since the console can't execute a driver module's
        // auth op itself (see Phase 11's own scope note on this endpoint).
        try {
          await authContextApi.get(name)
          setResult({
            kind: 'auth-context',
            note: `This cluster uses client-side auth (the default) — run "hyve cluster auth ${name}" from a terminal with the driver module's tools installed.`,
          })
        } catch (innerErr) {
          setError(innerErr instanceof ApiError ? innerErr.message : 'Failed to fetch auth context')
        }
      } else {
        setError(err instanceof ApiError ? err.message : 'Failed to fetch kubeconfig')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-neutral-900 dark:text-neutral-100">Access</h2>
        <button
          type="button"
          onClick={fetchAccess}
          disabled={loading}
          className="rounded bg-neutral-900 px-2.5 py-1 text-xs font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900"
        >
          {loading ? 'Fetching…' : 'Get kubeconfig'}
        </button>
      </div>
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
      {result?.kind === 'auth-context' && <p className="text-sm text-neutral-600 dark:text-neutral-400">{result.note}</p>}
      {result?.kind === 'kubeconfig' && (
        <pre className="max-h-64 overflow-auto rounded bg-neutral-100 p-2 text-xs dark:bg-neutral-800">
          {result.text}
        </pre>
      )}
    </div>
  )
}

export function ClusterDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const { data: cluster, error } = usePolledApi(() => clustersApi.get(name), POLL_INTERVAL_MS, [name])
  const { data: resources } = usePolledApi(() => clustersApi.resources(name), POLL_INTERVAL_MS, [name])
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function onDelete() {
    setDeleteError(null)
    setDeleting(true)
    try {
      await clustersApi.delete(name)
      navigate('/clusters')
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : 'Failed to delete cluster')
      setDeleting(false)
    }
  }

  if (error && !cluster) return <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
  if (!cluster) return <p className="text-sm text-neutral-500">Loading…</p>

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">{cluster.name}</h1>
          <p className="text-sm text-neutral-500">{cluster.driver}</p>
        </div>
        <div className="flex items-center gap-3">
          <ReadyBadge conditions={cluster.conditions} />
          <AdminOnly>
            <button
              type="button"
              onClick={onDelete}
              disabled={deleting}
              className="rounded border border-red-300 px-3 py-1.5 text-sm font-medium text-red-700 disabled:opacity-50 dark:border-red-900 dark:text-red-400"
            >
              {deleting ? 'Deleting…' : 'Delete'}
            </button>
          </AdminOnly>
        </div>
      </div>
      {deleteError && <p className="text-sm text-red-600 dark:text-red-400">{deleteError}</p>}

      <div className="rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
        <h2 className="mb-2 text-sm font-semibold text-neutral-900 dark:text-neutral-100">Conditions</h2>
        {!cluster.conditions?.length && <p className="text-sm text-neutral-500">No conditions reported yet.</p>}
        {cluster.conditions?.map((c) => (
          <div key={c.type} className="flex items-center justify-between border-t border-neutral-100 py-1.5 text-sm first:border-t-0 dark:border-neutral-800">
            <span className="font-medium">{c.type}</span>
            <span className="text-neutral-500">{c.status}</span>
            <span className="max-w-md truncate text-neutral-400" title={c.message}>
              {c.message}
            </span>
          </div>
        ))}
        <p className="mt-2 text-xs text-neutral-400">
          observedGeneration: {cluster.observedGeneration} · polling every {POLL_INTERVAL_MS / 1000}s
        </p>
      </div>

      <KubeconfigPanel name={name} />

      <div className="rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
        <h2 className="mb-2 text-sm font-semibold text-neutral-900 dark:text-neutral-100">Resources</h2>
        {!resources?.resources?.length && <p className="text-sm text-neutral-500">No resources declared.</p>}
        {resources?.resources?.map((r) => {
          const applied = resources.appliedResources?.[r.name]
          return (
            <div key={r.name} className="border-t border-neutral-100 py-1.5 text-sm first:border-t-0 dark:border-neutral-800">
              <div className="flex items-center justify-between">
                <span className="font-medium">{r.name}</span>
                <span className="text-neutral-500">{applied ? `applied ${applied.appliedAt}` : 'not yet applied'}</span>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
