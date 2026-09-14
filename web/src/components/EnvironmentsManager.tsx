import { useState, type FormEvent } from 'react'
import { organizationsApi } from '../lib/api/organizations'
import { ApiError } from '../lib/api/client'
import type { OrganizationEnvironment } from '../lib/api/types'
import { useApi } from '../lib/useApi'

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
            <li
              key={env.name}
              className="rounded-full border border-neutral-200 px-2 py-0.5 text-xs text-neutral-700 dark:border-neutral-700 dark:text-neutral-300"
            >
              {env.name}
            </li>
          ))}
        </ul>
      )}
      <CreateEnvironmentForm orgName={orgName} onCreated={reload} />
    </div>
  )
}
