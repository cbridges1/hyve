// A shell in a container, over the exec WebSocket, which the host's proxy
// passes through to the cluster as the signed-in user.
import { useState } from 'react'
import { execURL } from '../../lib/explorer'
import { Terminal, type TerminalState } from '../Terminal'

export function PodShell({ clusterId, namespace, pod, containers }: { clusterId: string; namespace: string; pod: string; containers: string[] }) {
  const [container, setContainer] = useState(containers[0] || '')
  const [state, setState] = useState<TerminalState>('connecting')
  const [attempt, setAttempt] = useState(0)

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-center gap-3">
        {containers.length > 1 && (
          <select className="w-auto" value={container} onChange={(e) => setContainer(e.target.value)} aria-label="Container">
            {containers.map((c) => (
              <option key={c}>{c}</option>
            ))}
          </select>
        )}
        <span className="text-muted text-sm">{state === 'connecting' ? 'Connecting…' : state === 'open' ? `Connected to ${container}` : 'Disconnected'}</span>
        {state === 'closed' && (
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => setAttempt((a) => a + 1)}>
            Reconnect
          </button>
        )}
      </div>
      <Terminal url={execURL(clusterId, namespace, pod, container)} attempt={attempt} onState={setState} className="h-[60vh]" />
    </div>
  )
}
