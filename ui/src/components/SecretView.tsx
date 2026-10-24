// One Secret: its keys, with each value hidden until revealed. It's read
// as the signed-in user, so what they can't read says so instead.
import { useMemo, useState } from 'react'
import { ApiError } from '../lib/api'
import { useLoad } from '../lib/hooks'
import { TYPES } from '../lib/types'
import { ViewDialog } from './ViewDialog'

function decode(b64: string) {
  try {
    const bin = atob(b64 || '')
    return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)))
  } catch {
    return b64
  }
}

export function SecretView({ namespace, name, onClose }: { namespace: string; name: string; onClose: () => void }) {
  const { data: secret, error, loading } = useLoad((r) => r.find(TYPES.SECRET, namespace, name), [namespace, name], { live: false })
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})

  const message = !error
    ? ''
    : error instanceof ApiError && error.status === 404
      ? "This Secret doesn't exist yet."
      : error instanceof ApiError && error.status === 403
        ? "You can't read this Secret."
        : error.message

  const keys = useMemo(() => {
    const d = secret?.data || {}
    return Object.keys(d).sort()
  }, [secret])

  // The Secret without its values.
  const meta = useMemo(() => {
    if (!secret) return []
    const md = JSON.parse(JSON.stringify(secret.metadata || {}))
    delete md.managedFields
    // kubectl apply keeps the whole Secret, values included, here.
    if (md.annotations) delete md.annotations['kubectl.kubernetes.io/last-applied-configuration']
    return [{ label: 'Metadata', object: { apiVersion: 'v1', kind: 'Secret', metadata: md } }]
  }, [secret])

  return (
    <ViewDialog title={`Secret ${namespace}/${name}`} sections={meta} onClose={onClose}>
      {loading ? (
        <p className="text-sm">Loading…</p>
      ) : message ? (
        <p className="text-error text-sm">{message}</p>
      ) : (
        <>
          <div className="text-muted mb-2.5 text-sm">Type: {secret?.type || 'Opaque'}</div>
          <table className="table mb-4">
            <thead>
              <tr>
                <th>Key</th>
                <th>Value</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {!keys.length && (
                <tr>
                  <td colSpan={3} className="text-muted">
                    This Secret has no keys.
                  </td>
                </tr>
              )}
              {keys.map((k) => (
                <tr key={k}>
                  <td className="align-middle!">
                    <code>{k}</code>
                  </td>
                  <td className="align-middle!">
                    {revealed[k] ? (
                      <pre className="m-0 max-h-[200px] overflow-auto font-mono text-xs break-all whitespace-pre-wrap">{decode(secret!.data![k])}</pre>
                    ) : (
                      <span>••••••••</span>
                    )}
                  </td>
                  <td className="text-right align-middle!">
                    <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setRevealed({ ...revealed, [k]: !revealed[k] })}>
                      {revealed[k] ? 'Hide' : 'Show'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </ViewDialog>
  )
}
