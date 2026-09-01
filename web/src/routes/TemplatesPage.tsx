import { useState, type MouseEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { load as loadYaml } from 'js-yaml'
import { AdminOnly } from '../components/RoleGate'
import { Modal } from '../components/Modal'
import { ModeTabs } from '../components/ModeTabs'
import { ApiError } from '../lib/api/client'
import { templatesApi } from '../lib/api/templates'
import type { TemplateSpec } from '../lib/api/types'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'

const YAML_SPEC_PLACEHOLDER = `# Full TemplateSpec — same shape the CLI/kubectl would apply.
# See internal/apis/hyve/v1alpha1/template_types.go for every field.
driver:
  source: github.com/org/hyve-x-module
  version: latest
region: nyc3
params:
  size: s-1vcpu-2gb
workflows:
  onCreate:
    - name: bootstrap
`

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800'

function NewTemplateForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'form' | 'yaml'>('form')
  const [name, setName] = useState('')
  const [driverSource, setDriverSource] = useState('')
  const [driverVersion, setDriverVersion] = useState('latest')
  const [region, setRegion] = useState('')
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
        New template
      </button>
    )
  }

  function reset() {
    setOpen(false)
    setName('')
    setDriverSource('')
    setDriverVersion('latest')
    setRegion('')
    setSpecYaml('')
    setMode('form')
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      let spec: TemplateSpec
      if (mode === 'yaml') {
        try {
          spec = (loadYaml(specYaml) ?? {}) as TemplateSpec
        } catch (err) {
          setError(err instanceof Error ? `Invalid YAML: ${err.message}` : 'Invalid YAML')
          setSubmitting(false)
          return
        }
      } else {
        spec = { driver: { source: driverSource, version: driverVersion }, region: region || undefined }
      }
      await templatesApi.create({ name, spec })
      reset()
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create template')
    } finally {
      setSubmitting(false)
    }
  }

  const canSubmit = mode === 'yaml' ? !!name && !!specYaml.trim() : !!name && !!driverSource

  return (
    <Modal title="New template" onClose={reset}>
      <ModeTabs
        value={mode}
        onChange={setMode}
        options={[
          { value: 'form', label: 'Form' },
          { value: 'yaml', label: 'Advanced (YAML)' },
        ]}
      />

      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </label>

      {mode === 'form' ? (
        <div className="mb-3 grid grid-cols-1 gap-3 text-sm sm:grid-cols-3">
          <label>
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Driver source</span>
            <input value={driverSource} onChange={(e) => setDriverSource(e.target.value)} placeholder="github.com/org/hyve-x-module" className={inputClass} />
          </label>
          <label>
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Driver version</span>
            <input value={driverVersion} onChange={(e) => setDriverVersion(e.target.value)} className={inputClass} />
          </label>
          <label>
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Region</span>
            <input value={region} onChange={(e) => setRegion(e.target.value)} className={inputClass} />
          </label>
        </div>
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
            Supports every TemplateSpec field — params, workflows, resources, runner — not just driver/region.
          </span>
        </label>
      )}

      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex justify-end gap-2">
        <button type="button" onClick={reset} className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700">
          Cancel
        </button>
        <button type="button" disabled={!canSubmit || submitting} onClick={submit} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  )
}

export function TemplatesPage() {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: templates, loading, error, reload } = useApi(() => templatesApi.list())

  async function onDelete(e: MouseEvent, name: string) {
    e.stopPropagation()
    const ok = await confirm({
      title: `Delete template "${name}"?`,
      message: 'Clusters already created from this template are unaffected — this only removes the template itself.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
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

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {templates?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No templates yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {templates?.map((t) => (
            <div
              key={t.name}
              onClick={() => navigate(`/templates/${encodeURIComponent(t.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="min-w-0">
                <div className="font-medium text-neutral-900 dark:text-neutral-100">{t.name}</div>
                <div className="truncate text-xs text-neutral-500 dark:text-neutral-500">
                  {t.spec.driver.source}@{t.spec.driver.version}
                  {t.spec.region ? ` · ${t.spec.region}` : ''}
                </div>
              </div>
              <AdminOnly>
                <button
                  type="button"
                  onClick={(e) => onDelete(e, t.name)}
                  className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                >
                  Delete
                </button>
              </AdminOnly>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
