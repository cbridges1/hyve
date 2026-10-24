// One object on a child cluster: YAML (view, and edit with server-side
// apply), its events, its owners, and for a pod its logs and a shell.
import { useEffect, useState } from 'react'
import { errorMessage } from '../../lib/api'
import { age } from '../../lib/format'
import type { Obj, Schema } from '../../lib/kube'
import { applyYaml, ConflictError, deleteObject, editableYaml, eventsFor, getObject, viewYaml } from '../../lib/explorer'
import { CodeEditor } from '../CodeEditor'
import { Banner, Card, ConfirmButton, ErrorBanner, Loading } from '../ui'
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
    { id: 'yaml', label: 'YAML' },
    ...(schemas.event ? [{ id: 'events', label: 'Events' }] : []),
    ...(isPod ? [
      { id: 'logs', label: 'Logs' },
      { id: 'shell', label: 'Shell' },
    ] : []),
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

  return (
    <div>
      <button type="button" className="btn btn-tertiary btn-sm mb-2 -ml-2" onClick={onBack}>
        ← {schema.kind} list
      </button>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 className="mb-0 text-base">
          {schema.kind} {namespace ? `${namespace}/` : ''}
          {name}
        </h2>
        <div className="flex items-center gap-2">
          {canDelete && (
            <ConfirmButton
              label="Delete"
              confirm={`Delete ${name}?`}
              onConfirm={() =>
                deleteObject(clusterId, schema, namespace, name).then(onBack, (e) => setError(errorMessage(e)))
              }
            />
          )}
        </div>
      </div>
      {obj.metadata.deletionTimestamp && <Banner tone="warning">Being deleted.</Banner>}
      {(obj.metadata.ownerReferences || []).length > 0 && (
        <p className="mb-3 text-sm text-neutral-600 dark:text-neutral-400">
          Owned by{' '}
          {obj.metadata.ownerReferences!.map((r, i) => {
            const t = typeFor(schemas, r.apiVersion, r.kind)
            return (
              <span key={r.uid || r.name}>
                {i > 0 && ', '}
                {t ? (
                  <button type="button" className="underline underline-offset-2" onClick={() => onOpen(t, namespace, r.name)}>
                    {r.kind} {r.name}
                  </button>
                ) : (
                  `${r.kind} ${r.name}`
                )}
              </span>
            )
          })}
        </p>
      )}
      <div className="mb-3 flex gap-1 border-b border-neutral-200 dark:border-neutral-800">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => onView(t.id)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium ${
              view === t.id ? 'border-neutral-900 text-neutral-900 dark:border-white dark:text-white' : 'border-transparent text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200'
            }`}
          >
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
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Type</th>
                <th>Reason</th>
                <th>Age</th>
                <th>Count</th>
                <th>Message</th>
              </tr>
            </thead>
            <tbody>
              {!events.length && (
                <tr>
                  <td colSpan={5} className="text-muted">
                    No events (Kubernetes keeps them for an hour).
                  </td>
                </tr>
              )}
              {events.map((e: any) => (
                <tr key={e.metadata.uid || e.metadata.name}>
                  <td className={e.type === 'Warning' ? 'text-warning' : ''}>{e.type}</td>
                  <td>{e.reason}</td>
                  <td>{age(e.lastTimestamp || e.eventTime || e.metadata.creationTimestamp)}</td>
                  <td>{e.count || e.series?.count || 1}</td>
                  <td className="break-words">{e.message || e.note}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ))}

      {view === 'logs' && isPod && <PodLogs clusterId={clusterId} namespace={namespace!} pod={name} containers={containers} />}
      {view === 'shell' && isPod && <PodShell clusterId={clusterId} namespace={namespace!} pod={name} containers={(obj.spec?.containers || []).map((c: any) => c.name)} />}
    </div>
  )
}
