import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AdminOnly } from '../components/RoleGate'
import { Modal } from '../components/Modal'
import { ApiError } from '../lib/api/client'
import { accessMethodsApi } from '../lib/api/accessMethods'
import { useApi } from '../lib/useApi'

const inputClass = 'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

function NewAccessMethodForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [serverURL, setServerURL] = useState('')
  const [authKind, setAuthKind] = useState<'inline' | 'driver'>('inline')
  const [inlineAuth, setInlineAuth] = useState('')
  const [requiredEnv, setRequiredEnv] = useState('')
  const [driverSource, setDriverSource] = useState('')
  const [driverVersion, setDriverVersion] = useState('')
  const [runnerImage, setRunnerImage] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
      >
        New access method
      </button>
    )
  }

  async function submit() {
    setError(null)
    setSubmitting(true)
    try {
      await accessMethodsApi.create({
        name,
        spec: {
          serverURL,
          ...(authKind === 'inline'
            ? {
                inlineAuth,
                requiredEnv: requiredEnv
                  .split(',')
                  .map((s) => s.trim())
                  .filter(Boolean),
              }
            : { driver: { source: driverSource, version: driverVersion || undefined } }),
          ...(runnerImage ? { runner: { image: runnerImage } } : {}),
        },
      })
      setOpen(false)
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create access method')
    } finally {
      setSubmitting(false)
    }
  }

  const canSubmit = !!name && !!serverURL && (authKind === 'inline' ? !!inlineAuth : !!driverSource)

  return (
    <Modal title="New access method" onClose={() => setOpen(false)}>
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </label>
      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Server URL</span>
        <input
          value={serverURL}
          onChange={(e) => setServerURL(e.target.value)}
          placeholder="https://rancher.example.com"
          className={inputClass}
        />
      </label>

      <div className="mb-3 flex gap-4 text-sm">
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={authKind === 'inline'} onChange={() => setAuthKind('inline')} />
          Inline auth script
        </label>
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={authKind === 'driver'} onChange={() => setAuthKind('driver')} />
          Driver module
        </label>
      </div>

      {authKind === 'inline' ? (
        <>
          <label className="mb-3 block text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Auth script</span>
            <textarea
              value={inlineAuth}
              onChange={(e) => setInlineAuth(e.target.value)}
              rows={6}
              placeholder={'set -e\ncurl ... > "$KUBECONFIG"'}
              className={`${inputClass} font-mono text-xs`}
            />
          </label>
          <label className="mb-3 block text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Required credentials (comma-separated)</span>
            <input
              value={requiredEnv}
              onChange={(e) => setRequiredEnv(e.target.value)}
              placeholder="RANCHER_USERNAME, RANCHER_PASSWORD"
              className={inputClass}
            />
          </label>
        </>
      ) : (
        <div className="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Driver source</span>
            <input value={driverSource} onChange={(e) => setDriverSource(e.target.value)} className={inputClass} />
          </label>
          <label className="text-sm">
            <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Driver version</span>
            <input value={driverVersion} onChange={(e) => setDriverVersion(e.target.value)} placeholder="main" className={inputClass} />
          </label>
        </div>
      )}

      <label className="mb-3 block text-sm">
        <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Runner image (optional)</span>
        <input value={runnerImage} onChange={(e) => setRunnerImage(e.target.value)} className={inputClass} />
      </label>

      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={!canSubmit || submitting}
          onClick={submit}
          className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  )
}

export function AccessMethodsPage() {
  const navigate = useNavigate()
  const { data: methods, loading, error, reload } = useApi(() => accessMethodsApi.list())

  return (
    <div>
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Access methods</h1>
          <p className="mt-0.5 text-sm text-neutral-500">
            Server-side auth brokers (Rancher, etc.) a cluster can reference to mint a kubeconfig via a third-party
            identity provider — see <code>hyve cluster auth</code>.
          </p>
        </div>
        <AdminOnly>
          <NewAccessMethodForm onCreated={reload} />
        </AdminOnly>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {methods?.length === 0 && <p className="p-6 text-center text-sm text-neutral-500">No access methods yet.</p>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {methods?.map((m) => (
            <div
              key={m.name}
              onClick={() => navigate(`/access-methods/${encodeURIComponent(m.name)}`)}
              className="flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-neutral-50 dark:hover:bg-neutral-800/50"
            >
              <div className="min-w-0">
                <div className="font-medium text-neutral-900 dark:text-neutral-100">{m.name}</div>
                <div className="truncate text-xs text-neutral-500 dark:text-neutral-500">{m.spec.serverURL}</div>
              </div>
              <span className="shrink-0 rounded-full bg-neutral-100 px-2 py-0.5 text-xs text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
                {m.spec.inlineAuth ? 'inline auth' : m.spec.driver?.source ? 'driver' : 'unconfigured'}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
