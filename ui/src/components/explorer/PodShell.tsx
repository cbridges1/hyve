// A shell in a container: xterm.js over the exec WebSocket, which the
// host's proxy passes through to the cluster as the signed-in user.
// Kubernetes' channel protocol: each binary message starts with its
// channel (0 stdin, 1 stdout, 2 stderr, 3 status, 4 resize).
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef, useState } from 'react'
import { execURL } from '../../lib/explorer'

const enc = new TextEncoder()

function frame(channel: number, data: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(data.length + 1))
  out[0] = channel
  out.set(data, 1)
  return out
}

export function PodShell({ clusterId, namespace, pod, containers }: { clusterId: string; namespace: string; pod: string; containers: string[] }) {
  const [container, setContainer] = useState(containers[0] || '')
  const [state, setState] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [attempt, setAttempt] = useState(0)
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!host.current) return
    const term = new Terminal({ cursorBlink: true, fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', theme: { background: '#0a0a0a' } })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host.current)
    fit.fit()
    setState('connecting')

    const ws = new WebSocket(execURL(clusterId, namespace, pod, container), ['v5.channel.k8s.io', 'v4.channel.k8s.io'])
    ws.binaryType = 'arraybuffer'
    const dec = new TextDecoder()
    const resize = () => {
      fit.fit()
      if (ws.readyState === WebSocket.OPEN) ws.send(frame(4, enc.encode(JSON.stringify({ Width: term.cols, Height: term.rows }))))
    }
    ws.onopen = () => {
      setState('open')
      resize()
      term.focus()
    }
    ws.onmessage = (m) => {
      const b = new Uint8Array(m.data as ArrayBuffer)
      if (!b.length) return
      const ch = b[0]
      const body = b.subarray(1)
      if (ch === 1 || ch === 2) term.write(dec.decode(body, { stream: true }))
      else if (ch === 3) {
        // The exec's final status: say so if it failed.
        try {
          const st = JSON.parse(dec.decode(body))
          if (st.status && st.status !== 'Success') term.write(`\r\n\x1b[31m${st.message || st.status}\x1b[0m\r\n`)
        } catch {
          // Not a Status; ignore.
        }
      }
    }
    ws.onclose = (e) => {
      setState('closed')
      term.write(`\r\n\x1b[90m— disconnected${e.reason ? `: ${e.reason}` : ''} —\x1b[0m\r\n`)
    }
    const input = term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(frame(0, enc.encode(d))))
    const ro = new ResizeObserver(resize)
    ro.observe(host.current)
    return () => {
      ro.disconnect()
      input.dispose()
      ws.close()
      term.dispose()
    }
  }, [clusterId, namespace, pod, container, attempt])

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
      <div ref={host} className="h-[60vh] overflow-hidden rounded-lg bg-neutral-950 p-2" />
    </div>
  )
}
