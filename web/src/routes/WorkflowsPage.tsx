import { useState } from 'react'
import { AdminOnly } from '../components/RoleGate'
import { RefStatusBadge } from '../components/ConditionBadge'
import { ApiError } from '../lib/api/client'
import { workflowsApi } from '../lib/api/workflows'
import { useApi } from '../lib/useApi'

function NewWorkflowForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [jobName, setJobName] = useState('main')
  const [script, setScript] = useState('echo hello')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white dark:bg-neutral-100 dark:text-neutral-900">
        New workflow
      </button>
    )
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await workflowsApi.create({
        name,
        spec: { jobs: [{ name: jobName, steps: [{ name: 'run', script }] }] },
      })
      setOpen(false)
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create workflow')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mb-4 rounded border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-3 grid grid-cols-3 gap-3 text-sm">
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Job name</span>
          <input value={jobName} onChange={(e) => setJobName(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Step script</span>
          <input value={script} onChange={(e) => setScript(e.target.value)} className="w-full rounded border border-neutral-300 px-2 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
      </div>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button type="button" disabled={!name || submitting} onClick={submit} className="rounded bg-neutral-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900">
          {submitting ? 'Creating…' : 'Create'}
        </button>
        <button type="button" onClick={() => setOpen(false)} className="rounded px-3 py-1.5 text-sm text-neutral-600 dark:text-neutral-400">
          Cancel
        </button>
      </div>
    </div>
  )
}

export function WorkflowsPage() {
  const { data: workflows, loading, error, reload } = useApi(() => workflowsApi.list())

  async function onDelete(name: string) {
    if (!confirm(`Delete workflow "${name}"?`)) return
    await workflowsApi.delete(name)
    reload()
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Workflows</h1>
        <AdminOnly>
          <NewWorkflowForm onCreated={reload} />
        </AdminOnly>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="space-y-2">
        {workflows?.map((w) => (
          <div key={w.name} className="flex items-center justify-between rounded border border-neutral-200 bg-white p-3 dark:border-neutral-800 dark:bg-neutral-900">
            <div className="flex items-center gap-2">
              <span className="font-medium">{w.name}</span>
              {w.refStatus ? (
                <>
                  <RefStatusBadge resolved={w.refStatus.resolved} error={w.refStatus.error} />
                  <span className="text-sm text-neutral-500">{w.refStatus.source}</span>
                </>
              ) : (
                <span className="text-sm text-neutral-500">{w.spec?.jobs?.length ?? 0} job(s)</span>
              )}
            </div>
            {!w.refStatus && (
              <AdminOnly>
                <button type="button" onClick={() => onDelete(w.name)} className="text-sm text-red-600 hover:underline dark:text-red-400">
                  Delete
                </button>
              </AdminOnly>
            )}
          </div>
        ))}
      </div>
      <p className="mt-3 text-xs text-neutral-400">
        Git-ref-backed workflows (source shown above) are mirrored read-only from the controller and can't be deleted here — remove the reference from whatever ClusterDefinition/Template declares it instead.
      </p>
    </div>
  )
}
