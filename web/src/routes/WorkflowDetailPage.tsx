import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { BackLink, Card, CodeBlock, Field } from '../components/Card'
import { RefStatusBadge } from '../components/ConditionBadge'
import { AdminOnly } from '../components/RoleGate'
import { ApiError } from '../lib/api/client'
import { workflowsApi } from '../lib/api/workflows'
import type { WorkflowJob, WorkflowStep } from '../lib/api/types'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'

function StepView({ step }: { step: WorkflowStep }) {
  return (
    <div className="rounded-lg border border-neutral-200 p-3 dark:border-neutral-800">
      <div className="mb-1.5 flex items-center justify-between">
        <span className="font-medium text-neutral-900 dark:text-neutral-100">{step.name}</span>
        {step.container && (
          <span className="rounded bg-neutral-100 px-1.5 py-0.5 font-mono text-xs text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
            {step.container}
          </span>
        )}
      </div>
      {step.description && <p className="mb-2 text-xs text-neutral-500">{step.description}</p>}
      {step.if && <p className="mb-2 font-mono text-xs text-neutral-500">if: {step.if}</p>}
      {step.command && <CodeBlock>{step.command}</CodeBlock>}
      {step.script && <CodeBlock>{step.script}</CodeBlock>}
      {step.action && (
        <p className="font-mono text-xs text-neutral-700 dark:text-neutral-300">
          action: {step.action}
          {step.with && Object.keys(step.with).length > 0 && ` (${Object.entries(step.with).map(([k, v]) => `${k}=${v}`).join(', ')})`}
        </p>
      )}
      {step.continueOnError && <p className="mt-1.5 text-xs text-amber-600 dark:text-amber-400">continues on error</p>}
    </div>
  )
}

function JobView({ job }: { job: WorkflowJob }) {
  return (
    <div className="rounded-lg bg-neutral-50 p-3 dark:bg-neutral-950/50">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="font-semibold text-neutral-900 dark:text-neutral-100">{job.name}</span>
        {job.cluster && (
          <span className="rounded-full bg-neutral-100 px-2 py-0.5 text-xs text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
            runs against: {job.cluster}
          </span>
        )}
        {job.dependsOn && job.dependsOn.length > 0 && (
          <span className="text-xs text-neutral-500">depends on: {job.dependsOn.join(', ')}</span>
        )}
      </div>
      <div className="space-y-2">
        {job.steps.map((step, i) => (
          <StepView key={i} step={step} />
        ))}
      </div>
    </div>
  )
}

export function WorkflowDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: wf, loading, error } = useApi(() => workflowsApi.get(name), [name])
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function onDelete() {
    const ok = await confirm({
      title: `Delete workflow "${name}"?`,
      message: 'This cannot be undone. Any cluster or template still referencing this workflow by name will fail to resolve it on its next run.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    setDeleteError(null)
    try {
      await workflowsApi.delete(name)
      navigate('/workflows')
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : 'Failed to delete workflow')
    }
  }

  if (loading) return <p className="text-sm text-neutral-500">Loading…</p>
  if (error || !wf) return <p className="text-sm text-red-600 dark:text-red-400">{error ?? 'Not found'}</p>

  return (
    <div className="space-y-4">
      <BackLink to="/workflows" label="Workflows" />

      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-lg font-semibold text-neutral-900 dark:text-neutral-100">{wf.name}</h1>
            {wf.refStatus && <RefStatusBadge resolved={wf.refStatus.resolved} error={wf.refStatus.error} />}
          </div>
          {wf.spec?.description && <p className="mt-0.5 text-sm text-neutral-500">{wf.spec.description}</p>}
        </div>
        {!wf.refStatus && (
          <AdminOnly>
            <button
              type="button"
              onClick={onDelete}
              className="shrink-0 rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-700 transition-colors hover:bg-red-50 dark:border-red-900/60 dark:text-red-400 dark:hover:bg-red-950/40"
            >
              Delete
            </button>
          </AdminOnly>
        )}
      </div>
      {deleteError && <p className="text-sm text-red-600 dark:text-red-400">{deleteError}</p>}

      {wf.refStatus ? (
        <Card title="Git reference">
          <Field label="Source">{wf.refStatus.source}</Field>
          <Field label="Resolved">{wf.refStatus.resolved ? 'yes' : 'no'}</Field>
          {wf.refStatus.rawVersion && <Field label="Requested version">{wf.refStatus.rawVersion}</Field>}
          {wf.refStatus.resolvedVersion && <Field label="Resolved version">{wf.refStatus.resolvedVersion}</Field>}
          {wf.refStatus.sha256 && <Field label="SHA256">{wf.refStatus.sha256}</Field>}
          {wf.refStatus.error && (
            <Field label="Error">
              <span className="text-red-600 dark:text-red-400">{wf.refStatus.error}</span>
            </Field>
          )}
          <p className="mt-3 text-xs text-neutral-500">
            This is a git-ref-backed workflow, mirrored read-only from the controller — its content lives in the source
            repository above, not here.
          </p>
        </Card>
      ) : (
        wf.spec && (
          <>
            <Card title="Overview">
              <Field label="Runtime">{wf.spec.runtime || 'cluster (default)'}</Field>
              {wf.spec.preFlight?.cluster && <Field label="Pre-flight check">{wf.spec.preFlight.cluster}</Field>}
              {wf.spec.inputs && wf.spec.inputs.length > 0 && (
                <Field label="Inputs">
                  <div className="space-y-1">
                    {wf.spec.inputs.map((inp) => (
                      <div key={inp.name} className="font-mono text-xs">
                        {inp.name}
                        {inp.default !== undefined && <span className="text-neutral-500"> = {inp.default}</span>}
                        {inp.description && <span className="text-neutral-500"> — {inp.description}</span>}
                      </div>
                    ))}
                  </div>
                </Field>
              )}
              {wf.spec.requirements?.tools && wf.spec.requirements.tools.length > 0 && (
                <Field label="Required tools">{wf.spec.requirements.tools.map((t) => t.name).join(', ')}</Field>
              )}
            </Card>

            <Card title={`Jobs (${wf.spec.jobs.length})`}>
              <div className="space-y-3">
                {wf.spec.jobs.map((job) => (
                  <JobView key={job.name} job={job} />
                ))}
              </div>
            </Card>
          </>
        )
      )}
    </div>
  )
}
