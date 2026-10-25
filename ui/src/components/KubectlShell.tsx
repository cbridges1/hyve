// The docked kubectl shell: starts a shell on the target cluster and
// shows its terminal. kubectl, helm and k9s in it run as the signed-in
// user, with their access to that cluster.
import { useEffect, useState } from 'react'
import { ApiError, errorMessage } from '../lib/api'
import { closeShell, discardShell, shellURL, startShell, type ShellTarget, type StartedShell } from '../lib/kubectlShell'
import { Icon } from './Icon'
import { Terminal, type TerminalState } from './Terminal'

export function KubectlShell({ target }: { target: ShellTarget }) {
  const [shell, setShell] = useState<StartedShell | null>(null)
  const [error, setError] = useState('')
  const [state, setState] = useState<TerminalState>('connecting')
  const [attempt, setAttempt] = useState(0)
  const [collapsed, setCollapsed] = useState(false)

  useEffect(() => {
    let cancelled = false
    setShell(null)
    setError('')
    setState('connecting')
    startShell(target.cluster).then(
      (s) => (cancelled ? discardShell(s) : setShell(s)),
      (e) => !cancelled && setError(e instanceof ApiError && e.status === 404 ? 'The kubectl shell is turned off on this host.' : errorMessage(e)),
    )
    return () => {
      cancelled = true
    }
  }, [target.cluster, attempt])

  const status = error ? 'Couldn’t start' : !shell ? 'Starting a shell…' : state === 'connecting' ? 'Connecting…' : state === 'open' ? 'Connected' : 'Ended'

  return (
    <section
      aria-label="Kubectl shell"
      className="fixed inset-x-0 bottom-0 z-30 flex flex-col border-t border-neutral-800 bg-neutral-950 text-neutral-200 shadow-2xl md:left-60"
    >
      <header className="flex h-10 shrink-0 items-center gap-3 px-3 text-sm">
        <Icon name="terminal" size={16} className="text-neutral-400" />
        <span className="font-medium">kubectl</span>
        <span className="truncate text-neutral-400">{target.label}</span>
        <span className="text-xs text-neutral-500">{status}</span>
        <span className="flex-1" />
        {(error || state === 'closed') && (
          <button type="button" className="rounded px-2 py-1 text-xs font-medium text-neutral-300 hover:bg-neutral-800" onClick={() => setAttempt((a) => a + 1)}>
            New shell
          </button>
        )}
        <button
          type="button"
          aria-label={collapsed ? 'Expand the shell' : 'Collapse the shell'}
          className="rounded p-1 text-neutral-400 hover:bg-neutral-800"
          onClick={() => setCollapsed((c) => !c)}
        >
          <Icon name="chevronDown" size={16} className={collapsed ? 'rotate-180' : ''} />
        </button>
        <button type="button" aria-label="Close the shell" className="rounded p-1 text-neutral-400 hover:bg-neutral-800" onClick={closeShell}>
          <Icon name="close" size={16} />
        </button>
      </header>
      <div className={collapsed ? 'hidden' : 'h-[40vh] px-2 pb-2'}>
        {error ? (
          <p className="p-3 font-mono text-xs text-red-400">{error}</p>
        ) : shell ? (
          <Terminal url={shellURL(shell)} onState={setState} className="h-full" />
        ) : (
          <p className="p-3 font-mono text-xs text-neutral-500">Starting a pod with kubectl, helm and k9s on the cluster…</p>
        )}
      </div>
    </section>
  )
}
