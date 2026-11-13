// Pieces the Fleet and Groups pages share: the new-group form and a
// target's clusters.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { idOf, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { KeyValueEditor, TextField } from './form'
import { Card, ErrorBanner } from './ui'

// A custom group: clusters by their labels. hyve copies it to every
// environment.
export function NewGroup({ busy, onCreate, onCancel }: { busy: boolean; onCreate: (name: string, labels: Record<string, string>) => void; onCancel: () => void }) {
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
    <Card title="New group" className="mb-4">
      <p className="text-muted mt-0 text-sm">
        Clusters with all these labels, which are set when a cluster is made (<code>--label</code>). Deployments aimed at the group reach them in every
        environment.
      </p>
      {bad && <ErrorBanner error={new Error(bad)} />}
      <TextField className="mb-3 sm:w-1/3" label="Name" required value={name} onChange={setName} />
      <div className="label">Clusters with these labels</div>
      <KeyValueEditor value={labels} onChange={setLabels} addLabel="Add label" />
      <div className="mt-3 flex gap-2.5">
        <button type="button" className="btn btn-primary" disabled={busy} onClick={create}>
          Create
        </button>
        <button type="button" className="btn btn-secondary" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </Card>
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
