import { useState, type FormEvent } from 'react'
import { Card, EmptyState } from '../components/Card'
import { reconcilingClustersApi } from '../lib/api/reconcilingClusters'
import { ApiError } from '../lib/api/client'
import { useApi } from '../lib/useApi'

function RegisterClusterForm({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState('')
  const [kubeconfig, setKubeconfig] = useState('')
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setMessage(null)
    setSubmitting(true)
    try {
      await reconcilingClustersApi.create(name, kubeconfig)
      setMessage(`"${name}" is registered.`)
      setName('')
      setKubeconfig('')
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to register reconciling cluster')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card title="Register a reconciling cluster">
      <p className="mb-3 text-xs text-neutral-500">
        A physical cluster hyve-controller can reconcile an organization's infrastructure against, instead of this
        install's own home cluster. Re-submitting an existing name rotates its kubeconfig rather than erroring — use
        this to update credentials for a cluster already in use.
      </p>
      <form onSubmit={onSubmit} className="space-y-2">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="cluster name"
          required
          className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
        />
        <textarea
          value={kubeconfig}
          onChange={(e) => setKubeconfig(e.target.value)}
          placeholder="paste the cluster's kubeconfig YAML here"
          required
          rows={6}
          className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
        <p className="text-xs text-neutral-500">
          Stored as a Secret on this install's own control plane — never echoed back by any endpoint once registered.
        </p>
        <button
          type="submit"
          disabled={!name || !kubeconfig || submitting}
          className="rounded-lg bg-neutral-900 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Registering…' : 'Register'}
        </button>
      </form>
      {message && <p className="mt-2 text-sm text-green-700 dark:text-green-400">{message}</p>}
      {error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>}
    </Card>
  )
}

function ReachableBadge({ reachable }: { reachable?: boolean }) {
  if (reachable === undefined) {
    return <span className="text-xs text-neutral-400 dark:text-neutral-600">not checked yet</span>
  }
  return reachable ? (
    <span className="text-xs font-medium text-green-700 dark:text-green-400">reachable</span>
  ) : (
    <span className="text-xs font-medium text-red-600 dark:text-red-400">unreachable</span>
  )
}

export function ReconcilingClustersPage() {
  const { data: clusters, loading, error, reload } = useApi(() => reconcilingClustersApi.list())

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Reconciling clusters</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          Physical clusters an organization's own resources can live on — superadmin-only. Assign one to an
          organization from the Organizations page.
        </p>
      </div>

      <RegisterClusterForm onCreated={reload} />

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {clusters?.length === 0 && <EmptyState>No reconciling clusters registered yet.</EmptyState>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {clusters?.map((rc) => (
            <div key={rc.name} className="flex items-center justify-between gap-3 px-4 py-3">
              <div>
                <div className="font-medium text-neutral-900 dark:text-neutral-100">{rc.name}</div>
                {rc.lastError && (
                  <div className="mt-0.5 max-w-md truncate text-xs text-red-600 dark:text-red-400" title={rc.lastError}>
                    {rc.lastError}
                  </div>
                )}
              </div>
              <div className="text-right">
                <ReachableBadge reachable={rc.reachable} />
                {rc.lastCheckedAt && (
                  <div className="mt-0.5 text-xs text-neutral-400 dark:text-neutral-600">
                    checked {new Date(rc.lastCheckedAt).toLocaleString()}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
