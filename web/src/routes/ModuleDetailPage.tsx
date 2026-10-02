import { useParams } from 'react-router-dom'
import { BackLink, Card, Field } from '../components/Card'
import { modulesApi } from '../lib/api/modules'
import { useApi } from '../lib/useApi'

export function ModuleDetailPage() {
  const { name = '' } = useParams()
  const { data: mod, loading, error } = useApi(() => modulesApi.get(name), [name])

  if (loading) return <p className="text-sm text-neutral-500">Loading…</p>
  if (error || !mod) return <p className="text-sm text-red-600 dark:text-red-400">{error ?? 'Not found'}</p>

  return (
    <div className="space-y-4">
      <BackLink to="/modules" label="Modules" />

      <h1 className="truncate text-lg font-semibold text-neutral-900 dark:text-neutral-100">{mod.spec.source}</h1>

      <Card title="Overview">
        <Field label="Source">{mod.spec.source}</Field>
        <Field label="Version">{mod.spec.version || 'latest'}</Field>
        <Field label="Resolved">
          {mod.status.error ? (
            <span className="text-red-600 dark:text-red-400">error</span>
          ) : mod.status.resolved ? (
            'yes'
          ) : (
            'no'
          )}
        </Field>
        {mod.status.resolvedAt && <Field label="Resolved at">{mod.status.resolvedAt}</Field>}
        {mod.status.sha256 && (
          <Field label="SHA256">
            <span className="font-mono">{mod.status.sha256}</span>
          </Field>
        )}
        {mod.status.error && (
          <Field label="Error">
            <span className="text-red-600 dark:text-red-400">{mod.status.error}</span>
          </Field>
        )}
      </Card>

      <p className="text-xs text-neutral-500 dark:text-neutral-500">
        Read-only — modules resolve automatically as a side effect of the controller reconciling a cluster that
        references them.
      </p>
    </div>
  )
}
