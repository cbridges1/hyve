// Environments are namespaces labelled hyve.io/environment, as
// `hyve env create` makes them; clusters live in them.
import { useState, type FormEvent } from 'react'
import { TextField } from '../components/form'
import { Modal } from '../components/Modal'
import { ConfirmButton, EmptyState, ErrorBanner, Loading, PageHeader, Pill } from '../components/ui'
import { ViewDialog } from '../components/ViewDialog'
import { errorMessage } from '../lib/api'
import { useLoad } from '../lib/hooks'
import { newObject, remove, save, type Obj } from '../lib/kube'
import { LABELS, TYPES } from '../lib/types'

const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

export default function Environments() {
  const { data, error: loadError, loading, reload } = useLoad(async (r) => {
    const [nss, clusters] = await Promise.all([
      r.findAll(TYPES.NAMESPACE, { force: true }),
      r.findAll(TYPES.CLUSTER, { force: true }).catch(() => [] as Obj[]),
    ])
    const envs = nss
      .filter((n) => n.metadata.labels?.[LABELS.ENVIRONMENT] !== undefined)
      .sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))
    const counts: Record<string, number> = {}
    clusters.forEach((c) => {
      const ns = c.metadata.namespace || ''
      counts[ns] = (counts[ns] || 0) + 1
    })
    return { envs, counts }
  }, [])
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [viewing, setViewing] = useState<Obj | null>(null)

  async function create(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (!NAME_RE.test(name) || name.length > 63) {
      setError('Lowercase letters, digits and dashes')
      return
    }
    setBusy(true)
    try {
      await save(TYPES.NAMESPACE, newObject(TYPES.NAMESPACE, { metadata: { name, labels: { [LABELS.ENVIRONMENT]: 'true', [LABELS.MANAGED_BY]: 'hyve' } } }))
      setCreating(false)
      setName('')
      await reload(true)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function removeEnv(env: Obj) {
    setError('')
    try {
      await remove(TYPES.NAMESPACE, env)
      await reload(true)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  if (loading) return <Loading />
  const envs = data?.envs || []
  const counts = data?.counts || {}
  return (
    <div>
      <PageHeader title="Environments">
        <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
          New environment
        </button>
      </PageHeader>
      <ErrorBanner error={(!creating && error) || loadError} />
      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        {!envs.length && <EmptyState>No environments yet.</EmptyState>}
        <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {envs.map((env) => (
            <div key={env.metadata.name} className="flex items-center justify-between gap-3 px-4 py-3">
              <div className="flex items-center gap-2">
                <span className="font-medium text-neutral-900 dark:text-neutral-100">{env.metadata.name}</span>
                <Pill label={`${counts[env.metadata.name] || 0} clusters`} />
              </div>
              <div className="flex shrink-0 items-center gap-1">
                <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(env)}>
                  View
                </button>
                {!counts[env.metadata.name] ? (
                  <ConfirmButton label="Delete" confirm={`Delete ${env.metadata.name}?`} onConfirm={() => removeEnv(env)} />
                ) : (
                  <span className="text-xs text-neutral-400">Delete its clusters first</span>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
      {creating && (
        <Modal title="New environment" onClose={() => setCreating(false)}>
          <form onSubmit={create}>
            <TextField
              className="mb-3"
              label="Name"
              value={name}
              onChange={setName}
              autoFocus
              placeholder="dev"
              help="A namespace; clusters created in it are this environment's."
            />
            {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className="btn btn-tertiary" onClick={() => setCreating(false)}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={!name || busy}>
                {busy ? 'Creating…' : 'Create'}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {viewing && (
        <ViewDialog title={`Environment ${viewing.metadata.name}`} sections={[{ label: 'Namespace', object: viewing }]} onClose={() => setViewing(null)} />
      )}
    </div>
  )
}
