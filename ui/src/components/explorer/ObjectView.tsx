// One object on a child cluster: YAML (view, and edit with server-side
// apply), its events, its owners, and for a pod its logs and a shell.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { errorMessage } from '../../lib/api'
import { age } from '../../lib/format'
import type { Obj, Schema } from '../../lib/kube'
import { applyYaml, cellTone, ConflictError, deleteObject, editableYaml, eventsFor, getObject, viewYaml } from '../../lib/explorer'
import { CodeEditor } from '../CodeEditor'
import { Icon } from '../Icon'
import { Card, ConfirmButton, ErrorBanner, Loading, Pill } from '../ui'
import { PodLogs } from './PodLogs'
import { PodShell } from './PodShell'

// The type ID for an owner reference's apiVersion and kind.
function typeFor(schemas: Record<string, Schema>, apiVersion: string, kind: string): string | undefined {
  const group = apiVersion.includes('/') ? apiVersion.split('/')[0] : ''
  return Object.values(schemas).find((s) => s.group === group && s.kind === kind)?.id
}

export function ObjectView({
  clusterId,
  schemas,
  schema,
  namespace,
  name,
  view,
  onView,
  onBack,
  listHref,
  namespaceHref,
  onOpen,
}: {
  clusterId: string
  schemas: Record<string, Schema>
  schema: Schema
  namespace?: string
  name: string
  view: string
  onView: (v: string) => void
  onBack: () => void
  // The list of this type, all namespaces, and one namespace's.
  listHref: string
  namespaceHref: (namespace: string) => string
  onOpen: (type: string, namespace: string | undefined, name: string) => void
}) {
  const [obj, setObj] = useState<Obj | null>(null)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState('')
  const [conflict, setConflict] = useState('')
  const [busy, setBusy] = useState(false)
  const [showSecrets, setShowSecrets] = useState(false)
  const [events, setEvents] = useState<Obj[] | null>(null)
  const [allLabels, setAllLabels] = useState(false)

  const load = async () => {
    try {
      setObj(await getObject(clusterId, schema, namespace, name))
      setError('')
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  useEffect(() => {
    setObj(null)
    setEditing(false)
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, schema.id, namespace, name])

  useEffect(() => {
    if (view !== 'events' || !obj) return
    setEvents(null)
    eventsFor(clusterId, schemas.event, obj).then(setEvents, (e) => setError(errorMessage(e)))
  }, [view, obj, clusterId, schemas.event])

  if (!obj) return error ? <ErrorBanner error={error} /> : <Loading />

  const isPod = schema.id === 'pod'
  const containers: string[] = isPod ? [...(obj.spec?.initContainers || []), ...(obj.spec?.containers || [])].map((c: any) => c.name) : []
  const tabs = [
    { id: 'yaml', label: 'YAML', icon: 'edit' },
    ...(schemas.event ? [{ id: 'events', label: 'Events', icon: 'refresh' }] : []),
    ...(isPod
      ? [
          { id: 'logs', label: 'Logs', icon: 'logs' },
          { id: 'shell', label: 'Shell', icon: 'terminal' },
        ]
      : []),
  ]
  const canUpdate = schema.verbs.includes('patch')
  const canDelete = schema.verbs.includes('delete')

  async function save(force = false) {
    setBusy(true)
    setError('')
    setConflict('')
    try {
      setObj(await applyYaml(clusterId, schema, namespace, name, text, force))
      setEditing(false)
    } catch (e) {
      if (e instanceof ConflictError) setConflict(e.message)
      else setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const md = obj.metadata
  const labels = Object.entries(md.labels || {})
  const LABELS_SHOWN = 6
  const phase: string | undefined = obj.status?.phase
  const crumb = 'text-neutral-500 no-underline hover:text-neutral-900 hover:no-underline dark:text-neutral-400 dark:hover:text-neutral-100'

  return (
    <div className="space-y-4">
      <nav aria-label="Breadcrumb" className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
        <Link to={listHref} className={crumb}>
          {schema.kind}
        </Link>
        {namespace && (
          <>
            <Icon name="chevronRight" size={14} className="shrink-0 text-neutral-400" />
            <Link to={namespaceHref(namespace)} className={crumb}>
              {namespace}
            </Link>
          </>
        )}
        <Icon name="chevronRight" size={14} className="shrink-0 text-neutral-400" />
        <span className="min-w-0 font-medium text-neutral-900 [overflow-wrap:anywhere] dark:text-neutral-100">{name}</span>
      </nav>

      <div className="rounded-xl border border-neutral-200 bg-white p-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="text-[11px] font-semibold tracking-wider text-neutral-400 uppercase">
              {schema.kind}
              {schema.group ? ` · ${schema.group}` : ''}
            </div>
            <h2 className="mt-0.5 mb-0 text-lg [overflow-wrap:anywhere]">{name}</h2>
            <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-neutral-500">
              {namespace && <Pill tone="env" label={namespace} title="Namespace" />}
              {phase && <Pill tone={cellTone('phase', phase) || 'idle'} label={phase} />}
              {md.deletionTimestamp && <Pill tone="busy" label="Being deleted" />}
              <span title={md.creationTimestamp}>created {age(md.creationTimestamp)} ago</span>
            </div>
          </div>
          {canDelete && (
            <ConfirmButton
              label="Delete"
              confirm={`Delete ${name}?`}
              onConfirm={() => deleteObject(clusterId, schema, namespace, name).then(onBack, (e) => setError(errorMessage(e)))}
            />
          )}
        </div>

        {(md.ownerReferences || []).length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-1.5 text-sm">
            <span className="text-muted mr-1">Owned by</span>
            {md.ownerReferences!.map((r) => {
              const t = typeFor(schemas, r.apiVersion, r.kind)
              const chip = 'inline-flex max-w-full items-center gap-1 rounded-md border border-neutral-200 px-2 py-0.5 text-xs dark:border-neutral-700'
              const body = (
                <>
                  <span className="text-neutral-500">{r.kind}</span>
                  <span className="truncate font-medium">{r.name}</span>
                </>
              )
              return t ? (
                <button key={r.uid || r.name} type="button" className={`${chip} hover:bg-neutral-100 dark:hover:bg-neutral-800`} onClick={() => onOpen(t, namespace, r.name)}>
                  {body}
                </button>
              ) : (
                <span key={r.uid || r.name} className={chip}>
                  {body}
                </span>
              )
            })}
          </div>
        )}

        {labels.length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-1.5">
            {(allLabels ? labels : labels.slice(0, LABELS_SHOWN)).map(([k, v]) => (
              <span
                key={k}
                title={`${k}=${v}`}
                className="inline-flex max-w-full items-center rounded-md bg-neutral-100 px-2 py-0.5 font-mono text-[11px] text-neutral-700 dark:bg-neutral-800 dark:text-neutral-300"
              >
                <span className="truncate">
                  {k}
                  {v ? <span className="text-neutral-400">={v}</span> : null}
                </span>
              </span>
            ))}
            {labels.length > LABELS_SHOWN && (
              <button type="button" className="text-xs text-neutral-500 underline-offset-2 hover:underline" onClick={() => setAllLabels((v) => !v)}>
                {allLabels ? 'Fewer' : `+${labels.length - LABELS_SHOWN} more`}
              </button>
            )}
          </div>
        )}
      </div>

      <div className="flex flex-wrap gap-1 border-b border-neutral-200 dark:border-neutral-800">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => onView(t.id)}
            className={`-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap ${
              view === t.id ? 'border-neutral-900 text-neutral-900 dark:border-white dark:text-white' : 'border-transparent text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200'
            }`}
          >
            <Icon name={t.icon} size={15} />
            {t.label}
          </button>
        ))}
      </div>
      <ErrorBanner error={error} />

      {view === 'yaml' && (
        <Card>
          {editing ? (
            <>
              <CodeEditor value={text} onChange={setText} minHeight="28rem" upload />
              {conflict && (
                <div className="banner banner-warning mt-3">
                  <p className="mb-2">Someone else manages some of these fields: {conflict}</p>
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => save(true)}>
                    Save anyway, taking those fields over
                  </button>
                </div>
              )}
              <div className="mt-4 flex justify-end gap-2">
                <button type="button" className="btn btn-tertiary" onClick={() => setEditing(false)}>
                  Cancel
                </button>
                <button type="button" className="btn btn-primary" disabled={busy} onClick={() => save(false)}>
                  {busy ? 'Saving…' : 'Save'}
                </button>
              </div>
              <p className="hint">Saved with server-side apply (field manager hyve-ui): fields you leave out that hyve-ui set before are removed.</p>
            </>
          ) : (
            <>
              <div className="mb-2 flex justify-end gap-2">
                {obj.kind === 'Secret' && (
                  <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setShowSecrets((v) => !v)}>
                    {showSecrets ? 'Hide values' : 'Show values'}
                  </button>
                )}
                <button type="button" className="btn btn-tertiary btn-sm" onClick={load}>
                  Refresh
                </button>
                {canUpdate && (
                  <button
                    type="button"
                    className="btn btn-primary btn-sm"
                    onClick={() => {
                      setText(editableYaml(obj))
                      setConflict('')
                      setEditing(true)
                    }}
                  >
                    Edit
                  </button>
                )}
              </div>
              <CodeEditor value={viewYaml(obj, showSecrets)} readOnly minHeight="20rem" />
            </>
          )}
        </Card>
      )}

      {view === 'events' &&
        (events === null ? (
          <Loading />
        ) : !events.length ? (
          <div className="rounded-xl border border-dashed border-neutral-300 py-10 text-center text-sm text-neutral-500 dark:border-neutral-700">
            No events (Kubernetes keeps them for an hour).
          </div>
        ) : (
          <ul className="divide-y divide-neutral-100 overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:divide-neutral-800 dark:border-neutral-800 dark:bg-neutral-900">
            {events.map((e: any) => (
              <li key={e.metadata.uid || e.metadata.name} className="px-4 py-3">
                <div className="flex flex-wrap items-center gap-2 text-sm">
                  <Pill tone={e.type === 'Warning' ? 'bad' : 'idle'} label={e.type || 'Normal'} />
                  <span className="font-medium">{e.reason}</span>
                  <span className="text-muted ml-auto text-xs whitespace-nowrap">
                    {age(e.lastTimestamp || e.eventTime || e.metadata.creationTimestamp)} ago
                    {(e.count || e.series?.count || 1) > 1 ? ` · ×${e.count || e.series?.count}` : ''}
                  </span>
                </div>
                <p className="mt-1 mb-0 text-sm text-neutral-600 [overflow-wrap:anywhere] dark:text-neutral-400">{e.message || e.note}</p>
              </li>
            ))}
          </ul>
        ))}

      {view === 'logs' && isPod && <PodLogs clusterId={clusterId} namespace={namespace!} pod={name} containers={containers} />}
      {view === 'shell' && isPod && <PodShell clusterId={clusterId} namespace={namespace!} pod={name} containers={(obj.spec?.containers || []).map((c: any) => c.name)} />}
    </div>
  )
}
