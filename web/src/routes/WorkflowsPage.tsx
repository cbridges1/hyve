import { useState, type MouseEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { AdminOnly } from '../components/RoleGate'
import { RefStatusBadge } from '../components/ConditionBadge'
import { ApiError } from '../lib/api/client'
import { workflowsApi } from '../lib/api/workflows'
import { useConfirm } from '../lib/confirm'
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
      <button type="button" onClick={() => setOpen(true)} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
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
    <div className="mb-4 rounded-xl border border-neutral-200 bg-white p-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
      <div className="mb-3 grid grid-cols-1 gap-3 text-sm sm:grid-cols-3">
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Job name</span>
          <input value={jobName} onChange={(e) => setJobName(e.target.value)} className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
        <label>
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Step script</span>
          <input value={script} onChange={(e) => setScript(e.target.value)} className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800" />
        </label>
      </div>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex gap-2">
        <button type="button" disabled={!name || submitting} onClick={submit} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
          {submitting ? 'Creating…' : 'Create'}
        </button>
        <button type="button" onClick={() => setOpen(false)} className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-800">
          Cancel
        </button>
      </div>
    </div>
  )
}

export function WorkflowsPage() {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: workflows, loading, error, reload } = useApi(() => workflowsApi.list())

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
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Workflows</h1>
        <AdminOnly>
          <NewWorkflowForm onCreated={reload} />
        </AdminOnly>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {workflows?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No workflows yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {workflows?.map((w) => (
            <div
              key={w.name}
              onClick={() => navigate(`/workflows/${encodeURIComponent(w.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className="font-medium text-neutral-900 dark:text-neutral-100">{w.name}</span>
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
