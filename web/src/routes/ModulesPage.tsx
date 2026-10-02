import { useNavigate } from 'react-router-dom'
import { modulesApi } from '../lib/api/modules'
import { useApi } from '../lib/useApi'

export function ModulesPage() {
  const navigate = useNavigate()
  const { data: modules, loading, error } = useApi(() => modulesApi.list())

  return (
    <div>
      <h1 className="mb-1 text-lg font-semibold text-neutral-900 dark:text-neutral-100">Modules</h1>
      <p className="mb-4 text-sm text-neutral-500">
        Read-only — modules resolve automatically as a side effect of the controller reconciling a cluster that
        references them. There's no create/delete here.
      </p>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {modules?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No modules resolved yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {modules?.map((m) => (
            <div
              key={m.name}
              onClick={() => navigate(`/modules/${encodeURIComponent(m.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="min-w-0">
                <div className="truncate font-medium text-neutral-900 dark:text-neutral-100">{m.spec.source}</div>
                <div className="text-xs text-neutral-500 dark:text-neutral-500">{m.spec.version}</div>
              </div>
              <div className="shrink-0 text-right">
                {m.status.error ? (
                  <span className="text-sm text-red-600 dark:text-red-400">error</span>
                ) : (
                  <span className="text-sm text-neutral-500">{m.status.resolved ? 'resolved' : 'unresolved'}</span>
                )}
                {m.status.sha256 && (
                  <div className="font-mono text-xs text-neutral-400">{m.status.sha256.slice(0, 12)}</div>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
