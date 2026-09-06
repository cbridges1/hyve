import { useState, type FormEvent } from 'react'
import { Card, EmptyState } from '../components/Card'
import { environmentsApi } from '../lib/api/environments'
import { ApiError } from '../lib/api/client'
import { useApi } from '../lib/useApi'

function CreateEnvironmentForm({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState('')
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setMessage(null)
    setSubmitting(true)
    try {
      await environmentsApi.create(name)
      setMessage(`"${name}" is ready.`)
      setName('')
      onCreated()
    } catch (err) {
      // handleCreateEnvironment's three steps are each idempotent — a
      // re-submit of an existing name only fails if something about it
      // genuinely conflicts, not merely because it already exists.
      setError(err instanceof ApiError ? err.message : 'Failed to create environment')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card title="Create environment">
      <p className="mb-3 text-xs text-neutral-500">
        Turns a name into a real tenant: its own namespace, RBAC scaffolding, and registry entry. Re-submitting an
        existing name is safe — each step only fills in what's missing.
      </p>
      <form onSubmit={onSubmit} className="flex gap-2">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="environment name"
          required
          className="flex-1 rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
        />
        <button
          type="submit"
          disabled={!name || submitting}
          className="shrink-0 rounded-lg bg-neutral-900 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </form>
      {message && <p className="mt-2 text-sm text-green-700 dark:text-green-400">{message}</p>}
      {error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>}
    </Card>
  )
}

export function EnvironmentsPage() {
  const { data: environments, loading, error, reload } = useApi(() => environmentsApi.list())

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Environments</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          Every tenant on this install — superadmin-only. Log in with <code>--org &lt;name&gt;</code> to reach one.
        </p>
      </div>

      <CreateEnvironmentForm onCreated={reload} />

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {environments?.length === 0 && <EmptyState>No environments yet.</EmptyState>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {environments?.map((env) => (
            <div key={env.name} className="flex items-center justify-between gap-3 px-4 py-3">
              <span className="font-medium text-neutral-900 dark:text-neutral-100">{env.name}</span>
              <span className="font-mono text-xs text-neutral-500 dark:text-neutral-500">{env.namespace}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
