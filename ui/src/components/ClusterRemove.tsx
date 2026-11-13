// Taking a cluster away, behind its typed name: Delete (its provider
// deletes the cluster), Detach (an imported one; it keeps running) and
// Release (hyve stops managing one it made or adopted, which keeps
// running: 'hyve cluster release'). Release first asks the host, as a dry
// run, what it would remove, so a user who may not release it finds out
// before typing anything.
import { useEffect, useState } from 'react'
import { download } from '../lib/format'
import { useAction } from '../lib/hooks'
import { host, type Obj } from '../lib/kube'
import { ANNOTATIONS } from '../lib/types'
import { TextField } from './form'
import { Modal } from './Modal'
import { Banner, ErrorBanner } from './ui'

export type RemoveMode = 'delete' | 'detach' | 'release'

type ReleaseResult = { imported?: boolean; kubeconfig?: string; hasKubeconfig?: boolean; removed: string[]; detachError?: string }

const TITLES: Record<RemoveMode, string> = { delete: 'Delete cluster', detach: 'Detach cluster', release: 'Release cluster' }

function releasePath(c: Obj): string {
  return `clusters/${encodeURIComponent(c.metadata.namespace!)}/${encodeURIComponent(c.metadata.name)}/release`
}

export function ClusterRemove({
  cluster,
  mode,
  onClose,
  onDelete,
  onDone,
}: {
  cluster: Obj
  mode: RemoveMode
  onClose: () => void
  // Deletes the Cluster (delete and detach).
  onDelete: () => Promise<void>
  // After a release.
  onDone: (message: string) => void
}) {
  const name = cluster.metadata.name
  const [typed, setTyped] = useState('')
  const [saveKubeconfig, setSaveKubeconfig] = useState(true)
  const [force, setForce] = useState(false)
  const [plan, setPlan] = useState<ReleaseResult | null>(null)
  const check = useAction()
  const act = useAction()

  useEffect(() => {
    if (mode !== 'release') return
    check.run(async () => setPlan(await host<ReleaseResult>(releasePath(cluster), { dryRun: true })))
    // Once, for this cluster.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, cluster.metadata.uid])

  const blocked = mode === 'release' && (!plan || !!check.error)
  const ok = typed.trim() === name && !blocked && !act.busy
  const submit = () =>
    act.run(async () => {
      if (mode !== 'release') {
        await onDelete()
        return
      }
      const res = await host<ReleaseResult>(releasePath(cluster), { force })
      if (saveKubeconfig && res.kubeconfig) {
        // Base64, as Go sends bytes.
        download(`${cluster.metadata.namespace}-${name}.admin.kubeconfig`, atob(res.kubeconfig))
      }
      onDone(
        res.detachError
          ? `Released ${name}. hyve's and Fleet's agents are still on it, disconnected: ${res.detachError}`
          : `Released ${name}: it keeps running, and hyve no longer manages it.`,
      )
    })

  return (
    <Modal title={`${TITLES[mode]} ${name}`} onClose={onClose}>
      {mode === 'delete' && (
        <p className="text-sm">
          Its provider <strong>deletes the cluster itself</strong> (unless its template retains it), and Fleet removes what it deployed for it. To keep the cluster running and stop managing it, release it instead.
        </p>
      )}
      {mode === 'detach' && <p className="text-sm">hyve removes its agents and its own access from the cluster, and forgets it. The cluster keeps running, with what Fleet deployed.</p>}
      {mode === 'release' && (
        <>
          <p className="text-sm">
            hyve stops managing this cluster and <strong>leaves it running</strong>: its provider deletes nothing and keeps billing for it. hyve's and Fleet's agents come off it; what Fleet deployed stays, unmanaged. Adopting or importing it brings it back.
          </p>
          <ErrorBanner error={check.error} />
          {check.busy && <p className="text-muted text-sm">Checking what releasing it removes…</p>}
          {plan && (
            <details className="mb-3 text-sm">
              <summary className="cursor-pointer">Removed from hyve: {plan.removed.length} objects (nothing on the cluster but the agents)</summary>
              <ul className="mt-1 ml-5 list-disc font-mono text-xs break-all">
                {plan.removed.map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            </details>
          )}
          {plan?.hasKubeconfig && (
            <label className="mb-2 flex min-h-11 items-center gap-2 text-sm">
              <input type="checkbox" checked={saveKubeconfig} onChange={(e) => setSaveKubeconfig(e.target.checked)} />
              Download its admin kubeconfig: hyve's copy goes with it
            </label>
          )}
          {act.error && /force/.test(String(act.error)) && (
            <label className="mb-2 flex min-h-11 items-center gap-2 text-sm">
              <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
              Release it anyway, leaving the agents on it, disconnected
            </label>
          )}
        </>
      )}
      <ErrorBanner error={act.error} />
      {mode === 'delete' && cluster.metadata.annotations?.[ANNOTATIONS.ADOPTED] === 'true' && (
        <Banner tone="warning">This cluster was adopted: deleting it deletes it at its provider unless its template retains it.</Banner>
      )}
      <TextField
        className="mt-3"
        label={`Type ${name} to confirm`}
        value={typed}
        onChange={setTyped}
        autoFocus
        autoComplete="off"
      />
      <div className="mt-4 flex flex-wrap justify-end gap-2.5">
        <button type="button" className="btn btn-secondary" onClick={onClose}>
          Cancel
        </button>
        <button type="button" className="btn btn-danger" disabled={!ok} onClick={submit}>
          {act.busy ? 'Working…' : TITLES[mode].split(' ')[0]}
        </button>
      </div>
    </Modal>
  )
}
