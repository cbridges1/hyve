// A ClusterTemplate's form: its ClusterClass, sizes, expiry schedule,
// variables (with locks), resources and cluster labels. Read-only when
// viewing.
import { useRef, useState } from 'react'
import { nextRun } from '../lib/cron'
import { useLoad } from '../lib/hooks'
import { save, type Obj } from '../lib/kube'
import { describe, kindOf, validate, type Entry } from '../lib/resources'
import { TYPES } from '../lib/types'
import { Checkbox, KeyValueEditor, TextField } from './form'
import { NameNamespace } from './NameNamespace'
import { ResourceForm, type ResourceFormHandle } from './ResourceForm'
import { Banner } from './ui'
import { ViewDialog } from './ViewDialog'

// A variable's value as text: JSON for anything but plain strings.
function toText(v: unknown): string {
  if (v === null || v === undefined) return ''
  return typeof v === 'string' ? v : JSON.stringify(v)
}

// Text back to a value: JSON when it parses, otherwise the string.
function fromText(s: string): unknown {
  if (s === '') return null
  try {
    return JSON.parse(s)
  } catch {
    return s
  }
}

type Row = { name: string; value: string; locked: boolean }

export type Mode = 'create' | 'edit' | 'view'

export function TemplateForm({ value, mode, onSaved, onCancel }: { value: Obj; mode: Mode; onSaved: (o: Obj) => void; onCancel: () => void }) {
  const isView = mode === 'view'
  const spec = value.spec || {}
  const [name, setName] = useState(value.metadata.name || '')
  const [namespace, setNamespace] = useState(value.metadata.namespace || '')
  const [description, setDescription] = useState<string>(spec.description || '')
  const [className, setClassName] = useState<string>(spec.clusterClassRef?.name || '')
  const [version, setVersion] = useState<string>(spec.kubernetesVersion || '')
  // lockedVariables' kubernetesVersion: clusters stay at the template's.
  const [lockVersion, setLockVersion] = useState<boolean>((spec.lockedVariables || []).includes('kubernetesVersion'))
  const [controlPlane, setControlPlane] = useState(String(spec.controlPlane?.replicas ?? 1))
  const [workers, setWorkers] = useState(String(spec.workers?.count ?? 1))
  const [schedule, setSchedule] = useState<string>(spec.schedule || '')
  const [rows, setRows] = useState<Row[]>(() => {
    const locked: string[] = spec.lockedVariables || []
    return Object.entries(spec.variables || {}).map(([n, v]) => ({ name: n, value: toText(v), locked: locked.includes(n) }))
  })
  const [labels, setLabels] = useState<Record<string, string>>({ ...(spec.labels || {}) })
  // Each entry is Fleet's own spec (or a ref), edited whole in the
  // resource form.
  const [resources, setResources] = useState<Entry[]>(() => JSON.parse(JSON.stringify(spec.resources || [])))
  // The entry being edited: its index, -1 for a new one, null for none.
  const [editing, setEditing] = useState<number | null>(null)
  const [formError, setFormError] = useState('')
  const [errors, setErrors] = useState<string[]>([])
  const [viewing, setViewing] = useState<Entry | null>(null)
  const [busy, setBusy] = useState(false)
  const rform = useRef<ResourceFormHandle>(null)

  const { data } = useLoad(async (r) => {
    const [classes, shared] = await Promise.all([r.findAll(TYPES.CLUSTER_CLASS).catch(() => []), r.findAll(TYPES.RESOURCE).catch(() => [])])
    return { classes, shared: shared.map((s) => ({ name: s.metadata.name, namespace: s.metadata.namespace! })) }
  }, [])
  const classes = data?.classes || []
  const classOptions = classes.filter((c) => c.metadata.namespace === namespace).map((c) => c.metadata.name)
  const selectedClass = classes.find((c) => c.metadata.namespace === namespace && c.metadata.name === className)

  const scheduleError = (() => {
    if (!schedule) return ''
    try {
      return `Next run: ${nextRun(schedule, new Date()).toLocaleString()}`
    } catch (e) {
      return (e as Error).message
    }
  })()
  const validationPassed = !!name && !!className && !scheduleError.startsWith('schedule')

  function loadDefaults() {
    const have = new Set(rows.map((r) => r.name))
    const add = (selectedClass?.spec?.variables || [])
      .filter((v: any) => !have.has(v.name))
      .map((v: any) => ({ name: v.name, value: toText(v.schema?.openAPIV3Schema?.default), locked: false }))
    setRows([...rows, ...add])
  }

  const setRow = (i: number, patch: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  function startEdit(i: number) {
    setFormError('')
    setEditing(i)
  }

  function saveEntry() {
    setFormError('')
    try {
      const e = rform.current!.entry()
      if (resources.some((r, j) => r.name === e.name && j !== editing)) throw new Error(`There's already a resource named ${e.name}.`)
      setResources(editing! < 0 ? [...resources, e] : resources.map((r, j) => (j === editing ? e : r)))
      setEditing(null)
    } catch (err) {
      setFormError((err as Error).message)
    }
  }

  function build(): Obj {
    const o: Obj = JSON.parse(JSON.stringify(value))
    o.metadata.name = name
    o.metadata.namespace = namespace
    const s: any = o.spec || {}
    const variables: Record<string, unknown> = {}
    rows.filter((r) => r.name).forEach((r) => (variables[r.name] = fromText(r.value)))
    s.description = description
    s.clusterClassRef = { ...(s.clusterClassRef || {}), name: className }
    s.kubernetesVersion = version
    s.schedule = schedule
    s.variables = variables
    s.lockedVariables = [...rows.filter((r) => r.name && r.locked).map((r) => r.name), ...(lockVersion && version ? ['kubernetesVersion'] : [])]
    s.controlPlane = { replicas: Number(controlPlane) || 1 }
    s.workers = { ...(s.workers || {}), count: Number(workers) }
    s.labels = Object.keys(labels).length ? labels : undefined
    for (const k of ['kubernetesVersion', 'schedule', 'description']) if (!s[k]) delete s[k]
    if (!s.lockedVariables.length) delete s.lockedVariables
    s.resources = resources.length ? JSON.parse(JSON.stringify(resources)) : undefined
    o.spec = s
    return o
  }

  async function onSave() {
    setErrors([])
    const o = build()
    const bad = (o.spec.resources || []).map(validate).filter(Boolean)
    if (bad.length) {
      setErrors(bad)
      return
    }
    setBusy(true)
    try {
      // hyve-controller turns the resources into Fleet objects.
      onSaved(await save(TYPES.TEMPLATE, o))
    } catch (e) {
      setErrors([(e as Error)?.message || String(e)])
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <NameNamespace name={name} namespace={namespace} onName={setName} onNamespace={setNamespace} fixed={mode !== 'create'} />
      <TextField label="Description" value={description} onChange={setDescription} disabled={isView} />
      <div className="grid gap-3 sm:grid-cols-12">
        <TextField
          className="sm:col-span-4"
          label="ClusterClass"
          hint="The ClusterClass clusters from this template are built from, in the template's namespace. Clusters can be in any environment."
          required
          value={className}
          onChange={setClassName}
          list={classOptions}
          disabled={isView}
        />
        <div className="sm:col-span-4">
          <TextField label="Kubernetes version" placeholder="ClusterClass default" value={version} onChange={setVersion} disabled={isView} />
          {version && (
            <div className="mt-1.5">
              <Checkbox label="Locked: clusters stay at this version" checked={lockVersion} onChange={setLockVersion} disabled={isView} />
            </div>
          )}
        </div>
        <TextField className="sm:col-span-2" label="Control-plane replicas" type="number" value={controlPlane} onChange={setControlPlane} disabled={isView} />
        <TextField className="sm:col-span-2" label="Worker replicas" type="number" value={workers} onChange={setWorkers} disabled={isView} />
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <TextField
          label="Expiry schedule"
          placeholder="e.g. 0 20 * * 5"
          hint="Cron (5 fields). Each cluster expires at the next run of the schedule after it is created."
          value={schedule}
          onChange={setSchedule}
          help={scheduleError || undefined}
          disabled={isView}
        />
      </div>

      <div>
        <h3>Variables</h3>
        <Banner>
          Defaults for ClusterClass variables. Values are JSON when valid JSON, otherwise strings. Locked variables cannot be changed when creating a
          cluster.
        </Banner>
        <div className="space-y-2">
          {rows.map((row, i) => (
            <div key={i} className="grid items-end gap-2 sm:grid-cols-12">
              <TextField className="sm:col-span-3" label="Name" value={row.name} onChange={(v) => setRow(i, { name: v })} disabled={isView} />
              <TextField className="sm:col-span-6" label="Value" value={row.value} onChange={(v) => setRow(i, { value: v })} disabled={isView} />
              <div className="flex h-9 items-center sm:col-span-2">
                <Checkbox label="Locked" checked={row.locked} onChange={(v) => setRow(i, { locked: v })} disabled={isView} />
              </div>
              {!isView && (
                <button type="button" className="btn btn-tertiary sm:col-span-1" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                  Remove
                </button>
              )}
            </div>
          ))}
          {isView && !rows.length && <p className="text-muted text-sm">—</p>}
        </div>
        {!isView && (
          <div className="mt-2 flex gap-2">
            <button type="button" className="btn btn-tertiary" onClick={() => setRows([...rows, { name: '', value: '', locked: false }])}>
              Add
            </button>
            <button type="button" className="btn btn-tertiary" disabled={!selectedClass} onClick={loadDefaults}>
              Load ClusterClass variables
            </button>
          </div>
        )}
      </div>

      <div>
        <h3>Resources</h3>
        <Banner>
          Delivered by Fleet to every cluster made from this template, now and later; hyve-controller keeps the Fleet objects in step. Secrets named
          here must exist in this namespace; Fleet copies them to each cluster.
        </Banner>
        {resources.length > 0 && (
          <div className="mb-2.5 overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Kind</th>
                  <th>Source</th>
                  <th>Target namespace</th>
                  <th>Secrets to copy</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {resources.map((r, i) => (
                  <tr key={r.name || i}>
                    <td>{r.name}</td>
                    <td>{kindOf(r)}</td>
                    <td className="break-all">{describe(r)}</td>
                    <td>{r.namespace || '-'}</td>
                    <td>{(r.secrets || []).join(', ') || '-'}</td>
                    <td className="text-right whitespace-nowrap">
                      <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setViewing(r)}>
                        View
                      </button>
                      {!isView && (
                        <>
                          <button type="button" className="btn btn-tertiary btn-sm" onClick={() => startEdit(i)}>
                            Edit
                          </button>
                          <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setResources(resources.filter((_, j) => j !== i))}>
                            Remove
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {isView && !resources.length && <p className="text-muted text-sm">—</p>}
        {editing !== null ? (
          <div className="rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
            <h3 className="mt-0">{editing < 0 ? 'New resource' : `Edit ${resources[editing].name}`}</h3>
            <ResourceForm ref={rform} key={editing} value={editing < 0 ? null : resources[editing]} shared={data?.shared || []} />
            {formError && (
              <div className="mt-3">
                <Banner tone="error">{formError}</Banner>
              </div>
            )}
            <div className="mt-3 flex gap-2">
              <button type="button" className="btn btn-primary" onClick={saveEntry}>
                {editing < 0 ? 'Add' : 'Done'}
              </button>
              <button type="button" className="btn btn-secondary" onClick={() => setEditing(null)}>
                Cancel
              </button>
            </div>
          </div>
        ) : (
          !isView && (
            <button type="button" className="btn btn-tertiary" onClick={() => startEdit(-1)}>
              Add resource
            </button>
          )
        )}
      </div>

      <div>
        <h3>Cluster labels</h3>
        <p className="hint mt-0 mb-2">Added to every cluster; Fleet sees them through CAAPF.</p>
        <KeyValueEditor key={isView ? 'view' : 'edit'} value={labels} onChange={setLabels} addLabel="Add" readOnly={isView} />
      </div>

      {errors.map((e, i) => (
        <Banner key={i} tone="error">
          {e}
        </Banner>
      ))}
      {!isView && (
        <div className="flex justify-end gap-2 border-t border-neutral-100 pt-4 dark:border-neutral-800">
          <button type="button" className="btn btn-tertiary" onClick={onCancel}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" disabled={busy || !validationPassed} onClick={onSave}>
            {mode === 'create' ? 'Create' : 'Save'}
          </button>
        </div>
      )}
      {viewing && <ViewDialog title={viewing.name || ''} sections={[{ label: 'As written', object: viewing }]} onClose={() => setViewing(null)} />}
    </div>
  )
}
