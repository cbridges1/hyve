// Resources down the side, clusters across the top: each cell is Fleet's
// state for that resource on that cluster, and why it's there (the
// template, or an extra on the cluster). Adding asks whether a resource
// goes on a template (every cluster made from it) or one cluster; Edit
// changes a row's resource where it's defined.
import { useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { EnvironmentPicker } from '../components/EnvironmentPicker'
import { RadioGroup, SelectField } from '../components/form'
import { ResourceForm, type ResourceFormHandle } from '../components/ResourceForm'
import { Banner, Card, ConfirmButton, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { ViewDialog, type Section } from '../components/ViewDialog'
import { useEnvironment } from '../lib/environment'
import { useLoad } from '../lib/hooks'
import { forget, idOf, kube, newObject, remove, save, type Obj } from '../lib/kube'
import { allClusterStates, allItems, clusterResourceObject, problem, stateColor, stateOn, type Entry, type Item } from '../lib/resources'
import { paths } from '../lib/routes'
import { LABELS, TYPES } from '../lib/types'

type Form = { target: 'template' | 'cluster'; template: string; cluster: string; entry: Entry | null; editing: boolean; key: number }
type Row = { it: Item; on: Set<string>; own: string }

function emptyForm(key = 0): Form {
  return { target: 'template', template: '', cluster: '', entry: null, editing: false, key }
}

const nsName = (o: Obj) => `${o.metadata.namespace}/${o.metadata.name}`
const clone = <T,>(x: T): T => JSON.parse(JSON.stringify(x))

export default function ResourceMatrix() {
  const [environment] = useEnvironment()
  const { data, error, loading, reload } = useLoad(async (r) => {
    const [all, states, shared] = await Promise.all([allItems(r), allClusterStates(r), r.findAll(TYPES.RESOURCE).catch(() => [] as Obj[])])
    return { ...all, states, shared: shared.map((x) => ({ name: x.metadata.name, namespace: x.metadata.namespace! })) }
  }, [])
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<Form>(emptyForm())
  const [errors, setErrors] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [viewing, setViewing] = useState<{ title: string; sections: Section[] } | null>(null)
  const rform = useRef<ResourceFormHandle>(null)

  const clusters = useMemo(() => data?.clusters || [], [data])
  const templates = useMemo(() => data?.templates || [], [data])
  const clusterResources = useMemo(() => data?.clusterResources || [], [data])
  const reaches = useMemo(() => data?.reaches || (() => []), [data])

  const columns = useMemo(
    () => clusters.filter((c) => !environment || c.metadata.namespace === environment).sort((a, b) => nsName(a).localeCompare(nsName(b))),
    [clusters, environment],
  )

  const rows: Row[] = useMemo(() => {
    const ids = new Set(columns.map(idOf))
    // A template's Fleet object outlives its entry until hyve-controller
    // deletes it; don't show it once the entry's gone.
    const listed = (it: Item) =>
      it.ownerKind !== 'ClusterTemplate' ||
      templates.some(
        (t) =>
          t.metadata.name === it.owner &&
          t.metadata.namespace === it.model.metadata.namespace &&
          (t.spec?.resources || []).some((e: Entry) => (e.name || e.ref) === it.name),
      )
    return (data?.items || [])
      .filter(listed)
      .map((it) => ({ it, on: new Set(reaches(it).map(idOf)), own: problem(it.model) }))
      .filter((r) => !environment || [...r.on].some((id) => ids.has(id)) || r.it.ownerKind === 'ClusterTemplate')
  }, [data, columns, templates, reaches, environment])

  const templateOptions = templates.map((t) => ({ label: nsName(t), value: idOf(t) }))
  const clusterOptions = columns.map((c) => ({ label: nsName(c), value: idOf(c) }))

  // Which clusters the new resource will reach, before it's saved.
  const preview = (() => {
    if (form.target === 'cluster') {
      const c = clusters.find((x) => idOf(x) === form.cluster)
      return c ? [c] : []
    }
    const t = templates.find((x) => idOf(x) === form.template)
    return t
      ? reaches({ ownerKind: 'ClusterTemplate', owner: t.metadata.name, model: { metadata: { name: '', namespace: t.metadata.namespace } } } as Item)
      : []
  })()

  const cell = (r: Row, c: Obj) => {
    if (!r.on.has(idOf(c))) return ''
    return stateOn(r.it, data?.states[nsName(c)] || null) || 'Waiting'
  }

  // A row's resource where it's defined: the template, or the cluster's
  // ClusterResource (whose spec is the entry, less its name).
  const owner = (r: Row): { tpl?: Obj; cr?: Obj; entry?: Entry | null; clusterName?: string } => {
    const it = r.it
    const ns = it.model.metadata.namespace
    if (it.ownerKind === 'ClusterTemplate') {
      const tpl = templates.find((x) => x.metadata.name === it.owner && x.metadata.namespace === ns)
      const entry = (tpl?.spec?.resources || []).find((e: Entry) => (e.name || e.ref) === it.name) || null
      return { tpl, entry }
    }
    const cr = clusterResources.find((x) => x.metadata.name === it.owner && x.metadata.namespace === ns)
    if (!cr) return {}
    const { clusterName, ...entry } = cr.spec || {}
    return { cr, entry: { ...entry, name: it.name }, clusterName }
  }

  // After a change, list again rather than from the cache.
  const refresh = async () => {
    ;[TYPES.TEMPLATE, TYPES.CLUSTER_RESOURCE, TYPES.FLEET_HELM_OP, TYPES.FLEET_BUNDLE, TYPES.FLEET_GIT_REPO].forEach(forget)
    await reload(true)
  }

  // The template as it is now: hyve-controller writes its status, so a
  // listed copy may be stale by the time it's saved.
  const fresh = (id: string) => {
    const [ns, name] = id.split('/')
    return kube.find(TYPES.TEMPLATE, ns, name)
  }

  // What's behind a row: the resource as its template or cluster has it
  // (a shared Resource's spec too, when it refers to one), and the Fleet
  // object hyve made from it.
  const view = async (r: Row) => {
    const it = r.it
    const ns = it.model.metadata.namespace!
    const sections: Section[] = []
    const { tpl, cr, entry } = owner(r)
    if (tpl && entry) sections.push({ label: `As written (template ${it.owner})`, object: entry })
    else if (cr) sections.push({ label: `As written (ClusterResource ${ns}/${it.owner})`, object: cr })
    if (entry?.ref) {
      const shared = await kube.maybe(TYPES.RESOURCE, entry.refNamespace || ns, entry.ref).catch(() => null)
      if (shared) sections.push({ label: `Resource ${entry.refNamespace || ns}/${entry.ref}`, object: shared })
    }
    sections.push({ label: `Delivered as ${it.kind}`, object: it.model })
    setViewing({ title: it.name, sections })
  }

  // An empty cell starts adding that row's resource to that cluster, as
  // an extra.
  const startAdd = (c?: Obj, r?: Row) => {
    const next = emptyForm(form.key + 1)
    if (c) {
      next.target = 'cluster'
      next.cluster = idOf(c)
    }
    if (r) {
      const { entry } = owner(r)
      next.entry = entry ? clone(entry) : null
    }
    setForm(next)
    setErrors([])
    setAdding(true)
  }

  // Edit changes the resource where it's defined.
  const startEdit = (r: Row) => {
    const { tpl, cr, entry, clusterName } = owner(r)
    if (!entry) return
    const next: Form = { ...emptyForm(form.key + 1), editing: true, entry: clone(entry) }
    if (tpl) next.template = idOf(tpl)
    else {
      next.target = 'cluster'
      next.cluster = `${cr!.metadata.namespace}/${clusterName}`
    }
    setForm(next)
    setErrors([])
    setAdding(true)
  }

  const onSave = async () => {
    setErrors([])
    let e: Entry
    try {
      e = rform.current!.entry()
    } catch (err) {
      setErrors([err instanceof Error ? err.message : String(err)])
      return
    }
    const replacing = form.editing ? form.entry?.name : null
    setSaving(true)
    try {
      if (form.target === 'cluster') {
        const c = clusters.find((x) => idOf(x) === form.cluster)
        if (!c) throw new Error('Pick a cluster')
        // 'hyve cluster resource add' replaces the one of the same name.
        const want = clusterResourceObject(c, e)
        const have = clusterResources.find(
          (x) =>
            x.metadata.namespace === c.metadata.namespace &&
            x.spec?.clusterName === c.metadata.name &&
            (x.metadata.labels?.[LABELS.RESOURCE] || x.metadata.name) === (replacing || e.name),
        )
        if (have && (!form.editing || replacing === e.name)) {
          await save(TYPES.CLUSTER_RESOURCE, { ...have, spec: want.spec })
        } else {
          await save(TYPES.CLUSTER_RESOURCE, newObject(TYPES.CLUSTER_RESOURCE, want))
          if (have) await remove(TYPES.CLUSTER_RESOURCE, have)
        }
      } else {
        if (!form.template) throw new Error('Pick a template')
        const t = await fresh(form.template)
        const list: Entry[] = (t.spec.resources || []).filter((x: Entry) => x.name !== replacing)
        if (list.some((x) => x.name === e.name)) throw new Error(`Template ${t.metadata.name} already has a resource named ${e.name}`)
        const at = (t.spec.resources || []).findIndex((x: Entry) => x.name === replacing)
        if (at >= 0) list.splice(at, 0, e)
        else list.push(e)
        t.spec.resources = list
        await save(TYPES.TEMPLATE, t)
      }
      setAdding(false)
      await refresh()
    } catch (ex) {
      setErrors([ex instanceof Error ? ex.message : String(ex)])
    } finally {
      setSaving(false)
    }
  }

  const onRemove = async (r: Row) => {
    setErrors([])
    try {
      if (r.it.ownerKind === 'ClusterTemplate') {
        const t = await fresh(`${r.it.model.metadata.namespace}/${r.it.owner}`)
        t.spec.resources = (t.spec.resources || []).filter((x: Entry) => x.name !== r.it.name)
        await save(TYPES.TEMPLATE, t)
      } else {
        const cr = clusterResources.find((x) => x.metadata.name === r.it.owner && x.metadata.namespace === r.it.model.metadata.namespace)
        if (cr) await remove(TYPES.CLUSTER_RESOURCE, cr)
      }
      await refresh()
    } catch (ex) {
      setErrors([ex instanceof Error ? ex.message : String(ex)])
    }
  }

  if (loading) return <Loading />

  return (
    <div>
      <PageHeader
        title="Delivery"
        subtitle="Every resource hyve delivers, across the clusters it reaches, with Fleet's state on each. A + adds the row's resource to that cluster only."
      >
        <EnvironmentPicker />
        <button type="button" className="btn btn-primary" onClick={() => startAdd()}>
          Add resource
        </button>
      </PageHeader>
      <ErrorBanner error={error} />
      {errors.map((e, i) => (
        <Banner key={i} tone="error">
          {e}
        </Banner>
      ))}

      {adding && (
        <Card className="mb-5" title={form.editing ? `Edit ${form.entry?.name}` : 'Add resource'}>
          <div className="mb-4 grid gap-4 sm:grid-cols-2">
            <RadioGroup
              value={form.target}
              onChange={(v) => setForm({ ...form, target: v as Form['target'] })}
              options={[
                { value: 'template', label: 'On a template (every cluster made from it, now and later)', disabled: form.editing },
                { value: 'cluster', label: 'On one cluster (an extra)', disabled: form.editing },
              ]}
            />
            {form.target === 'template' ? (
              <SelectField
                label="Template"
                value={form.template}
                onChange={(v) => setForm({ ...form, template: v })}
                options={templateOptions}
                placeholder=""
                disabled={form.editing}
              />
            ) : (
              <SelectField
                label="Cluster"
                value={form.cluster}
                onChange={(v) => setForm({ ...form, cluster: v })}
                options={clusterOptions}
                placeholder=""
                disabled={form.editing}
              />
            )}
          </div>
          <ResourceForm key={form.key} ref={rform} value={form.entry} shared={data?.shared || []} />
          <div className="mt-4">
            <Banner tone="info">
              {preview.length ? `Will be delivered to: ${preview.map(nsName).join(', ')}` : "No clusters yet; it reaches clusters as they're made."}
            </Banner>
          </div>
          <div className="flex gap-2.5">
            <button type="button" className="btn btn-primary" disabled={saving} onClick={onSave}>
              {form.editing ? 'Save' : 'Add'}
            </button>
            <button type="button" className="btn btn-secondary" onClick={() => setAdding(false)}>
              Cancel
            </button>
          </div>
        </Card>
      )}

      <div className="overflow-x-auto">
        <table className="table min-w-full">
          <thead>
            <tr>
              <th>Resource</th>
              <th>From</th>
              {columns.map((c) => (
                <th key={idOf(c)} className="text-center!">
                  <Link to={paths.cluster(c.metadata.namespace!, c.metadata.name)} className="normal-case">
                    {c.metadata.name}
                  </Link>
                  <div className="font-normal normal-case">{c.metadata.namespace}</div>
                </th>
              ))}
              <th />
            </tr>
          </thead>
          <tbody>
            {!rows.length && (
              <tr>
                <td colSpan={columns.length + 3} className="text-muted">
                  No resources yet.
                </td>
              </tr>
            )}
            {rows.map((r) => (
              <tr key={`${r.it.kind}/${idOf(r.it.model)}`}>
                <td className="whitespace-nowrap">
                  {r.it.name}
                  <div className="text-muted text-xs">
                    {r.it.kind} {r.it.source}
                  </div>
                  {r.own && <div className="text-error text-xs whitespace-normal">{r.own}</div>}
                </td>
                <td className="whitespace-nowrap">{r.it.ownerKind === 'ClusterTemplate' ? `Template ${r.it.owner}` : 'This cluster'}</td>
                {columns.map((c) => {
                  const s = cell(r, c)
                  return (
                    <td key={idOf(c)} className="text-center whitespace-nowrap">
                      {s ? (
                        <span className={stateColor(s)}>{s}</span>
                      ) : (
                        <button
                          type="button"
                          title="Add to this cluster"
                          className="px-2 text-neutral-400 opacity-60 hover:text-neutral-900 hover:opacity-100 dark:hover:text-white"
                          onClick={() => startAdd(c, r)}
                        >
                          +
                        </button>
                      )}
                    </td>
                  )
                })}
                <td className="text-right whitespace-nowrap">
                  <div className="flex justify-end gap-1">
                    <button type="button" className="btn btn-tertiary btn-sm" onClick={() => view(r)}>
                      View
                    </button>
                    <button type="button" className="btn btn-tertiary btn-sm" onClick={() => startEdit(r)}>
                      Edit
                    </button>
                    <ConfirmButton label="Remove" confirm="Remove?" onConfirm={() => onRemove(r)} />
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {viewing && <ViewDialog title={viewing.title} sections={viewing.sections} onClose={() => setViewing(null)} />}
    </div>
  )
}
