// A container's log, followed as it's written.
import { useEffect, useRef, useState } from 'react'
import { followLog } from '../../lib/explorer'
import { download } from '../../lib/format'
import { Checkbox } from '../form'
import { ErrorBanner } from '../ui'

// The most kept on screen; older lines scroll away.
const MAX_CHARS = 2_000_000

export function PodLogs({ clusterId, namespace, pod, containers }: { clusterId: string; namespace: string; pod: string; containers: string[] }) {
  const [container, setContainer] = useState(containers[containers.length - 1] || '')
  const [previous, setPrevious] = useState(false)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [following, setFollowing] = useState(true)
  const [ended, setEnded] = useState(false)
  const box = useRef<HTMLPreElement>(null)
  const stick = useRef(true)

  useEffect(() => {
    const ctl = new AbortController()
    setText('')
    setError('')
    setEnded(false)
    followLog(
      clusterId,
      namespace,
      pod,
      { container, previous },
      (chunk) => setText((t) => (t + chunk).slice(-MAX_CHARS)),
      ctl.signal,
    ).then(
      () => setEnded(true),
      (e) => {
        if (!ctl.signal.aborted) setError(e instanceof Error ? e.message : String(e))
      },
    )
    return () => ctl.abort()
  }, [clusterId, namespace, pod, container, previous])

  // Stay at the bottom while following, unless the user scrolled up.
  useEffect(() => {
    if (following && stick.current && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [text, following])

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
        <Checkbox checked={previous} onChange={setPrevious} label="Previous container" />
        <Checkbox checked={following} onChange={setFollowing} label="Follow" />
        <span className="flex-1" />
        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => download(`${pod}-${container}.log`, text, 'text/plain')}>
          Download
        </button>
      </div>
      <ErrorBanner error={error} />
      <pre
        ref={box}
        onScroll={(e) => {
          const el = e.currentTarget
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
        className="h-[60vh] overflow-auto rounded-lg bg-neutral-950 p-3 font-mono text-xs whitespace-pre-wrap text-neutral-200"
      >
        {text || (error ? '' : ended ? '(no output)' : 'Waiting for output…')}
        {ended && <span className="text-neutral-500">{'\n'}— the log ended —</span>}
      </pre>
    </div>
  )
}
