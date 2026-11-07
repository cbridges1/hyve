// A Secret's keys in the explorer, decoded: each value hidden until shown,
// and copied with a click. A binary value says its size instead.
import { useState } from 'react'
import type { Obj } from '../../lib/kube'
import { asText, fromBase64 } from '../../lib/secrets'

export function SecretKeys({ secret }: { secret: Obj }) {
  const [shown, setShown] = useState<Record<string, boolean>>({})
  const [copied, setCopied] = useState('')
  const data: Record<string, string> = secret.data || {}
  const keys = Object.keys(data).sort()

  const copy = (k: string, text: string) =>
    navigator.clipboard.writeText(text).then(
      () => {
        setCopied(k)
        setTimeout(() => setCopied((c) => (c === k ? '' : c)), 1500)
      },
      () => setCopied(''),
    )

  if (!keys.length) return <p className="text-muted mb-0 text-sm">This Secret has no keys.</p>
  return (
    <div className="overflow-x-auto">
      <table className="table mb-0">
        <thead>
          <tr>
            <th>Key</th>
            <th>Value</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {keys.map((k) => {
            const bytes = fromBase64(data[k])
            const text = asText(bytes)
            return (
              <tr key={k}>
                <td className="whitespace-nowrap align-middle!">
                  <code>{k}</code>
                </td>
                <td className="w-full align-middle!">
                  {text === null ? (
                    <span className="text-muted text-sm">Binary ({bytes.length} bytes)</span>
                  ) : shown[k] ? (
                    <pre className="m-0 max-h-[200px] overflow-auto font-mono text-xs break-all whitespace-pre-wrap">{text}</pre>
                  ) : (
                    <span>••••••••</span>
                  )}
                </td>
                <td className="text-right whitespace-nowrap align-middle!">
                  {text !== null && (
                    <>
                      <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setShown({ ...shown, [k]: !shown[k] })}>
                        {shown[k] ? 'Hide' : 'Show'}
                      </button>
                      <button type="button" className="btn btn-tertiary btn-sm" onClick={() => copy(k, text)}>
                        {copied === k ? 'Copied' : 'Copy'}
                      </button>
                    </>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
