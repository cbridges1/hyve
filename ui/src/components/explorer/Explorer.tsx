// A cluster's Explorer tab: every resource type the cluster serves, listed
// as `kubectl get` lists them, and each object as YAML with its events (a
// pod's logs and shell too). Where you are is in the URL's query: type,
// ns (namespace), obj (an object's name) and view.
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { errorMessage } from '../../lib/api'
import { age } from '../../lib/format'
import { useLoad } from '../../lib/hooks'
import type { Schema } from '../../lib/kube'
import {
  cellTone,
  cellText,
  COMMON,
  discoverCluster,
  groupLabel,
  isCompactColumn,
  listTable,
  watchList,
  type Table,
  type TableColumn,
  type TableRow,
} from '../../lib/explorer'
import { Checkbox } from '../form'
import { Icon } from '../Icon'
import { ErrorBanner, Loading, Pill } from '../ui'
import { ObjectView } from './ObjectView'

// The types last opened, across clusters, for the sidebar's Recent group.
const RECENT_KEY = 'hyve.explorer.recent'
const RECENT_MAX = 5

function readRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
    return Array.isArray(v) ? v.filter((x) => typeof x === 'string') : []
  } catch {
    return []
  }
}

function rememberRecent(id: string) {
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify([id, ...readRecent().filter((x) => x !== id)].slice(0, RECENT_MAX)))
  } catch {
    // No storage (a private window): no Recent group.
  }
}

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

  useEffect(() => {
    if (schemas?.[typeId]) rememberRecent(typeId)
  }, [schemas, typeId])

  if (loading) return <Loading label="Reading what the cluster serves…" />
  if (!schemas) return <ErrorBanner error={error} />
  const schema = schemas[typeId]

  return (
    <div className="grid gap-4 lg:grid-cols-[15rem_minmax(0,1fr)] lg:gap-6">
      <TypeList schemas={schemas} current={typeId} onPick={(t) => set({ type: t, obj: null, view: null })} />
      <div className="min-w-0">
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
            listHref={`?${withParams({ obj: null, view: null, ns: null })}`}
            namespaceHref={(n) => `?${withParams({ obj: null, view: null, ns: n })}`}
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

// The sidebar: recent and common types first, then every listable type by
// API group, each group folded until opened or searched.
function TypeList({ schemas, current, onPick }: { schemas: Record<string, Schema>; current: string; onPick: (t: string) => void }) {
  const [filter, setFilter] = useState('')
  // Below lg the list is a drawer under a button naming the current type.
  const [showMobile, setShowMobile] = useState(false)
  const [open, setOpen] = useState<Set<string>>(() => new Set(['Recent', 'Common', schemas[current] && !COMMON.includes(current) ? groupLabel(schemas[current]) : '']))
  const f = filter.trim().toLowerCase()
  const nav = useRef<HTMLElement>(null)

  // The current type in view when the page opens on it.
  useEffect(() => {
    nav.current?.querySelector('[aria-current="true"]')?.scrollIntoView({ block: 'nearest' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const groups = useMemo(() => {
    const listable = Object.values(schemas).filter((s) => s.verbs.includes('list'))
    const match = (s: Schema) => !f || s.kind.toLowerCase().includes(f) || s.resource.includes(f) || s.group.includes(f)
    const pick = (ids: string[]) => ids.map((t) => schemas[t]).filter((s) => s && s.verbs.includes('list') && match(s))
    const recent = f ? [] : pick(readRecent()).filter((s) => !COMMON.includes(s.id))
    const common = pick(COMMON)
    const byGroup = new Map<string, Schema[]>()
    for (const s of listable.filter(match)) {
      if (COMMON.includes(s.id)) continue
      const g = groupLabel(s)
      byGroup.set(g, [...(byGroup.get(g) || []), s])
    }
    const rest = [...byGroup.entries()].sort(([a], [b]) => (a === 'core' ? -1 : b === 'core' ? 1 : a.localeCompare(b)))
    return [
      ...(recent.length ? [['Recent', recent] as [string, Schema[]]] : []),
      ...(common.length ? [['Common', common] as [string, Schema[]]] : []),
      ...rest.map(([g, ss]) => [g, ss.sort((a, b) => a.kind.localeCompare(b.kind))] as [string, Schema[]]),
    ]
    // Recent is read when the current type changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schemas, f, current])

  const toggle = (g: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(g)) n.delete(g)
      else n.add(g)
      return n
    })

  const cur = schemas[current]
  return (
    <aside className="min-w-0 lg:sticky lg:top-20 lg:self-start">
      <button
        type="button"
        className="btn btn-secondary w-full justify-between lg:hidden"
        aria-expanded={showMobile}
        onClick={() => setShowMobile((v) => !v)}
      >
        <span className="truncate">
          <span className="text-muted mr-1.5 font-normal">Type</span>
          {cur?.kind || current}
        </span>
        <Icon name="chevronDown" size={16} className={showMobile ? 'rotate-180' : ''} />
      </button>
      <div className={`${showMobile ? 'mt-2 block' : 'hidden'} lg:block`}>
        <div className="relative mb-2">
          <span className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-neutral-400">
            <Icon name="search" size={15} />
          </span>
          <input placeholder="Find a type" value={filter} onChange={(e) => setFilter(e.target.value)} className="pl-8" aria-label="Find a type" />
        </div>
        <nav ref={nav} className="max-h-[60vh] overflow-y-auto rounded-xl border border-neutral-200 bg-white p-1.5 shadow-sm lg:max-h-[calc(100vh-11rem)] dark:border-neutral-800 dark:bg-neutral-900">
          {!groups.length && <p className="text-muted px-2 py-3 text-sm">No type matches.</p>}
          {groups.map(([g, ss]) => {
            const expanded = !!f || open.has(g)
            return (
              <div key={g} className="mb-0.5">
                <button
                  type="button"
                  onClick={() => toggle(g)}
                  aria-expanded={expanded}
                  title={g}
                  className={`flex w-full items-center gap-1 rounded-md px-2 pt-2 pb-1 text-left text-neutral-400 hover:text-neutral-700 dark:text-neutral-500 dark:hover:text-neutral-300 ${
                    g === 'Recent' || g === 'Common' ? 'text-[11px] font-semibold tracking-wider uppercase' : 'text-xs font-medium'
                  }`}
                >
                  <Icon name="chevronRight" size={12} className={`shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />
                  <span className="truncate">{g}</span>
                  <span className="ml-auto pl-1 text-[11px] font-normal tracking-normal tabular-nums">{ss.length}</span>
                </button>
                {expanded &&
                  ss.map((s) => (
                    <button
                      key={s.id}
                      type="button"
                      onClick={() => {
                        onPick(s.id)
                        setShowMobile(false)
                      }}
                      title={s.group ? `${s.resource}.${s.group}` : s.resource}
                      aria-current={s.id === current}
                      className={`block w-full truncate rounded-md py-1 pr-2 pl-6 text-left text-sm ${
                        s.id === current
                          ? 'bg-neutral-900 font-medium text-white dark:bg-white dark:text-neutral-900'
                          : 'text-neutral-700 hover:bg-neutral-100 dark:text-neutral-300 dark:hover:bg-neutral-800'
                      }`}
                    >
                      {s.kind}
                    </button>
                  ))}
              </div>
            )
          })}
        </nav>
      </div>
    </aside>
  )
}

type Col = TableColumn & { i: number }

// A cell: Age from the object, status-like values as pills, names as links.
function Cell({ col, row, href }: { col: Col; row: TableRow; href: string }) {
  const md = row.object.metadata
  const text = col.name === 'Age' ? age(md.creationTimestamp) : cellText(row.cells[col.i])
  if (col.format === 'name') {
    return (
      <Link to={href} className="font-medium [overflow-wrap:anywhere]" onClick={(e) => e.stopPropagation()}>
        {text}
      </Link>
    )
  }
  // Timestamps (a CRD's date columns) as ages, the time on hover.
  if (col.type === 'date' && col.name !== 'Age' && !Number.isNaN(Date.parse(text))) return <span title={text}>{age(text)}</span>
  const tone = cellTone(col.name, text)
  if (tone) return <Pill tone={tone} label={text} />
  return <>{text || <span className="text-neutral-300 dark:text-neutral-700">—</span>}</>
}

// The wide columns (`kubectl get -o wide`), turned on one by one.
function ColumnsMenu({ columns, shown, onChange }: { columns: string[]; shown: Set<string>; onChange: (s: Set<string>) => void }) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent | KeyboardEvent) => {
      if (e instanceof KeyboardEvent ? e.key === 'Escape' : !box.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', close)
    }
  }, [open])
  return (
    <div ref={box} className="relative">
      <button type="button" className="btn btn-secondary w-full" aria-expanded={open} title="More columns, as kubectl get -o wide shows" onClick={() => setOpen((v) => !v)}>
        <Icon name="columns" size={15} />
        Columns{shown.size ? ` (+${shown.size})` : ''}
      </button>
      {open && (
        <div className="absolute right-0 z-20 mt-1 w-56 space-y-1.5 rounded-lg border border-neutral-200 bg-white p-3 shadow-lg dark:border-neutral-700 dark:bg-neutral-900">
          {columns.map((c) => (
            <Checkbox
              key={c}
              label={c}
              checked={shown.has(c)}
              onChange={(v) => {
                const n = new Set(shown)
                if (v) n.add(c)
                else n.delete(c)
                onChange(n)
              }}
            />
          ))}
        </div>
      )}
    </div>
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
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [selector, setSelector] = useState('')
  const [applied, setApplied] = useState('')
  const [extra, setExtra] = useState<Set<string>>(new Set())
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

  // The columns `kubectl get` shows by default (priority 0), and the wide
  // ones (`-o wide`) the user turned on.
  const all: Col[] = (table?.columnDefinitions || []).map((c, i) => ({ ...c, i }))
  const optional = all.filter((c) => c.priority)
  const cols = all.filter((c) => !c.priority || extra.has(c.name))
  const nameCol = cols.find((c) => c.format === 'name')
  const otherCols = cols.filter((c) => c !== nameCol)
  // The namespace goes under the name, not in a column of its own.
  const showNS = schema.namespaced && !namespace

  const s = search.trim().toLowerCase()
  const rows = (table?.rows || []).filter(
    (r) => !s || r.object.metadata.name.toLowerCase().includes(s) || (r.object.metadata.namespace || '').toLowerCase().includes(s),
  )
  const total = table?.rows.length || 0

  const nameBlock = (r: TableRow) => {
    const md = r.object.metadata
    return (
      <div className="min-w-0">
        {nameCol ? <Cell col={nameCol} row={r} href={hrefFor(md.name, md.namespace)} /> : <Link to={hrefFor(md.name, md.namespace)}>{md.name}</Link>}
        {showNS && md.namespace && <div className="text-muted mt-0.5 text-xs [overflow-wrap:anywhere]">{md.namespace}</div>}
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h2 className="mb-0 text-base">{schema.kind}</h2>
        {table && (
          <span className="text-muted text-sm">
            {s ? `${rows.length} of ${total}` : total}
            {table.metadata?.continue ? '+' : ''}
          </span>
        )}
        <span className="flex items-center gap-1.5 text-xs text-neutral-400" title="The list updates as the cluster changes">
          <span className="h-1.5 w-1.5 rounded-full bg-green-500" />
          Live
        </span>
        {schema.namespaced && (
          <select
            className="ml-auto w-full sm:w-auto sm:min-w-[12rem]"
            value={namespace}
            onChange={(e) => onNamespace(e.target.value)}
            aria-label="Namespace"
          >
            <option value="">All namespaces</option>
            {namespace && !namespaces.includes(namespace) && <option value={namespace}>{namespace}</option>}
            {namespaces.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        )}
      </div>

      <div className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <span className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-neutral-400">
            <Icon name="search" size={15} />
          </span>
          <input className="pl-8" placeholder={`Search ${schema.kind} by name`} value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Search by name" />
        </div>
        <form
          className="relative flex-1"
          onSubmit={(e) => {
            e.preventDefault()
            setApplied(selector.trim())
          }}
        >
          <span className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-neutral-400">
            <Icon name="tag" size={15} />
          </span>
          <input
            className={`pl-8 ${applied ? 'pr-16' : ''}`}
            placeholder="Label selector, then Enter (app=web)"
            value={selector}
            onChange={(e) => setSelector(e.target.value)}
            aria-label="Label selector"
          />
          {applied && (
            <button
              type="button"
              className="btn btn-tertiary btn-sm absolute top-1/2 right-1 -translate-y-1/2"
              onClick={() => {
                setSelector('')
                setApplied('')
              }}
            >
              Clear
            </button>
          )}
        </form>
        {optional.length > 0 && <ColumnsMenu columns={optional.map((c) => c.name)} shown={extra} onChange={setExtra} />}
      </div>

      <ErrorBanner error={error} />
      {loading ? (
        <Loading />
      ) : (
        table && (
          <>
            {!rows.length ? (
              <div className="rounded-xl border border-dashed border-neutral-300 py-10 text-center text-sm text-neutral-500 dark:border-neutral-700">
                {total ? `No ${schema.kind} matches “${search}”.` : `No ${schema.kind}${applied ? ' matches that selector' : namespace ? ` in ${namespace}` : ''}.`}
              </div>
            ) : (
              <>
                {/* Phones: a card per object, so nothing scrolls sideways. */}
                <ul className="divide-y divide-neutral-100 overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm md:hidden dark:divide-neutral-800 dark:border-neutral-800 dark:bg-neutral-900">
                  {rows.map((r) => (
                    <li key={`${r.object.metadata.namespace}/${r.object.metadata.name}`} className="px-4 py-3">
                      {nameBlock(r)}
                      {otherCols.length > 0 && (
                        <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1.5 text-sm">
                          {otherCols.map((c) => (
                            <div key={c.i} className="min-w-0">
                              <dt className="text-[11px] font-semibold tracking-wider text-neutral-400 uppercase">{c.name}</dt>
                              <dd className="[overflow-wrap:anywhere]">
                                <Cell col={c} row={r} href="" />
                              </dd>
                            </div>
                          ))}
                        </dl>
                      )}
                    </li>
                  ))}
                </ul>

                {/* Wider screens: a table whose long values wrap; it scrolls
                    sideways only when many wide columns are on. */}
                <div className="hidden overflow-x-auto rounded-xl md:block">
                  <table className="table">
                    <thead>
                      <tr>
                        <th className="min-w-[12rem]">{nameCol?.name || 'Name'}</th>
                        {otherCols.map((c) => (
                          <th key={c.i} title={c.description} className={isCompactColumn(c) ? '' : 'whitespace-normal'}>
                            {c.name}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((r) => {
                        const md = r.object.metadata
                        return (
                          <tr
                            key={`${md.namespace}/${md.name}`}
                            className="cursor-pointer"
                            onClick={(e) => {
                              // Not when selecting text to copy it.
                              if (window.getSelection()?.toString()) return
                              if (e.metaKey || e.ctrlKey) window.open(hrefFor(md.name, md.namespace), '_blank')
                              else navigate(hrefFor(md.name, md.namespace))
                            }}
                          >
                            <td>{nameBlock(r)}</td>
                            {otherCols.map((c) => (
                              <td key={c.i} className={isCompactColumn(c) ? 'whitespace-nowrap' : 'max-w-[24rem] break-words'}>
                                <Cell col={c} row={r} href="" />
                              </td>
                            ))}
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              </>
            )}
            {table.metadata?.continue && (
              <div className="flex justify-center">
                <button type="button" className="btn btn-secondary" disabled={more} onClick={loadMore}>
                  {more ? 'Loading…' : 'Load more'}
                </button>
              </div>
            )}
          </>
        )
      )}
    </div>
  )
}
