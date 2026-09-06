import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { BackLink, Card, CodeBlock, Field } from '../components/Card'
import { RefStatusBadge } from '../components/ConditionBadge'
import { AdminOnly } from '../components/RoleGate'
import { SpecEditor } from '../components/SpecEditor'
import { ApiError } from '../lib/api/client'
import { resourcesApi } from '../lib/api/resources'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'

export function ResourceDetailPage() {
  const { name = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { data: res, loading, error, reload } = useApi(() => resourcesApi.get(name), [name])
  const [deleteError, setDeleteError] = useState<string | null>(null)

  async function onDelete() {
    const ok = await confirm({
      title: `Delete resource "${name}"?`,
      message: 'This cannot be undone. Any cluster still referencing this resource by name will fail to resolve it on its next reconcile.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    setDeleteError(null)
    try {
      await resourcesApi.delete(name)
      navigate('/resources')
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : 'Failed to delete resource')
    }
  }

  if (loading) return <p className="text-sm text-neutral-500">Loading…</p>
  if (error || !res) return <p className="text-sm text-red-600 dark:text-red-400">{error ?? 'Not found'}</p>

  return (
    <div className="space-y-4">
      <BackLink to="/resources" label="Resources" />

      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <h1 className="truncate text-lg font-semibold text-neutral-900 dark:text-neutral-100">{res.name}</h1>
          {res.refStatus && <RefStatusBadge resolved={res.refStatus.resolved} error={res.refStatus.error} />}
        </div>
        {!res.refStatus && (
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

      {res.refStatus ? (
        <Card title="Git reference">
          <Field label="Source">{res.refStatus.source}</Field>
          <Field label="Resolved">{res.refStatus.resolved ? 'yes' : 'no'}</Field>
          {res.refStatus.rawVersion && <Field label="Requested version">{res.refStatus.rawVersion}</Field>}
          {res.refStatus.sha256 && <Field label="SHA256">{res.refStatus.sha256}</Field>}
          {res.refStatus.error && (
            <Field label="Error">
              <span className="text-red-600 dark:text-red-400">{res.refStatus.error}</span>
            </Field>
          )}
          <p className="mt-3 text-xs text-neutral-500">
            This is a git-ref-backed resource, mirrored read-only from the controller — its manifest lives in the
            source repository above, not here.
          </p>
        </Card>
      ) : (
        res.spec && (
          <>
            <Card title="Manifest">
              <CodeBlock>{res.spec.manifest}</CodeBlock>
            </Card>
            <AdminOnly>
              <SpecEditor spec={res.spec} onSave={(spec) => resourcesApi.update(name, spec).then(reload)} />
            </AdminOnly>
          </>
        )
      )}
    </div>
  )
}
