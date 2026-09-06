import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { BackLink, Card, Field } from '../components/Card'
import { ResourceRefList } from '../components/ResourceRefList'
import { AdminOnly } from '../components/RoleGate'
import { SpecEditor } from '../components/SpecEditor'
import { WorkflowHooks } from '../components/WorkflowHooks'
import { ApiError } from '../lib/api/client'
import { templatesApi } from '../lib/api/templates'
import type { ClusterDefinitionSpec } from '../lib/api/types'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'

function RenderPreview({ name }: { name: string }) {
  const [region, setRegion] = useState('')
  const [paramsText, setParamsText] = useState('{}')
  const [result, setResult] = useState<ClusterDefinitionSpec | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function render() {
    setError(null)
    setResult(null)
    let params: Record<string, string> = {}
    try {
      params = JSON.parse(paramsText)
    } catch {
      setError('Params must be valid JSON, e.g. {"node_count": "5"}')
      return
    }
    try {
      setResult(await templatesApi.render(name, { region: region || undefined, params }))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to render')
    }
  }

  return (
    <Card title="Render preview">
      <div className="mb-3 flex flex-col gap-2 sm:flex-row">
        <input
          value={region}
          onChange={(e) => setRegion(e.target.value)}
          placeholder="region override (optional)"
          className="flex-1 rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
        />
        <input
          value={paramsText}
          onChange={(e) => setParamsText(e.target.value)}
          placeholder='{"key": "value"}'
          className="flex-1 rounded-lg border border-neutral-300 px-2.5 py-1.5 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
        <button
          type="button"
          onClick={render}
          className="shrink-0 rounded-lg bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          Preview
        </button>
      </div>
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
      {result && (
        <pre className="max-h-64 overflow-auto rounded-lg bg-neutral-50 p-3 text-xs text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300">
          {JSON.stringify(result, null, 2)}
        </pre>
      )}
    </Card>
  )
}

export function TemplateDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: tpl, loading, error, reload } = useApi(() => templatesApi.get(name), [name])
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function onDelete() {
    if (!tpl) return
    const ok = await confirm({
      title: `Delete template "${tpl.name}"?`,
      message: 'Clusters already created from this template are unaffected — this only removes the template itself.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    setDeleteError(null)
    try {
      await templatesApi.delete(tpl.name)
      navigate('/templates')
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : 'Failed to delete template')
    }
  }

  if (loading) return <p className="text-sm text-neutral-500">Loading…</p>
  if (error || !tpl) return <p className="text-sm text-red-600 dark:text-red-400">{error ?? 'Not found'}</p>

  return (
    <div className="space-y-4">
      <BackLink to="/templates" label="Templates" />

      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-lg font-semibold text-neutral-900 dark:text-neutral-100">{tpl.name}</h1>
          {tpl.spec.description && <p className="mt-0.5 text-sm text-neutral-500">{tpl.spec.description}</p>}
        </div>
        <AdminOnly>
          <button
            type="button"
            onClick={onDelete}
            className="shrink-0 rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-700 transition-colors hover:bg-red-50 dark:border-red-900/60 dark:text-red-400 dark:hover:bg-red-950/40"
          >
            Delete
          </button>
        </AdminOnly>
      </div>
      {deleteError && <p className="text-sm text-red-600 dark:text-red-400">{deleteError}</p>}

      <Card title="Overview">
        <Field label="Driver">
          {tpl.spec.driver.source}
          {tpl.spec.driver.version ? `@${tpl.spec.driver.version}` : ''}
        </Field>
        <Field label="Region">{tpl.spec.region || <span className="text-neutral-400">not set</span>}</Field>
        {tpl.spec.runner?.image && <Field label="Runner image">{tpl.spec.runner.image}</Field>}
        {tpl.spec.schedule && <Field label="Schedule">{tpl.spec.schedule} (cron)</Field>}
        <Field label="Lock params">{tpl.spec.lockParams ? 'yes — --set is rejected when creating from this template' : 'no'}</Field>
      </Card>

      <Card title="Params">
        {tpl.spec.params && Object.keys(tpl.spec.params).length > 0 ? (
          Object.entries(tpl.spec.params).map(([k, v]) => (
            <Field key={k} label={k}>
              {v}
            </Field>
          ))
        ) : (
          <p className="text-sm text-neutral-500 dark:text-neutral-500">No default params.</p>
        )}
      </Card>

      <Card title="Lifecycle hooks">
        <WorkflowHooks workflows={tpl.spec.workflows} />
      </Card>

      <Card title="Resources">
        <ResourceRefList resources={tpl.spec.resources} />
      </Card>

      <RenderPreview name={tpl.name} />

      <AdminOnly>
        <SpecEditor spec={tpl.spec} onSave={(spec) => templatesApi.update(tpl.name, spec).then(reload)} />
      </AdminOnly>
    </div>
  )
}
