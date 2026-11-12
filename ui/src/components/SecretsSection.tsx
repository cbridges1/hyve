// The Secrets a Deployment, a cluster or a provider's clusters need (lib/secrets.ts), whether each
// exists, and its keys: view, create or change them here, as the signed-in
// user. One an operator syncs (Infisical's, External Secrets) is shown as
// managed by it, and not edited here: its next sync would undo the edit.
// Other Secrets are in the explorer.
import { useState, type ReactNode } from 'react'
import { useLoad } from '../lib/hooks'
import type { Reader } from '../lib/kube'
import type { Need } from '../lib/secrets'
import { SecretEdit } from './SecretEdit'
import { SecretView } from './SecretView'
import { ErrorBanner, Loading, Pill } from './ui'

export function SecretsSection({
  load,
  deps,
  hint,
  empty,
  showNamespace = false,
}: {
  load: (r: Reader) => Promise<Need[]>
  deps: unknown[]
  // Said in the create form, about where the Secret goes.
  hint: string
  // Said when nothing needs a Secret.
  empty: ReactNode
  // A column for each Secret's namespace, when they're in several.
  showNamespace?: boolean
}) {
  const { data, error, loading, reload } = useLoad(load, deps)
  const [viewing, setViewing] = useState<Need | null>(null)
  const [editing, setEditing] = useState<{ need: Need; existing: boolean } | null>(null)

  if (loading) return <Loading />
  const needs = data || []

  return (
    <div>
      <ErrorBanner error={error} />
      {!needs.length ? (
        <p className="text-muted text-sm">{empty}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                {showNamespace && <th>Environment</th>}
                <th>Keys</th>
                <th>State</th>
                <th>Needed by</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {needs.map((n) => (
                <tr key={`${n.namespace}/${n.name}`}>
                  <td>
                    {n.name}
                    {n.type && n.type !== 'Opaque' && <div className="text-muted text-xs">{n.type}</div>}
                  </td>
                  {showNamespace && <td>{n.namespace}</td>}
                  <td className="text-xs">{n.keys.join(', ') || '-'}</td>
                  <td>
                    {n.exists === null ? (
                      <Pill tone="idle" label="Can't read" title="You can't read Secrets here, and hyve-controller hasn't reported it missing." />
                    ) : n.exists ? (
                      <Pill tone="good" label="Present" />
                    ) : (
                      <Pill tone="bad" label="Missing" />
                    )}
                    {n.managedBy && <div className="text-muted mt-1 text-xs">managed by {n.managedBy}</div>}
                  </td>
                  <td className="text-sm">{n.neededBy.join(', ')}</td>
                  <td className="text-right whitespace-nowrap">
                    {n.exists === false ? (
                      <button type="button" className="btn btn-secondary btn-sm" onClick={() => setEditing({ need: n, existing: false })}>
                        Create
                      </button>
                    ) : n.exists ? (
                      <>
                        <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(n)}>
                          View
                        </button>
                        {!n.managedBy && (
                          <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setEditing({ need: n, existing: true })}>
                            Edit
                          </button>
                        )}
                      </>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {viewing && <SecretView namespace={viewing.namespace} name={viewing.name} onClose={() => setViewing(null)} />}
      {editing && (
        <SecretEdit
          existing={editing.existing}
          namespace={editing.need.namespace}
          name={editing.need.name}
          hint={hint}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null)
            await reload(true)
          }}
        />
      )}
    </div>
  )
}
