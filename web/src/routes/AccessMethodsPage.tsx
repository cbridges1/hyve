import { useNavigate } from 'react-router-dom'
import { accessMethodsApi } from '../lib/api/accessMethods'
import { useApi } from '../lib/useApi'

export function AccessMethodsPage() {
  const navigate = useNavigate()
  const { data: methods, loading, error } = useApi(() => accessMethodsApi.list())

  return (
    <div>
      <div className="mb-4">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Access methods</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          Server-side auth brokers (Rancher, etc.) a cluster can reference to mint a kubeconfig via a third-party
          identity provider — see <code>hyve cluster auth</code>. Managed via <code>kubectl apply</code>, not here.
        </p>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {methods?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No access methods yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {methods?.map((m) => (
            <div
              key={m.name}
              onClick={() => navigate(`/access-methods/${encodeURIComponent(m.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="min-w-0">
                <div className="font-medium text-neutral-900 dark:text-neutral-100">{m.name}</div>
                <div className="truncate text-xs text-neutral-500 dark:text-neutral-500">{m.spec.serverURL}</div>
              </div>
              <span className="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
                {m.spec.inlineAuth ? 'inline auth' : m.spec.driver?.source ? 'driver' : 'unconfigured'}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
