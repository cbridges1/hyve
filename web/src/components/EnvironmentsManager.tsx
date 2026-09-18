import { useState, type FormEvent } from 'react'
import { organizationsApi } from '../lib/api/organizations'
import { ApiError } from '../lib/api/client'
import type { OrganizationEnvironment } from '../lib/api/types'
import { useApi } from '../lib/useApi'
import { useConfirm } from '../lib/confirm'

// Shared by OrganizationsPage (a superadmin managing any organization by
// name) and EnvironmentsPage (an admin or superadmin managing their own
// organization, resolved from their own session via whoami — see that
// page's own doc comment for why the same component works for both
// without knowing which caller it is).

function CreateEnvironmentForm({ orgName, onCreated }: { orgName: string; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await organizationsApi.createEnvironment(orgName, name)
      setName('')
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create environment')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={onSubmit} className="flex gap-2">
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="environment name (e.g. staging)"
        required
        className="min-w-0 flex-1 rounded-lg border border-neutral-300 px-2.5 py-1 text-xs dark:border-neutral-700 dark:bg-neutral-800"
      />
      <button
        type="submit"
        disabled={!name || submitting}
        className="shrink-0 rounded-lg border border-neutral-300 px-2.5 py-1 text-xs font-medium transition-colors hover:bg-neutral-100 disabled:opacity-50 dark:border-neutral-700 dark:hover:bg-neutral-800"
      >
        {submitting ? 'Adding…' : 'Add environment'}
      </button>
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
    </form>
  )
}

// DEFAULT_ENVIRONMENT_NAME mirrors orgdb.DefaultEnvironmentName — the one
// reserved environment every organization gets at creation, and the one
// unlabeled/legacy objects resolve to for display (see
// internal/api/environmentnaming.go's effectiveEnvironmentLabel). The
// backend refuses to delete it unconditionally, not just when it's the
// last remaining environment — deleting it while a peer survives would
// reintroduce that exact display ambiguity for anything still unlabeled.
const DEFAULT_ENVIRONMENT_NAME = 'default'

// EnvironmentPill renders one environment as a pill with its own delete
// control — a single environment left in an organization, or "default"
// specifically (see DEFAULT_ENVIRONMENT_NAME), has no delete button at all
// (canDelete=false): the backend refuses both cases anyway, so there's no
// point offering a control that can only ever fail. Renaming isn't offered
// here — an environment's name is baked into every object's own Kubernetes
// name, not just a label, so it isn't a simple rename the way an
// organization's own display name is.
function EnvironmentPill({
  env,
  orgName,
  canDelete,
  onDeleted,
}: {
  env: OrganizationEnvironment
  orgName: string
  canDelete: boolean
  onDeleted: () => void
}) {
  const confirm = useConfirm()
  const [error, setError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)

  async function onDelete() {
    const ok = await confirm({
      title: `Delete environment "${env.name}"?`,
      message:
        'Permanently removes this environment. Refused if it still has any clusters, templates, workflows, or resources — delete those first.',
      confirmLabel: 'Delete environment',
      danger: true,
    })
    if (!ok) return
    setError(null)
    setDeleting(true)
    try {
      await organizationsApi.deleteEnvironment(orgName, env.name)
      onDeleted()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete environment')
      setDeleting(false)
    }
  }

  return (
    <li className="flex items-center gap-1 rounded-full border border-neutral-200 py-0.5 pr-1 pl-2 text-xs text-neutral-700 dark:border-neutral-700 dark:text-neutral-300">
      {env.name}
      {canDelete && (
        <button
          type="button"
          onClick={onDelete}
          disabled={deleting}
          title={`Delete ${env.name}`}
          className="rounded-full px-1 text-neutral-400 transition-colors hover:bg-red-100 hover:text-red-600 disabled:opacity-50 dark:text-neutral-500 dark:hover:bg-red-950 dark:hover:text-red-400"
        >
          ×
        </button>
      )}
      {error && <span className="text-red-600 dark:text-red-400">{error}</span>}
    </li>
  )
}

export function EnvironmentsSection({ orgName }: { orgName: string }) {
  const { data: environments, loading, error, reload } = useApi<OrganizationEnvironment[]>(
    () => organizationsApi.listEnvironments(orgName),
    [orgName],
  )

  return (
    <div>
      <div className="mb-1 text-xs font-medium tracking-wide text-neutral-500 uppercase dark:text-neutral-500">
        Environments
      </div>
      {loading && <p className="text-xs text-neutral-500">Loading…</p>}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
      {environments && environments.length > 0 && (
        <ul className="mb-2 flex flex-wrap gap-1.5">
          {environments.map((env) => (
            <EnvironmentPill
              key={env.name}
              env={env}
              orgName={orgName}
              canDelete={environments.length > 1 && env.name !== DEFAULT_ENVIRONMENT_NAME}
              onDeleted={reload}
            />
          ))}
        </ul>
      )}
      <CreateEnvironmentForm orgName={orgName} onCreated={reload} />
    </div>
  )
}
