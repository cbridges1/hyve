import { useState } from 'react'
import { AdminOnly } from '../components/RoleGate'
import { templatesApi } from '../lib/api/templates'
import { ApiError } from '../lib/api/client'
import type { ClusterDefinitionSpec } from '../lib/api/types'
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
      const spec = await templatesApi.render(name, { region: region || undefined, params })
      setResult(spec)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to render')
    }
  }

  return (
    <div className="mt-2 rounded border border-neutral-200 bg-neutral-50 p-3 dark:border-neutral-800 dark:bg-neutral-950">
      <div className="mb-2 flex gap-2">
        <input
          value={region}
          onChange={(e) => setRegion(e.target.value)}
          placeholder="region override (optional)"
          className="flex-1 rounded border border-neutral-300 px-2 py-1 text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
        <input
          value={paramsText}
          onChange={(e) => setParamsText(e.target.value)}
          placeholder='{"key": "value"}'
          className="flex-1 rounded border border-neutral-300 px-2 py-1 text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
        <button
          type="button"
          onClick={render}
          className="rounded bg-neutral-900 px-2 py-1 text-xs font-medium text-white dark:bg-neutral-100 dark:text-neutral-900"
        >
          Preview render
        </button>
      </div>
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
      {result && (
        <pre className="max-h-48 overflow-auto rounded bg-neutral-100 p-2 text-xs dark:bg-neutral-800">
          {JSON.stringify(result, null, 2)}
        </pre>
      )}
    </div>
  )
}

function NewTemplateForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [driverSource, setDriverSource] = useState('')
  const [driverVersion, setDriverVersion] = useState('latest')
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
        New template
      </button>
    )
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await templatesApi.create({
        name,
        spec: { driver: { source: driverSource, version: driverVersion }, region: region || undefined },
      })
      setOpen(false)
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create template')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mb-4 rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-3 grid grid-cols-4 gap-3 text-sm">
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Driver source</span>
          <input value={driverSource} onChange={(e) => setDriverSource(e.target.value)} placeholder="github.com/org/hyve-x-module" className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Driver version</span>
          <input value={driverVersion} onChange={(e) => setDriverVersion(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Region</span>
          <input value={region} onChange={(e) => setRegion(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
      </div>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button type="button" disabled={!name || !driverSource || submitting} onClick={submit} className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900">
          {submitting ? 'Creating…' : 'Create'}
        </button>
        <button type="button" onClick={() => setOpen(false)} className="rounded px-3 py-1.5 text-sm text-neutral-600 dark:text-neutral-400">
          Cancel
        </button>
      </div>
    </div>
  )
}

export function TemplatesPage() {
  const { data: templates, loading, error, reload } = useApi(() => templatesApi.list())
  const [expanded, setExpanded] = useState<string | null>(null)

  async function onDelete(name: string) {
    if (!confirm(`Delete template "${name}"?`)) return
    await templatesApi.delete(name)
    reload()
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Templates</h1>
        <AdminOnly>
          <NewTemplateForm onCreated={reload} />
        </AdminOnly>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="space-y-2">
        {templates?.map((t) => (
          <div key={t.name} className="rounded border border-neutral-200 bg-white p-3 dark:border-neutral-800 dark:bg-neutral-900">
            <div className="flex items-center justify-between">
              <div>
                <span className="font-medium">{t.name}</span>
                <span className="ml-2 text-sm text-neutral-500">{t.spec.driver.source}@{t.spec.driver.version}</span>
              </div>
              <div className="flex gap-2">
                <button type="button" onClick={() => setExpanded(expanded === t.name ? null : t.name)} className="text-sm text-neutral-500 hover:underline">
                  {expanded === t.name ? 'Hide' : 'Preview render'}
                </button>
                <AdminOnly>
                  <button type="button" onClick={() => onDelete(t.name)} className="text-sm text-red-600 hover:underline dark:text-red-400">
                    Delete
                  </button>
                </AdminOnly>
              </div>
            </div>
            {expanded === t.name && <RenderPreview name={t.name} />}
          </div>
        ))}
      </div>
    </div>
  )
}
