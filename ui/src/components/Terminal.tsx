// A terminal over a Kubernetes exec WebSocket: xterm.js speaking the
// channel protocol, where each binary message starts with its channel (0
// stdin, 1 stdout, 2 stderr, 3 status, 4 resize). Pod shells reach the
// exec through the host's proxy; the kubectl shell through the host's
// /v1/hyve/shells.
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerm } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef } from 'react'

const enc = new TextEncoder()

function frame(channel: number, data: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(data.length + 1))
  out[0] = channel
  out.set(data, 1)
  return out
}

export type TerminalState = 'connecting' | 'open' | 'closed'

// url is the exec WebSocket; a new url (or attempt) connects again.
export function Terminal({
  url,
  attempt = 0,
  onState,
  className = '',
}: {
  url: string
  attempt?: number
  onState?: (s: TerminalState) => void
  className?: string
}) {
  const host = useRef<HTMLDivElement>(null)
  // The latest onState, without reconnecting when it changes.
  const report = useRef(onState)
  useEffect(() => {
    report.current = onState
  })

  useEffect(() => {
    if (!host.current) return
    const term = new XTerm({ cursorBlink: true, fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', theme: { background: '#0a0a0a' } })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host.current)
    fit.fit()
    report.current?.('connecting')

    const ws = new WebSocket(url, ['v5.channel.k8s.io', 'v4.channel.k8s.io'])
    ws.binaryType = 'arraybuffer'
    const dec = new TextDecoder()
    const resize = () => {
      // Hidden (a collapsed panel): nothing to measure.
      if (!host.current?.offsetParent) return
      fit.fit()
      if (ws.readyState === WebSocket.OPEN) ws.send(frame(4, enc.encode(JSON.stringify({ Width: term.cols, Height: term.rows }))))
    }
    ws.onopen = () => {
      report.current?.('open')
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
      report.current?.('closed')
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
  }, [url, attempt])

  return <div ref={host} className={`overflow-hidden rounded-lg bg-neutral-950 p-2 ${className}`} />
}
