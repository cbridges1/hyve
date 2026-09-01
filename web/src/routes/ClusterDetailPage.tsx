import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { BackLink, Card, EmptyState } from '../components/Card'
import { ReadyBadge } from '../components/ConditionBadge'
import { AdminOnly } from '../components/RoleGate'
import { clustersApi } from '../lib/api/clusters'
import { ApiError } from '../lib/api/client'
import { authContextApi, kubeconfigApi } from '../lib/api/kubeconfig'
import { useConfirm } from '../lib/confirm'
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
    <Card
      title="Access"
      action={
        <button
          type="button"
          onClick={fetchAccess}
          disabled={loading}
          className="rounded-lg bg-neutral-900 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {loading ? 'Fetching…' : 'Get kubeconfig'}
        </button>
      }
    >
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
      {result?.kind === 'auth-context' && <p className="text-sm text-neutral-600 dark:text-neutral-400">{result.note}</p>}
      {result?.kind === 'kubeconfig' && (
        <pre className="max-h-64 overflow-auto rounded-lg bg-neutral-50 p-3 text-xs text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300">
          {result.text}
        </pre>
      )}
      {!result && !error && <p className="text-sm text-neutral-500 dark:text-neutral-500">Not fetched yet.</p>}
    </Card>
  )
}

export function ClusterDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: cluster, error } = usePolledApi(() => clustersApi.get(name), POLL_INTERVAL_MS, [name])
  const { data: resources } = usePolledApi(() => clustersApi.resources(name), POLL_INTERVAL_MS, [name])
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function onDelete() {
    const ok = await confirm({
      title: `Delete cluster "${name}"?`,
      message: 'This runs the driver module\'s delete operation and tears down the real infrastructure behind this cluster. This cannot be undone.',
      confirmLabel: 'Delete cluster',
      danger: true,
    })
    if (!ok) return
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
      <BackLink to="/clusters" label="Clusters" />

      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-lg font-semibold text-neutral-900 dark:text-neutral-100">{cluster.name}</h1>
          <p className="text-sm text-neutral-500">{cluster.driver}</p>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <ReadyBadge conditions={cluster.conditions} />
          <AdminOnly>
            <button
              type="button"
              onClick={onDelete}
              disabled={deleting}
              className="rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-700 transition-colors hover:bg-red-50 disabled:opacity-50 dark:border-red-900/60 dark:text-red-400 dark:hover:bg-red-950/40"
            >
              {deleting ? 'Deleting…' : 'Delete'}
            </button>
          </AdminOnly>
        </div>
      </div>
      {deleteError && <p className="text-sm text-red-600 dark:text-red-400">{deleteError}</p>}

      <Card title="Conditions">
        {!cluster.conditions?.length && <EmptyState>No conditions reported yet.</EmptyState>}
        {cluster.conditions?.map((c) => (
          <div key={c.type} className="flex flex-col gap-0.5 border-t border-neutral-100 py-2 text-sm first:border-t-0 sm:flex-row sm:items-center sm:justify-between dark:border-neutral-800/70">
            <span className="font-medium text-neutral-900 dark:text-neutral-100">{c.type}</span>
            <span className="text-neutral-500">{c.status}</span>
            <span className="text-neutral-400 sm:max-w-md sm:truncate" title={c.message}>
              {c.message}
            </span>
          </div>
        ))}
        <p className="mt-3 text-xs text-neutral-400">
          observedGeneration: {cluster.observedGeneration} · polling every {POLL_INTERVAL_MS / 1000}s
        </p>
      </Card>

      <KubeconfigPanel name={name} />

      <Card title="Resources">
        {!resources?.resources?.length && <EmptyState>No resources declared.</EmptyState>}
        {resources?.resources?.map((r) => {
          const applied = resources.appliedResources?.[r.name]
          return (
            <div key={r.name} className="flex flex-col gap-0.5 border-t border-neutral-100 py-2 text-sm first:border-t-0 sm:flex-row sm:items-center sm:justify-between dark:border-neutral-800/70">
              <span className="font-medium text-neutral-900 dark:text-neutral-100">{r.name}</span>
              <span className="text-neutral-500">{applied ? `applied ${applied.appliedAt}` : 'not yet applied'}</span>
            </div>
          )
        })}
      </Card>
    </div>
  )
}
