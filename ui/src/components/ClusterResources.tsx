// A cluster's Resources tab: what hyve delivers to it (its template's
// resources and its own extras) with Fleet's state there. Extras can be
// added, edited and removed here ('hyve cluster resource').
import { useRef, useState } from 'react'
import { useAction, useLoad } from '../lib/hooks'
import { idOf, kube, newObject, remove, save, type Obj } from '../lib/kube'
import { clusterItems, clusterResourceObject, clusterStates, stateOn, type Entry, type Item } from '../lib/resources'
import { TYPES } from '../lib/types'
import { ResourceForm, type ResourceFormHandle } from './ResourceForm'
import { Banner, ErrorBanner, Loading } from './ui'
import { ViewDialog, type Section } from './ViewDialog'

// An extra's Fleet object is owned by its ClusterResource.
function findCR(it: Item) {
  return kube.find(TYPES.CLUSTER_RESOURCE, it.model.metadata.namespace, it.owner)
}

export function ClusterResources({ cluster }: { cluster: Obj }) {
  const ns = cluster.metadata.namespace!
  const name = cluster.metadata.name
  const { data, error, loading, reload } = useLoad(
    async (r) => {
      const [items, states, shared] = await Promise.all([
        clusterItems(r, cluster),
        clusterStates(r, ns, name),
        r.findAll(TYPES.RESOURCE).catch(() => [] as Obj[]),
      ])
      return { items, states, shared: shared.map((x) => ({ name: x.metadata.name, namespace: x.metadata.namespace! })) }
    },
    // The labels decide the template's resources.
    [ns, name, JSON.stringify(cluster.metadata.labels || {})],
  )
  const action = useAction()
  // null, or the extra being added (cr null) or edited.
  const [editing, setEditing] = useState<{ cr: Obj | null; entry: Entry | null } | null>(null)
  const [formKey, setFormKey] = useState(0)
  const [viewing, setViewing] = useState<{ title: string; sections: Section[] } | null>(null)
  const formRef = useRef<ResourceFormHandle>(null)

  if (loading) return <Loading />
  const items = data?.items || []
  const states = data?.states ?? null

  const startAdd = () => {
    action.setError('')
    setFormKey((k) => k + 1)
    setEditing({ cr: null, entry: null })
  }

  const startEdit = (it: Item) =>
    action.run(async () => {
      const cr = await findCR(it)
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { clusterName, ...entry } = cr.spec || {}
      setFormKey((k) => k + 1)
      setEditing({ cr, entry: { ...entry, name: it.name } })
    })

  const view = async (it: Item) => {
    const sections: Section[] = []
    if (it.ownerKind === 'ClusterResource') {
      const cr = await findCR(it).catch(() => null)
      if (cr) sections.push({ label: 'As written (ClusterResource)', object: cr })
    } else {
      const tpl = await kube.maybe(TYPES.TEMPLATE, it.model.metadata.namespace, it.owner).catch(() => null)
      const entry = (tpl?.spec?.resources || []).find((e: Entry) => (e.name || e.ref) === it.name)
      if (entry) sections.push({ label: `As written (template ${it.owner})`, object: entry })
    }
    sections.push({ label: `Delivered as ${it.kind}`, object: it.model })
    setViewing({ title: it.name, sections })
  }

  const submit = () =>
    action.run(async () => {
      const e = formRef.current!.entry()
      const want = clusterResourceObject(cluster, e)
      const cr = editing?.cr
      // Renamed: the ClusterResource's name and label follow the
      // resource's, so make the new one and drop the old.
      if (cr && cr.metadata.name === want.metadata.name) {
        await save(TYPES.CLUSTER_RESOURCE, { ...cr, spec: want.spec })
      } else {
        const taken = items.some((it) => it.ownerKind === 'ClusterResource' && it.name === e.name)
        if (taken && !cr) throw new Error(`There's already a resource named ${e.name}.`)
        await save(TYPES.CLUSTER_RESOURCE, newObject(TYPES.CLUSTER_RESOURCE, want))
        if (cr) await remove(TYPES.CLUSTER_RESOURCE, cr)
      }
      setEditing(null)
      await reload(true)
    })

  const drop = (it: Item) =>
    action.run(async () => {
      await remove(TYPES.CLUSTER_RESOURCE, await findCR(it))
      await reload(true)
    })

  return (
    <div>
      {states === null && <Banner>This cluster isn't registered with Fleet yet, so nothing has been delivered.</Banner>}
      <ErrorBanner error={error} />
      <ErrorBanner error={action.error} />
      <table className="table">
        <thead>
          <tr>
            <th>Name</th>
            <th>From</th>
            <th>Kind</th>
            <th>Source</th>
            <th>State here</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {!items.length && (
            <tr>
              <td colSpan={6} className="text-muted">
                No resources yet.
              </td>
            </tr>
          )}
          {items.map((it) => (
            <tr key={`${it.kind}/${idOf(it.model)}`}>
              <td>{it.name}</td>
              <td>{it.ownerKind === 'ClusterTemplate' ? `Template ${it.owner}` : 'This cluster'}</td>
              <td>{it.kind}</td>
              <td className="break-all">{it.source}</td>
              <td>{stateOn(it, states) || '-'}</td>
              <td className="text-right whitespace-nowrap">
                <button type="button" className="btn btn-tertiary btn-sm" onClick={() => view(it)}>
                  View
                </button>
                {it.ownerKind === 'ClusterResource' && (
                  <>
                    <button type="button" className="btn btn-tertiary btn-sm" disabled={action.busy} onClick={() => startEdit(it)}>
                      Edit
                    </button>
                    <button type="button" className="btn btn-tertiary btn-sm" disabled={action.busy} onClick={() => drop(it)}>
                      Remove
                    </button>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {editing ? (
        <div className="mt-5">
          <h3>{editing.cr ? `Edit ${editing.entry?.name}` : 'Add resource to this cluster'}</h3>
          <ResourceForm key={formKey} ref={formRef} value={editing.entry} shared={data?.shared || []} />
          <div className="mt-3 flex gap-2.5">
            <button type="button" className="btn btn-primary" disabled={action.busy} onClick={submit}>
              {editing.cr ? 'Save' : 'Add'}
            </button>
            <button type="button" className="btn btn-secondary" onClick={() => setEditing(null)}>
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <button type="button" className="btn btn-tertiary mt-5" onClick={startAdd}>
          Add resource to this cluster
        </button>
      )}
      <p className="text-muted mt-2.5 text-sm">
        CLI: <code>hyve cluster resource add {name} --helm/--git/--path/--ref</code>
      </p>
      {viewing && <ViewDialog title={viewing.title} sections={viewing.sections} onClose={() => setViewing(null)} />}
    </div>
  )
}
