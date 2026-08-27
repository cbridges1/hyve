import { modulesApi } from '../lib/api/modules'
import { useApi } from '../lib/useApi'

export function ModulesPage() {
  const { data: modules, loading, error } = useApi(() => modulesApi.list())

  return (
    <div>
      <h1 className="mb-4 text-lg font-semibold text-neutral-900 dark:text-neutral-100">Modules</h1>
      <p className="mb-4 text-sm text-neutral-500">
        Read-only — modules resolve automatically as a side effect of the controller reconciling a cluster that
        references them. There's no create/delete here.
      </p>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded border border-neutral-200 dark:border-neutral-800">
        <table className="w-full text-sm">
          <thead className="bg-neutral-100 text-left text-neutral-600 dark:bg-neutral-900 dark:text-neutral-400">
            <tr>
              <th className="px-3 py-2 font-medium">Source</th>
              <th className="px-3 py-2 font-medium">Version</th>
              <th className="px-3 py-2 font-medium">Resolved</th>
              <th className="px-3 py-2 font-medium">SHA256</th>
            </tr>
          </thead>
          <tbody>
            {modules?.length === 0 && (
              <tr>
                <td colSpan={4} className="px-3 py-6 text-center text-neutral-500">
                  No modules resolved yet.
                </td>
              </tr>
            )}
            {modules?.map((m) => (
              <tr key={m.name} className="border-t border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-950">
                <td className="px-3 py-2">{m.spec.source}</td>
                <td className="px-3 py-2 text-neutral-500">{m.spec.version}</td>
                <td className="px-3 py-2">
                  {m.status.error ? (
                    <span className="text-red-600 dark:text-red-400" title={m.status.error}>
                      error
                    </span>
                  ) : m.status.resolved ? (
                    'yes'
                  ) : (
                    'no'
                  )}
                </td>
                <td className="px-3 py-2 font-mono text-xs text-neutral-400">{m.status.sha256?.slice(0, 12)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
