// Pieces the Fleet and Groups pages share: the new-group dialog and a
// target's clusters.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { idOf, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { KeyValueEditor, TextField } from './form'
import { Modal } from './Modal'
import { ErrorBanner } from './ui'

// A custom group, in a dialog as other things are made: clusters by
// their labels. hyve copies it to every environment. error is why saving
// failed.
export function NewGroup({
  busy,
  error,
  onCreate,
  onCancel,
}: {
  busy: boolean
  error?: string
  onCreate: (name: string, labels: Record<string, string>) => void
  onCancel: () => void
}) {
  const [name, setName] = useState('')
  const [labels, setLabels] = useState<Record<string, string>>({})
  const [bad, setBad] = useState('')
  const create = () => {
    if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)) return setBad('Name: lowercase letters, digits and dashes.')
    if (name.startsWith('template-') || name.startsWith('env-')) return setBad("template- and env- names are hyve's own groups.")
    if (!Object.keys(labels).length) return setBad('Give at least one label to select clusters by.')
    setBad('')
    onCreate(name, labels)
  }
  return (
    <Modal title="New group" onClose={onCancel} persistent={busy}>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          create()
        }}
      >
        <p className="text-muted mt-0 text-sm">
          Clusters with all these labels, which are set when a cluster is made (<code>--label</code>). Deployments aimed at the group reach them in every
          environment.
        </p>
        <ErrorBanner error={bad || error} />
        <TextField className="mb-3" label="Name" required value={name} onChange={setName} autoFocus placeholder="gold" />
        <div className="label">Clusters with these labels</div>
        <KeyValueEditor value={labels} onChange={setLabels} addLabel="Add label" />
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="btn btn-tertiary" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || !name}>
            {busy ? 'Creating…' : 'Create'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

// The clusters a target reaches, linked, the first few by name.
export function Members({ clusters }: { clusters: Obj[] }) {
  if (!clusters.length) return <span className="text-muted">no clusters</span>
  const shown = clusters.slice(0, 4)
  return (
    <span title={clusters.map(idOf).join(', ')}>
      {clusters.length} cluster{clusters.length === 1 ? '' : 's'}:{' '}
      {shown.map((c, i) => (
        <span key={idOf(c)}>
          {i > 0 && ', '}
          <Link to={paths.cluster(c.metadata.namespace!, c.metadata.name, 'deployments')}>{idOf(c)}</Link>
        </span>
      ))}
      {clusters.length > shown.length && ` and ${clusters.length - shown.length} more`}
    </span>
  )
}
