import { useState, type MouseEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { load as loadYaml } from 'js-yaml'
import { AdminOnly } from '../components/RoleGate'
import { RefStatusBadge } from '../components/ConditionBadge'
import { EnvironmentBadge, EnvironmentFilterSelect, EnvironmentPickerField, useEnvironments } from '../components/Environment'
import { Modal } from '../components/Modal'
import { ModeTabs } from '../components/ModeTabs'
import { YamlEditor } from '../components/YamlEditor'
import { ApiError } from '../lib/api/client'
import { workflowsApi } from '../lib/api/workflows'
import type { WorkflowSpec } from '../lib/api/types'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'
import { useEnvironmentFilter } from '../lib/useEnvironmentFilter'

const YAML_SPEC_PLACEHOLDER = `# Full WorkflowSpec — same shape the CLI/kubectl would apply.
# See internal/apis/hyve/v1alpha1/workflow_types.go for every field.
jobs:
  - name: main
    steps:
      - name: run
        script: echo hello
inputs: []
triggers: []
`

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800'

function NewWorkflowForm({ onCreated, defaultEnv }: { onCreated: () => void; defaultEnv: string }) {
  const { data: environments } = useEnvironments()
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'form' | 'yaml'>('form')
  const [name, setName] = useState('')
  const [jobName, setJobName] = useState('main')
  const [script, setScript] = useState('echo hello')
  const [specYaml, setSpecYaml] = useState('')
  const [env, setEnv] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const envRequired = (environments?.length ?? 0) > 1
  const selectedEnv = env || defaultEnv

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
        New workflow
      </button>
    )
  }

  function reset() {
    setOpen(false)
    setName('')
    setJobName('main')
    setScript('echo hello')
    setSpecYaml('')
    setMode('form')
    setEnv('')
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      let spec: WorkflowSpec
      if (mode === 'yaml') {
        try {
          spec = (loadYaml(specYaml) ?? {}) as WorkflowSpec
        } catch (err) {
          setError(err instanceof Error ? `Invalid YAML: ${err.message}` : 'Invalid YAML')
          setSubmitting(false)
          return
        }
      } else {
        spec = { jobs: [{ name: jobName, steps: [{ name: 'run', script }] }] }
      }
      await workflowsApi.create({ name, spec }, selectedEnv || undefined)
      reset()
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create workflow')
    } finally {
      setSubmitting(false)
    }
  }

  const canSubmit = (mode === 'yaml' ? !!name && !!specYaml.trim() : !!name) && (!envRequired || !!selectedEnv)

  return (
    <Modal title="New workflow" onClose={reset}>
      <ModeTabs
        value={mode}
        onChange={setMode}
        options={[
          { value: 'form', label: 'Form' },
          { value: 'yaml', label: 'Advanced (YAML)' },
        ]}
      />

      <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
        </label>
        <EnvironmentPickerField value={env} onChange={setEnv} defaultValue={defaultEnv} />
      </div>

      {mode === 'form' ? (
        <div className="mb-3 grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
          <label>
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Job name</span>
            <input value={jobName} onChange={(e) => setJobName(e.target.value)} className={inputClass} />
          </label>
          <label>
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Step script</span>
            <input value={script} onChange={(e) => setScript(e.target.value)} className={inputClass} />
          </label>
        </div>
      ) : (
        <div className="mb-3">
          <YamlEditor value={specYaml} onChange={setSpecYaml} onNameDetected={setName} extractSpec placeholder={YAML_SPEC_PLACEHOLDER} />
          <span className="mt-1 block text-xs text-neutral-500">
            Supports multiple jobs/steps, inputs, triggers, requirements, secretsFrom — not just one job with one step.
          </span>
        </div>
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

export function WorkflowsPage() {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: workflows, loading, error, reload } = useApi(() => workflowsApi.list())
  const [envFilter, setEnvFilter] = useEnvironmentFilter()
  const filtered = workflows?.filter((w) => !envFilter || w.environment === envFilter)

  async function onDelete(e: MouseEvent, name: string) {
    e.stopPropagation()
    const ok = await confirm({
      title: `Delete workflow "${name}"?`,
      message: 'This cannot be undone. Any cluster or template still referencing this workflow by name will fail to resolve it on its next run.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    await workflowsApi.delete(name)
    reload()
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Workflows</h1>
        <div className="flex items-center gap-2">
          <EnvironmentFilterSelect value={envFilter} onChange={setEnvFilter} />
          <AdminOnly>
            <NewWorkflowForm onCreated={reload} defaultEnv={envFilter} />
          </AdminOnly>
        </div>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {filtered?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No workflows yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {filtered?.map((w) => (
            <div
              key={w.name}
              onClick={() => navigate(`/workflows/${encodeURIComponent(w.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className="font-medium text-neutral-900 dark:text-neutral-100">{w.name}</span>
                <EnvironmentBadge environment={w.environment} />
                {w.refStatus ? (
                  <>
                    <RefStatusBadge resolved={w.refStatus.resolved} error={w.refStatus.error} />
                    <span className="truncate text-xs text-neutral-500">{w.refStatus.source}</span>
                  </>
                ) : (
                  <span className="text-xs text-neutral-500">{w.spec?.jobs?.length ?? 0} job(s)</span>
                )}
              </div>
              {!w.refStatus && (
                <AdminOnly>
                  <button
                    type="button"
                    onClick={(e) => onDelete(e, w.name)}
                    className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
                  >
                    Delete
                  </button>
                </AdminOnly>
              )}
            </div>
          ))}
        </div>
      </div>
      <p className="mt-3 text-xs text-neutral-400">
        Git-ref-backed workflows are mirrored read-only from the controller and can't be deleted here — remove the
        reference from whatever ClusterDefinition/Template declares it instead.
      </p>
    </div>
  )
}
