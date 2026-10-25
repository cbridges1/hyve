// A cluster's Explorer tab: every resource type the cluster serves, listed
// as `kubectl get` lists them, and each object as YAML with its events (a
// pod's logs and shell too). Where you are is in the URL's query: type,
// ns (namespace), obj (an object's name) and view.
import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { errorMessage } from '../../lib/api'
import { age } from '../../lib/format'
import { useLoad } from '../../lib/hooks'
import type { Schema } from '../../lib/kube'
import { cellText, COMMON, discoverCluster, groupLabel, listTable, watchList, type Table } from '../../lib/explorer'
import { ErrorBanner, Loading } from '../ui'
import { ObjectView } from './ObjectView'

export function Explorer({ clusterId }: { clusterId: string }) {
  const [query, setQuery] = useSearchParams()
  const typeId = query.get('type') || 'pod'
  const ns = query.get('ns') || ''
  const obj = query.get('obj')
  const withParams = (p: Record<string, string | null>) => {
    const q = new URLSearchParams(query)
    for (const [k, v] of Object.entries(p)) {
      if (v) q.set(k, v)
      else q.delete(k)
    }
    return q
  }
  const set = (p: Record<string, string | null>) => setQuery(withParams(p))

  const { data: schemas, error, loading } = useLoad(() => discoverCluster(clusterId), [clusterId], { live: false })
  const { data: namespaces } = useLoad(
    async () => {
      if (!schemas?.namespace) return []
      const t = await listTable(clusterId, schemas.namespace, { limit: 500 }).catch(() => null)
      return (t?.rows || []).map((r) => r.object.metadata.name).sort()
    },
    [clusterId, schemas],
    { live: false },
  )

  if (loading) return <Loading label="Reading what the cluster serves…" />
  if (!schemas) return <ErrorBanner error={error} />
  const schema = schemas[typeId]

  return (
    <div className="flex flex-col gap-4 lg:flex-row">
      <TypeList schemas={schemas} current={typeId} onPick={(t) => set({ type: t, obj: null, view: null })} />
      <div className="min-w-0 flex-1">
        {!schema ? (
          <p className="text-muted text-sm">This cluster doesn't serve {typeId}.</p>
        ) : obj ? (
          <ObjectView
            clusterId={clusterId}
            schemas={schemas}
            schema={schema}
            namespace={schema.namespaced ? ns : undefined}
            name={obj}
            view={query.get('view') || 'yaml'}
            onView={(v) => set({ view: v })}
            onBack={() => set({ obj: null, view: null })}
            onOpen={(t, n, name) => set({ type: t, ns: n || ns, obj: name, view: null })}
          />
        ) : (
          <ResourceTable
            key={`${typeId}@${ns}`}
            clusterId={clusterId}
            schema={schema}
            namespace={ns}
            namespaces={namespaces || []}
            onNamespace={(n) => set({ ns: n || null })}
            hrefFor={(name, n) => `?${withParams({ obj: name, ns: n || ns, view: null })}`}
          />
        )}
      </div>
    </div>
  )
}

// The sidebar: common types first, then every listable type by API group.
function TypeList({ schemas, current, onPick }: { schemas: Record<string, Schema>; current: string; onPick: (t: string) => void }) {
  const [filter, setFilter] = useState('')
  const groups = useMemo(() => {
    const listable = Object.values(schemas).filter((s) => s.verbs.includes('list'))
    const f = filter.trim().toLowerCase()
    const match = (s: Schema) => !f || s.kind.toLowerCase().includes(f) || s.resource.includes(f) || s.group.includes(f)
    const common = COMMON.map((t) => schemas[t]).filter((s) => s && s.verbs.includes('list') && match(s))
    const byGroup = new Map<string, Schema[]>()
    for (const s of listable.filter(match)) {
      if (COMMON.includes(s.id)) continue
      const g = groupLabel(s)
      byGroup.set(g, [...(byGroup.get(g) || []), s])
    }
    const rest = [...byGroup.entries()].sort(([a], [b]) => (a === 'core' ? -1 : b === 'core' ? 1 : a.localeCompare(b)))
    return [...(common.length ? [['Common', common] as [string, Schema[]]] : []), ...rest.map(([g, ss]) => [g, ss.sort((a, b) => a.kind.localeCompare(b.kind))] as [string, Schema[]])]
  }, [schemas, filter])

  return (
    <aside className="w-full shrink-0 lg:w-56">
      <input placeholder="Find a type" value={filter} onChange={(e) => setFilter(e.target.value)} className="mb-2" />
      <nav className="max-h-[70vh] overflow-y-auto rounded-xl border border-neutral-200 bg-white p-1.5 dark:border-neutral-800 dark:bg-neutral-900">
        {groups.map(([g, ss]) => (
          <div key={g} className="mb-1">
            <div className="px-2 pt-2 pb-1 text-[11px] font-semibold tracking-wider text-neutral-400 uppercase dark:text-neutral-600">{g}</div>
            {ss.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => onPick(s.id)}
                title={s.group ? `${s.resource}.${s.group}` : s.resource}
                className={`block w-full truncate rounded-md px-2 py-1 text-left text-sm ${
                  s.id === current
                    ? 'bg-neutral-900 text-white dark:bg-white dark:text-neutral-900'
                    : 'text-neutral-700 hover:bg-neutral-100 dark:text-neutral-300 dark:hover:bg-neutral-800'
                }`}
              >
                {s.kind}
              </button>
            ))}
          </div>
        ))}
      </nav>
    </aside>
  )
}

function ResourceTable({
  clusterId,
  schema,
  namespace,
  namespaces,
  onNamespace,
  hrefFor,
}: {
  clusterId: string
  schema: Schema
  namespace: string
  namespaces: string[]
  onNamespace: (n: string) => void
  // An object's address, so its link opens in a new tab too.
  hrefFor: (name: string, namespace?: string) => string
}) {
  const [selector, setSelector] = useState('')
  const [applied, setApplied] = useState('')
  const [table, setTable] = useState<Table | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [more, setMore] = useState(false)

  const load = async (quiet: boolean) => {
    if (!quiet) setLoading(true)
    try {
      setTable(await listTable(clusterId, schema, { namespace: schema.namespaced ? namespace : undefined, labelSelector: applied }))
      setError('')
    } catch (e) {
      setError(errorMessage(e))
      if (!quiet) setTable(null)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load(false)
    // Live: the host watches the list as you, and the page reloads its
    // first page when it changes.
    return watchList(clusterId, schema, namespace, () => load(true))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, schema.id, namespace, applied])

  async function loadMore() {
    if (!table?.metadata?.continue) return
    setMore(true)
    try {
      const next = await listTable(clusterId, schema, {
        namespace: schema.namespaced ? namespace : undefined,
        labelSelector: applied,
        continueToken: table.metadata.continue,
      })
      setTable({ ...next, rows: [...table.rows, ...next.rows] })
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setMore(false)
    }
  }

  // The columns `kubectl get` shows by default (priority 0).
  const cols = (table?.columnDefinitions || []).map((c, i) => ({ ...c, i })).filter((c) => !c.priority)
  const showNS = schema.namespaced && !namespace

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-end gap-2">
        <h2 className="mr-auto mb-0 text-base">{schema.kind}</h2>
        {schema.namespaced && (
          <select className="w-auto min-w-[12rem]" value={namespace} onChange={(e) => onNamespace(e.target.value)} aria-label="Namespace">
            <option value="">All namespaces</option>
            {namespace && !namespaces.includes(namespace) && <option value={namespace}>{namespace}</option>}
            {namespaces.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        )}
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            setApplied(selector.trim())
          }}
        >
          <input className="w-56" placeholder="Label selector, e.g. app=web" value={selector} onChange={(e) => setSelector(e.target.value)} />
          <button type="submit" className="btn btn-secondary">
            Filter
          </button>
        </form>
      </div>
      <ErrorBanner error={error} />
      {loading ? (
        <Loading />
      ) : (
        table && (
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  {showNS && <th>Namespace</th>}
                  {cols.map((c) => (
                    <th key={c.i} title={c.description}>
                      {c.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {!table.rows.length && (
                  <tr>
                    <td colSpan={cols.length + 1} className="text-muted">
                      None{applied ? ' match' : ''}.
                    </td>
                  </tr>
                )}
                {table.rows.map((r) => {
                  const md = r.object.metadata
                  return (
                    <tr key={`${md.namespace}/${md.name}`}>
                      {showNS && <td className="whitespace-nowrap">{md.namespace}</td>}
                      {cols.map((c) => (
                        <td key={c.i} className={c.format === 'name' ? 'whitespace-nowrap' : ''}>
                          {c.format === 'name' ? (
                            <Link to={hrefFor(md.name, md.namespace)}>
                              {cellText(r.cells[c.i])}
                            </Link>
                          ) : c.name === 'Age' ? (
                            age(md.creationTimestamp)
                          ) : (
                            cellText(r.cells[c.i])
                          )}
                        </td>
                      ))}
                    </tr>
                  )
                })}
              </tbody>
            </table>
            {table.metadata?.continue && (
              <div className="mt-3 flex justify-center">
                <button type="button" className="btn btn-secondary" disabled={more} onClick={loadMore}>
                  {more ? 'Loading…' : 'Load more'}
                </button>
              </div>
            )}
          </div>
        )
      )}
    </div>
  )
}
