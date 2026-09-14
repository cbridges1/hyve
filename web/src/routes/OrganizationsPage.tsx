import { useState, type FormEvent } from 'react'
import { useSyncExternalStore } from 'react'
import { Card, EmptyState } from '../components/Card'
import { ChevronDownIcon, ChevronRightIcon } from '../components/icons'
import { EnvironmentsSection } from '../components/EnvironmentsManager'
import { organizationsApi } from '../lib/api/organizations'
import { ApiError } from '../lib/api/client'
import type { Organization } from '../lib/api/types'
import { useApi } from '../lib/useApi'
import { getOrganizationsVersion, invalidateOrganizations, subscribe as subscribeOrganizations } from '../lib/organizationsStore'

// Mirrors internal/api's validateOrganizationName — client-side so a
// reserved name is rejected before a round trip, not instead of the
// server-side check (a direct API/CLI caller still hits that). "hyve-
// system" is hardcoded here (unlike the server's own check against its
// actual configured s.Namespace) since the console has no other way to
// know the control plane's namespace short of asking the API for it —
// every real deployment uses this name by convention anyway.
function reservedOrganizationNameError(name: string): string | null {
  if (name.toLowerCase() === 'hyve-system') {
    return `"${name}" is the control plane's own namespace — choose a different organization name`
  }
  const normalized = name.toLowerCase().trim().replace(/[\s-]+/g, '-')
  if (normalized === 'control-plane') {
    return `"${name}" is reserved for the control plane — choose a different organization name`
  }
  return null
}

function CreateOrganizationForm({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState('')
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setMessage(null)
    const reserved = reservedOrganizationNameError(name)
    if (reserved) {
      setError(reserved)
      return
    }
    setSubmitting(true)
    try {
      // Always created on this install's own home cluster — registering a
      // reconciling cluster for it is entirely self-service, this
      // organization's own admin (or a superadmin "Viewing" it) doing it
      // from its own Reconciling cluster page, not something bundled into
      // creation here.
      await organizationsApi.create(name)
      setMessage(`"${name}" is ready.`)
      setName('')
      invalidateOrganizations()
      onCreated()
    } catch (err) {
      // handleCreateOrganization's steps are each idempotent — a
      // re-submit of an existing name only fails if something about it
      // genuinely conflicts, not merely because it already exists.
      setError(err instanceof ApiError ? err.message : 'Failed to create organization')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card title="Create organization">
      <p className="mb-3 text-xs text-neutral-500">
        Turns a name into a real tenant: its own namespace, RBAC scaffolding, and datastore record. Re-submitting an
        existing name is safe — each step only fills in what's missing. Assign a reconciling cluster to it afterward
        from its own Reconciling cluster page.
      </p>
      <form onSubmit={onSubmit} className="flex flex-wrap gap-2">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="organization name"
          required
          className="min-w-0 flex-1 rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800"
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

function OrganizationRow({ org }: { org: Organization }) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div>
      <button
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
      >
        <span className="flex items-center gap-2">
          {expanded ? <ChevronDownIcon width={14} height={14} /> : <ChevronRightIcon width={14} height={14} />}
          <span className="font-medium text-neutral-900 dark:text-neutral-100">{org.name}</span>
          {org.reconcilingCluster && (
            <span className="rounded-full border border-neutral-200 px-2 py-0.5 text-xs text-neutral-500 dark:border-neutral-700 dark:text-neutral-400">
              {org.reconcilingCluster}
            </span>
          )}
          {org.migrating && (
            <span className="rounded-full border border-amber-300 px-2 py-0.5 text-xs text-amber-700 dark:border-amber-700 dark:text-amber-400">
              migrating
            </span>
          )}
        </span>
        <span className="font-mono text-xs text-neutral-500 dark:text-neutral-500">{org.namespace}</span>
      </button>
      {expanded && (
        <div className="space-y-4 border-t border-neutral-100 px-4 py-3 dark:border-neutral-800">
          <EnvironmentsSection orgName={org.name} />
        </div>
      )}
    </div>
  )
}

export function OrganizationsPage() {
  const orgsVersion = useSyncExternalStore(subscribeOrganizations, getOrganizationsVersion)
  const { data: organizations, loading, error, reload } = useApi(() => organizationsApi.list(), [orgsVersion])

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Organizations</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          Every tenant on this install — superadmin-only. Log in with <code>--org &lt;name&gt;</code> to reach one.
          Click a row to manage its environments. Assign a reconciling cluster to one by "Viewing" it, then its own
          Reconciling cluster page.
        </p>
      </div>

      <CreateOrganizationForm onCreated={reload} />

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {organizations?.length === 0 && <EmptyState>No organizations yet.</EmptyState>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {organizations?.map((org) => (
            <OrganizationRow key={org.name} org={org} />
          ))}
        </div>
      </div>
    </div>
  )
}
