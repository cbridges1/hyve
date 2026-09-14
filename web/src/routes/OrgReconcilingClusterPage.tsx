import { useState, type FormEvent } from 'react'
import { Card } from '../components/Card'
import { organizationsApi } from '../lib/api/organizations'
import { ApiError } from '../lib/api/client'
import { useConfirm } from '../lib/confirm'
import { useApi } from '../lib/useApi'
import { useActAs } from '../lib/useActAs'
import { useWhoami } from '../lib/useWhoami'

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

function SetKubeconfigForm({ orgName, hasOwnCluster, onChanged }: { orgName: string; hasOwnCluster: boolean; onChanged: () => void }) {
  const [kubeconfig, setKubeconfig] = useState('')
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setMessage(null)
    setSubmitting(true)
    try {
      await organizationsApi.setOwnReconcilingCluster(orgName, kubeconfig)
      setMessage(hasOwnCluster ? 'Kubeconfig replaced.' : "Reconciling cluster registered — migrating your organization's resources onto it now.")
      setKubeconfig('')
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to update reconciling cluster')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card title={hasOwnCluster ? 'Edit / replace kubeconfig' : 'Register a dedicated reconciling cluster'}>
      <p className="mb-3 text-xs text-neutral-500">
        {hasOwnCluster
          ? "Replace the stored kubeconfig for this organization's own reconciling cluster — a real, expected operational need (kubeconfigs expire/get regenerated, or point the same name at genuinely different cluster credentials). This does not move your resources anywhere; they stay wherever this cluster's own server: actually points."
          : "A physical Kubernetes cluster your organization's own resources will live on, instead of this install's shared home cluster. Submitting this migrates everything you currently have onto it."}
      </p>
      <form onSubmit={onSubmit} className="space-y-2">
        <textarea
          value={kubeconfig}
          onChange={(e) => setKubeconfig(e.target.value)}
          placeholder="paste your cluster's kubeconfig YAML here"
          required
          rows={6}
          className="w-full rounded-lg border border-neutral-300 px-2.5 py-1.5 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-800"
        />
        <p className="text-xs text-neutral-500">Never echoed back by any endpoint once submitted.</p>
        <button
          type="submit"
          disabled={!kubeconfig || submitting}
          className="rounded-lg bg-neutral-900 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-neutral-800 disabled:opacity-50 dark:bg-white dark:text-neutral-900 dark:hover:bg-neutral-200"
        >
          {submitting ? 'Saving…' : hasOwnCluster ? 'Replace' : 'Register & migrate'}
        </button>
      </form>
      {message && <p className="mt-2 text-sm text-green-700 dark:text-green-400">{message}</p>}
      {error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{error}</p>}
    </Card>
  )
}

function MoveHomeControl({ orgName, onChanged }: { orgName: string; onChanged: () => void }) {
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onMove() {
    setError(null)
    setSubmitting(true)
    try {
      await organizationsApi.setOwnReconcilingCluster(orgName, '')
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to move back to the home cluster')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div>
      <button
        type="button"
        onClick={onMove}
        disabled={submitting}
        className="rounded-lg border border-neutral-300 px-3 py-1.5 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-100 disabled:opacity-50 dark:border-neutral-700 dark:text-neutral-300 dark:hover:bg-neutral-800"
      >
        {submitting ? 'Moving…' : 'Move back to the home cluster'}
      </button>
      {error && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}

// RemoveReconcilingClusterControl is the "remove" half of the self-service
// register/edit/remove trio — distinct from MoveHomeControl above (which
// only detaches, keeping the stored kubeconfig around for later reuse):
// this permanently deletes it via DELETE
// /organizations/{name}/reconciling-cluster, migrating back home first.
// Irreversible, so gated behind the same confirm() dialog every other
// destructive action in this console uses.
function RemoveReconcilingClusterControl({ orgName, onChanged }: { orgName: string; onChanged: () => void }) {
  const confirm = useConfirm()
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onRemove() {
    const ok = await confirm({
      title: 'Remove this reconciling cluster?',
      message:
        'Permanently deletes the stored kubeconfig, migrating your resources back to the home cluster first. This cannot be undone — you would need to paste the kubeconfig again to use it, or a different one, later.',
      confirmLabel: 'Remove reconciling cluster',
      danger: true,
    })
    if (!ok) return
    setError(null)
    setSubmitting(true)
    try {
      await organizationsApi.deleteOwnReconcilingCluster(orgName)
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to remove reconciling cluster')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div>
      <button
        type="button"
        onClick={onRemove}
        disabled={submitting}
        className="rounded-lg border border-red-300 px-3 py-1.5 text-sm font-medium text-red-600 transition-colors hover:bg-red-50 disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/40"
      >
        {submitting ? 'Removing…' : 'Remove reconciling cluster'}
      </button>
      {error && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}

// OrgReconcilingClusterPage is this console's one and only reconciling-
// cluster page — reachable by an ordinary admin for their own
// organization, or a superadmin currently "Viewing" one, including the
// control plane's own default view: Server.TenantNamespace already
// resolves "Viewing: Control plane" to the control plane's own namespace
// (hyve-system) for every request, so orgName below lands on exactly that
// organization the same way it would for any tenant, with no special
// control-plane-wide registry page layered on top — the control plane is
// only ever concerned with its own reconciling-cluster configuration here,
// never any other organization's. Registration, editing/replacing the
// stored kubeconfig, and permanently removing it are all self-service via
// GET/PUT/DELETE /organizations/{name}/reconciling-cluster — a superadmin
// "Viewing" an organization acts exactly like its own admin would, not
// through some other, superadmin-only path.
export function OrgReconcilingClusterPage() {
  const who = useWhoami().data
  const [actAs] = useActAs()
  const orgName = actAs ?? who?.namespace ?? ''
  const { data: status, loading, error, reload } = useApi(
    () => (orgName ? organizationsApi.getOwnReconcilingCluster(orgName) : Promise.resolve(null)),
    [orgName],
  )

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100">Reconciling cluster</h1>
        <p className="mt-0.5 text-sm text-neutral-500">
          The physical Kubernetes cluster this organization's own resources currently live on.
        </p>
      </div>

      {loading && <p className="text-sm text-neutral-500">Loading…</p>}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      {status?.migrating ? (
        <Card>
          <p className="text-sm text-amber-700 dark:text-amber-400">
            Migration in progress — every request against this organization's own clusters/templates/workflows/
            resources returns 423 until it completes. This page will reflect the new placement once it finishes.
          </p>
        </Card>
      ) : (
        status && (
          <>
            <Card title="Current placement">
              {status.onHomeCluster ? (
                <p className="text-sm text-neutral-700 dark:text-neutral-300">On this install's own shared home cluster.</p>
              ) : (
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="font-medium text-neutral-900 dark:text-neutral-100">{status.name}</div>
                    {status.lastError && (
                      <div className="mt-0.5 max-w-md truncate text-xs text-red-600 dark:text-red-400" title={status.lastError}>
                        {status.lastError}
                      </div>
                    )}
                  </div>
                  <div className="text-right">
                    <ReachableBadge reachable={status.reachable} />
                    {status.lastCheckedAt && (
                      <div className="mt-0.5 text-xs text-neutral-400 dark:text-neutral-600">
                        checked {new Date(status.lastCheckedAt).toLocaleString()}
                      </div>
                    )}
                  </div>
                </div>
              )}
            </Card>

            <SetKubeconfigForm orgName={orgName} hasOwnCluster={!status.onHomeCluster} onChanged={reload} />
            {!status.onHomeCluster && (
              <div className="flex items-center gap-3">
                <MoveHomeControl orgName={orgName} onChanged={reload} />
                <RemoveReconcilingClusterControl orgName={orgName} onChanged={reload} />
              </div>
            )}
          </>
        )
      )}
    </div>
  )
}
