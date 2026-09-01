import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { load as loadYaml } from 'js-yaml'
import { AdminOnly } from '../components/RoleGate'
import { ReadyBadge } from '../components/ConditionBadge'
import { Modal } from '../components/Modal'
import { ModeTabs } from '../components/ModeTabs'
import { clustersApi } from '../lib/api/clusters'
import { templatesApi } from '../lib/api/templates'
import { ApiError } from '../lib/api/client'
import type { ClusterDefinitionSpec } from '../lib/api/types'
import { useApi } from '../lib/useApi'

const YAML_SPEC_PLACEHOLDER = `# Full ClusterDefinitionSpec — same shape the CLI/kubectl would apply.
# See internal/apis/hyve/v1alpha1/clusterdefinition_types.go for every field.
driver:
  source: github.com/org/hyve-x-module
  version: latest
region: nyc3
params:
  size: s-1vcpu-2gb
`

const inputClass =
  'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

function NewClusterForm({ onCreated }: { onCreated: () => void }) {
  const { data: templates } = useApi(() => templatesApi.list())
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'template' | 'yaml'>('template')
  const [name, setName] = useState('')
  const [templateName, setTemplateName] = useState('')
  const [region, setRegion] = useState('')
  const [params, setParams] = useState<{ key: string; value: string }[]>([])
  const [specYaml, setSpecYaml] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        New cluster
      </button>
    )
  }

  function reset() {
    setOpen(false)
    setName('')
    setTemplateName('')
    setRegion('')
    setParams([])
    setSpecYaml('')
    setMode('template')
  }

  function paramsObject(): Record<string, string> | undefined {
    const entries = params.filter((p) => p.key.trim() !== '')
    return entries.length ? Object.fromEntries(entries.map((p) => [p.key.trim(), p.value])) : undefined
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      if (mode === 'yaml') {
        let spec: ClusterDefinitionSpec
        try {
          spec = (loadYaml(specYaml) ?? {}) as ClusterDefinitionSpec
        } catch (err) {
          setError(err instanceof Error ? `Invalid YAML: ${err.message}` : 'Invalid YAML')
          setSubmitting(false)
          return
        }
        await clustersApi.create({ name, spec })
      } else {
        await clustersApi.create({
          name,
          template: templateName ? { name: templateName, region: region || undefined, params: paramsObject() } : undefined,
        })
      }
      reset()
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create cluster')
    } finally {
      setSubmitting(false)
    }
  }

  const canSubmit = mode === 'yaml' ? !!name && !!specYaml.trim() : !!name && !!templateName

  return (
    <Modal title="New cluster" onClose={reset}>
      <ModeTabs
        value={mode}
        onChange={setMode}
        options={[
          { value: 'template', label: 'From template' },
          { value: 'yaml', label: 'Advanced (YAML)' },
        ]}
      />

      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </label>

      {mode === 'template' ? (
        <>
          <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="text-sm">
              <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Template</span>
              <select value={templateName} onChange={(e) => setTemplateName(e.target.value)} className={inputClass}>
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
                className={inputClass}
              />
            </label>
          </div>

          <div className="mb-3">
            <span className="mb-1 block text-sm text-neutral-600 dark:text-neutral-400">Param overrides (optional)</span>
            <div className="space-y-2">
              {params.map((p, i) => (
                <div key={i} className="flex gap-2">
                  <input
                    value={p.key}
                    onChange={(e) => setParams(params.map((row, j) => (j === i ? { ...row, key: e.target.value } : row)))}
                    placeholder="key"
                    className={inputClass}
                  />
                  <input
                    value={p.value}
                    onChange={(e) => setParams(params.map((row, j) => (j === i ? { ...row, value: e.target.value } : row)))}
                    placeholder="value"
                    className={inputClass}
                  />
                  <button
                    type="button"
                    onClick={() => setParams(params.filter((_, j) => j !== i))}
                    aria-label="Remove param"
                    className="shrink-0 rounded-lg px-2 text-neutral-400 transition-colors hover:bg-neutral-100 hover:text-neutral-600 dark:hover:bg-neutral-700 dark:hover:text-neutral-300"
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
            <button
              type="button"
              onClick={() => setParams([...params, { key: '', value: '' }])}
              className="mt-2 rounded-lg px-2.5 py-1 text-sm font-medium text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
            >
              + Add param
            </button>
          </div>
        </>
      ) : (
        <label className="mb-3 block text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Spec (YAML)</span>
          <textarea
            value={specYaml}
            onChange={(e) => setSpecYaml(e.target.value)}
            rows={10}
            placeholder={YAML_SPEC_PLACEHOLDER}
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-800"
          />
          <span className="mt-1 block text-xs text-neutral-500">
            Bypasses templates entirely — posts this spec directly, same as <code>kubectl apply</code> on a ClusterDefinition.
          </span>
        </label>
      )}

      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={reset}
          className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={!canSubmit || submitting}
          onClick={submit}
          className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  )
}

export function ClustersListPage() {
  const navigate = useNavigate()
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

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {clusters?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No clusters yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {clusters?.map((c) => (
            <div
              key={c.name}
              onClick={() => navigate(`/clusters/${encodeURIComponent(c.name)}`)}
              className="flex cursor-pointer flex-col gap-2 px-4 py-3 transition-colors hover:bg-neutral-50 sm:flex-row sm:items-center sm:justify-between dark:hover:bg-neutral-800/50"
            >
              <div className="min-w-0">
                <div className="font-medium text-neutral-900 dark:text-neutral-100">{c.name}</div>
                <div className="truncate text-xs text-neutral-500 dark:text-neutral-500">{c.driver}</div>
              </div>
              <div className="flex shrink-0 items-center gap-3">
                <span className="text-xs text-neutral-500 dark:text-neutral-500">{c.accessMethod || 'client-side (default)'}</span>
                <ReadyBadge conditions={c.conditions} />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
