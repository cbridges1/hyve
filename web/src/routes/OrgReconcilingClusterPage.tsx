import { useState, type FormEvent } from 'react'
import { Card, Field } from '../components/Card'
import { Modal } from '../components/Modal'
import { AdminOnly } from '../components/RoleGate'
import { YamlEditor } from '../components/YamlEditor'
import { organizationsApi } from '../lib/api/organizations'
import { ApiError } from '../lib/api/client'
import type { OrgReconcilingCluster, ReconcilingClusterOwnership } from '../lib/api/types'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'
import { useActAs } from '../lib/useActAs'
import { useWhoami } from '../lib/useWhoami'

const primaryButton =
  'rounded-lg bg-neutral-900 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200'
const secondaryButton =
  'rounded-lg border border-neutral-300 px-3 py-1.5 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-100 disabled:opacity-50 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800'
const dangerButton =
  'rounded-lg border border-red-300 px-3 py-1.5 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/40'
const inputClass =
  'w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 text-sm dark:border-neutral-700 dark:bg-neutral-800'

function errorMessage(err: unknown, fallback: string) {
  return err instanceof ApiError ? err.message : fallback
}

function ReachableBadge({ reachable }: { reachable?: boolean }) {
  if (reachable === undefined) {
    return <span className="text-xs text-neutral-400 dark:text-neutral-600">not checked yet</span>
  }
  return reachable ? (
    <span className="text-xs font-medium text-green-700 dark:text-green-400">reachable</span>
  ) : (
    <span className="text-xs font-medium text-red-600 dark:text-red-400">unreachable</span>
  )
}

// Only a superadmin-assigned pool cluster gets a badge — an organization's
// own clusters are the normal case and need no label.
function OwnershipBadge({ ownership }: { ownership: ReconcilingClusterOwnership }) {
  if (ownership !== 'pool') return null
  return (
    <span className="rounded-full border border-neutral-300 px-2 py-0.5 text-xs text-neutral-600 dark:border-neutral-700 dark:text-neutral-400">
      assigned by a superadmin
    </span>
  )
}

// ClusterRow is one cluster the organization can switch to. Switching
// migrates every resource onto the target first, so it's confirmed.
function ClusterRow({ orgName, cluster, onChanged }: { orgName: string; cluster: OrgReconcilingCluster; onChanged: () => void }) {
  const confirm = useConfirm()
  const [busy, setBusy] = useState<'switch' | 'remove' | 'rotate' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [rotating, setRotating] = useState(false)
  const [kubeconfig, setKubeconfig] = useState('')

  async function onSwitch() {
    const ok = await confirm({
      title: `Switch to ${cluster.name}?`,
      message:
        "Copies every cluster, template, workflow and resource onto it, then moves the organization over. Requests against the organization return 423 until that finishes. A failed copy leaves it where it is.",
      confirmLabel: 'Switch',
    })
    if (!ok) return
    setError(null)
    setBusy('switch')
    try {
      await organizationsApi.useReconcilingCluster(orgName, cluster.name)
      onChanged()
    } catch (err) {
      setError(errorMessage(err, 'Failed to switch reconciling cluster'))
    } finally {
      setBusy(null)
    }
  }

  async function onRemove() {
    const ok = await confirm({
      title: `Remove ${cluster.name}?`,
      message: "Permanently deletes the stored kubeconfig. You'd need to paste it again to use this cluster later.",
      confirmLabel: 'Remove',
      danger: true,
    })
    if (!ok) return
    setError(null)
    setBusy('remove')
    try {
      await organizationsApi.removeReconcilingCluster(orgName, cluster.name)
      onChanged()
    } catch (err) {
      setError(errorMessage(err, 'Failed to remove reconciling cluster'))
    } finally {
      setBusy(null)
    }
  }

  async function onRotate(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy('rotate')
    try {
      await organizationsApi.addReconcilingCluster(orgName, cluster.name, kubeconfig)
      setKubeconfig('')
      setRotating(false)
      onChanged()
    } catch (err) {
      setError(errorMessage(err, 'Failed to replace kubeconfig'))
    } finally {
      setBusy(null)
    }
  }

  const own = cluster.ownership === 'organization'

  return (
    <li className="border-t border-neutral-100 py-3 first:border-t-0 dark:border-neutral-800/70">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-neutral-900 dark:text-neutral-100">{cluster.name}</span>
            <OwnershipBadge ownership={cluster.ownership} />
            {cluster.active && (
              <span className="rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800 dark:bg-green-950 dark:text-green-300">
                active
              </span>
            )}
          </div>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-neutral-500">
            <ReachableBadge reachable={cluster.reachable} />
            {cluster.kubernetesVersion && <span>{cluster.kubernetesVersion}</span>}
            {cluster.server && <span className="truncate font-mono">{cluster.server}</span>}
          </div>
          {cluster.lastError && (
            <div className="mt-0.5 max-w-md truncate text-xs text-red-600 dark:text-red-400" title={cluster.lastError}>
              {cluster.lastError}
            </div>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!cluster.active && (
            <button type="button" onClick={onSwitch} disabled={busy !== null} className={secondaryButton}>
              {busy === 'switch' ? 'Switching…' : 'Switch to'}
            </button>
          )}
          {own && (
            <button type="button" onClick={() => setRotating((v) => !v)} disabled={busy !== null} className={secondaryButton}>
              Replace kubeconfig
            </button>
          )}
          {own && !cluster.active && (
            <button type="button" onClick={onRemove} disabled={busy !== null} className={dangerButton}>
              {busy === 'remove' ? 'Removing…' : 'Remove'}
            </button>
          )}
        </div>
      </div>
      {rotating && (
        <form onSubmit={onRotate} className="mt-2 space-y-2">
          <textarea
            value={kubeconfig}
            onChange={(e) => setKubeconfig(e.target.value)}
            placeholder={`new kubeconfig YAML for ${cluster.name}`}
            required
            rows={5}
            className={`${inputClass} font-mono text-xs`}
          />
          <button type="submit" disabled={!kubeconfig || busy !== null} className={primaryButton}>
            {busy === 'rotate' ? 'Saving…' : 'Save'}
          </button>
        </form>
      )}
      {error && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{error}</p>}
    </li>
  )
}

// NewReconcilingClusterForm follows the same create flow as every other
// resource page: a header button that opens a Modal. Storing a cluster
// doesn't switch to it unless "Switch to it now" is ticked.
function NewReconcilingClusterForm({ orgName, onCreated }: { orgName: string; onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [kubeconfig, setKubeconfig] = useState('')
  const [switchNow, setSwitchNow] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState<'store' | 'switch' | null>(null)

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} disabled={!orgName} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
        New reconciling cluster
      </button>
    )
  }

  function reset() {
    setOpen(false)
    setName('')
    setKubeconfig('')
    setSwitchNow(false)
    setError(null)
  }

  async function submit() {
    setError(null)
    setSubmitting('store')
    try {
      const stored = await organizationsApi.addReconcilingCluster(orgName, name, kubeconfig)
      if (switchNow && !stored.active) {
        setSubmitting('switch')
        await organizationsApi.useReconcilingCluster(orgName, stored.name)
      }
      reset()
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create reconciling cluster')
      // The cluster may have been stored even if the switch failed.
      onCreated()
    } finally {
      setSubmitting(null)
    }
  }

  const validName = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)

  return (
    <Modal title="New reconciling cluster" onClose={reset}>
      <div className="mb-3">
        <label className="text-sm">
          <span className="mb-1 block text-neutral-600 dark:text-neutral-400">Name</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. k3s-home"
            className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 dark:border-neutral-700 dark:bg-neutral-800"
          />
        </label>
        {name && !validName && (
          <p className="mt-1 text-xs text-red-600 dark:text-red-400">Lowercase letters, digits and dashes only.</p>
        )}
      </div>
      <div className="mb-1">
        <YamlEditor value={kubeconfig} onChange={setKubeconfig} rows={8} label="Kubeconfig (YAML)" placeholder="paste the cluster's kubeconfig, or drop the file here" />
      </div>
      <p className="mb-3 text-xs text-neutral-500">Never shown again once saved. Using an existing name replaces its kubeconfig.</p>
      <label className="mb-3 flex items-center gap-2 text-sm text-neutral-700 dark:text-neutral-300">
        <input type="checkbox" checked={switchNow} onChange={(e) => setSwitchNow(e.target.checked)} />
        Switch to it now (moves every resource onto it first)
      </label>
      {error && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
      <div className="flex justify-end gap-2">
        <button type="button" onClick={reset} className="rounded-lg px-3.5 py-2 text-sm text-neutral-600 transition-colors hover:bg-neutral-100 dark:text-neutral-400 dark:hover:bg-neutral-700">
          Cancel
        </button>
        <button type="button" disabled={!validName || !kubeconfig || submitting !== null} onClick={submit} className="rounded-lg bg-neutral-900 px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200">
          {submitting === 'switch' ? 'Switching…' : submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  )
}

function MoveHomeControl({ orgName, onChanged }: { orgName: string; onChanged: () => void }) {
  const confirm = useConfirm()
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onMove() {
    const ok = await confirm({
      title: 'Move back to the home cluster?',
      message: "Copies every resource onto this install's own home cluster, then moves the organization there. Your stored clusters are kept.",
      confirmLabel: 'Move',
    })
    if (!ok) return
    setError(null)
    setSubmitting(true)
    try {
      await organizationsApi.useReconcilingCluster(orgName, null)
      onChanged()
    } catch (err) {
      setError(errorMessage(err, 'Failed to move back to the home cluster'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div>
      <button type="button" onClick={onMove} disabled={submitting} className={secondaryButton}>
        {submitting ? 'Moving…' : 'Move back to the home cluster'}
      </button>
      {error && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}

// OrgReconcilingClusterPage is this console's reconciling-cluster page —
// reachable by an ordinary admin for their own organization, or a
// superadmin currently "Viewing" one, including the control plane's own
// default view (Server.TenantNamespace resolves that to the control
// plane's own organization). An organization can store several clusters of
// its own and switch between them by name.
export function OrgReconcilingClusterPage() {
  const who = useWhoami().data
  const [actAs] = useActAs()
  const orgName = actAs ?? who?.organization ?? who?.namespace ?? ''
  const status = useApi(
    () => (orgName ? organizationsApi.getOwnReconcilingCluster(orgName) : Promise.resolve(null)),
    [orgName],
  )
  const clusters = useApi(
    () => (orgName ? organizationsApi.listReconcilingClusters(orgName) : Promise.resolve(null)),
    [orgName],
  )
  const reload = () => {
    status.reload()
    clusters.reload()
  }
  const placement = status.data

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Reconciling cluster</h1>
          <p className="mt-0.5 text-sm text-neutral-500">
            The Kubernetes cluster this organization's resources live on. Keep several on hand and switch between them.
          </p>
        </div>
        <AdminOnly>
          <NewReconcilingClusterForm orgName={orgName} onCreated={reload} />
        </AdminOnly>
      </div>

      {status.loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {status.error && <p className="text-sm text-red-600 dark:text-red-400">{status.error}</p>}

      {placement?.migrating ? (
        <Card>
          <p className="text-sm text-amber-700 dark:text-amber-400">
            Switch in progress — every request against this organization's clusters/templates/workflows/resources
            returns 423 until it completes. This page will show the new placement once it finishes.
          </p>
        </Card>
      ) : (
        placement && (
          <>
            <Card title="Current placement">
              {placement.onHomeCluster ? (
                <p className="text-sm text-neutral-700 dark:text-neutral-300">On this install's own home cluster.</p>
              ) : (
                <div>
                  <Field label="Name">
                    <span className="mr-2">{placement.name}</span>
                    {placement.ownership && <OwnershipBadge ownership={placement.ownership} />}
                  </Field>
                  {placement.server && (
                    <Field label="Server">
                      <span className="font-mono text-xs">{placement.server}</span>
                    </Field>
                  )}
                  <Field label="Kubernetes">{placement.kubernetesVersion ?? 'unknown — no successful check yet'}</Field>
                  <Field label="Reachability">
                    <div className="flex items-center gap-2">
                      <ReachableBadge reachable={placement.reachable} />
                      {placement.lastCheckedAt && (
                        <span className="text-xs text-neutral-400 dark:text-neutral-600">
                          checked {new Date(placement.lastCheckedAt).toLocaleString()}
                        </span>
                      )}
                    </div>
                  </Field>
                </div>
              )}
              {!placement.onHomeCluster && (
                <div className="mt-3">
                  <MoveHomeControl orgName={orgName} onChanged={reload} />
                </div>
              )}
            </Card>

            <Card title="Available clusters">
              {clusters.error && <p className="text-sm text-red-600 dark:text-red-400">{clusters.error}</p>}
              {clusters.data && clusters.data.length === 0 ? (
                <p className="text-sm text-neutral-500">None yet — use New reconciling cluster to add one.</p>
              ) : (
                <ul>
                  {clusters.data?.map((c) => (
                    <ClusterRow key={`${c.ownership}/${c.name}`} orgName={orgName} cluster={c} onChanged={reload} />
                  ))}
                </ul>
              )}
            </Card>
          </>
        )
      )}
    </div>
  )
}
