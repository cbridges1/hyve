// Fleet: what goes on clusters. One list of Deployments (each one Fleet
// GitRepo, HelmOp or Bundle, aimed at templates, classes, environments,
// groups or clusters), shown by category: all of them; by each template,
// environment, class or group they're aimed at; by each cluster they reached;
// or those reaching no cluster. Then narrowed to one template (or cluster...),
// by name and by state, a page at a time. Deployments written by hand are
// here too. Custom groups are made and deleted here (Groups) and on the
// Groups page.
import { useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { TextField } from '../components/form'
import { Members, NewGroup } from '../components/GroupParts'
import { ConfirmButton, EmptyState, ErrorBanner, Loading, PageHeader, Pill } from '../components/ui'
import {
  CLUSTER_GROUP,
  groupKind,
  HOST_WORKSPACE,
  loadDelivery,
  reaches,
  readiness,
  ROLLOUTS,
  stateTone,
  targetKey,
  targetLabel,
  type ClusterState,
  type Delivery,
  type Deployment,
  type Target,
  type TargetKind,
} from '../lib/delivery'
import { useAction, useLoad } from '../lib/hooks'
import { can, idOf, newObject, remove, save, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from '../lib/types'
import { Table } from '../components/Table'

const SOURCE_LABEL = { git: 'Git', helm: 'Helm', manifests: 'Manifests' }
const HOST_ID = `${HOST_WORKSPACE}/local`

type Category = 'all' | 'templates' | 'environments' | 'classes' | 'groups' | 'clusters' | 'none'
type StateFilter = '' | 'ready' | 'problem' | 'paused'

// item: what one of the category is called, for the second dropdown; kind:
// the target kind a Deployment is aimed at to be in it.
const CATEGORIES: { value: Category; label: string; item?: string; kind?: TargetKind }[] = [
  { value: 'all', label: 'All deployments' },
  { value: 'templates', label: 'Templates', item: 'Template', kind: 'template' },
  { value: 'environments', label: 'Environments', item: 'Environment', kind: 'env' },
  { value: 'classes', label: 'Cluster classes', item: 'Class', kind: 'class' },
  { value: 'groups', label: 'Groups', item: 'Group', kind: 'group' },
  { value: 'clusters', label: 'Clusters', item: 'Cluster' },
  { value: 'none', label: 'No target' },
]

function customGroups(dl: Delivery): Obj[] {
  return dl.groups.filter((g) => g.metadata.namespace === DEFAULT_TEMPLATE_NAMESPACE && groupKind(g) === 'custom')
}

// The templates, environments... of a category, by name; clusters by id.
function itemsOf(dl: Delivery, c: Category): { value: string; label: string }[] {
  const named = (names: string[]) => [...new Set(names)].sort().map((n) => ({ value: n, label: n }))
  switch (c) {
    case 'templates':
      return named(dl.templates.map((t) => t.metadata.name))
    case 'environments':
      return named(dl.environments)
    case 'classes':
      return named(dl.classes)
    case 'groups':
      return named(customGroups(dl).map((g) => g.metadata.name))
    case 'clusters':
      return [...named(dl.clusters.map(idOf)), { value: HOST_ID, label: 'local (host)' }]
  }
  return []
}

// Whether a Deployment reaches a CAPI cluster ("env/name") or the host:
// it's there, or one of its targets selects it.
function reachesCluster(dl: Delivery, d: Deployment, id: string): boolean {
  if (d.states.some((s) => s.cluster === id)) return true
  if (id === HOST_ID) return d.namespace === HOST_WORKSPACE
  if (d.namespace === HOST_WORKSPACE) return false
  const c = dl.clusters.find((x) => idOf(x) === id)
  return !!c && d.targets.some((t) => reaches(t, c, dl.groups))
}

function reachesNoCluster(dl: Delivery, d: Deployment): boolean {
  return ![...dl.clusters.map(idOf), HOST_ID].some((id) => reachesCluster(dl, d, id))
}

// Whether a Deployment is in a category at all (counted in its dropdown).
function inCategory(dl: Delivery, d: Deployment, c: Category): boolean {
  const kind = CATEGORIES.find((x) => x.value === c)?.kind
  if (kind) return d.targets.some((t) => t.kind === kind)
  if (c === 'none') return reachesNoCluster(dl, d)
  if (c === 'clusters') return !reachesNoCluster(dl, d)
  return true
}

function matches(d: Deployment, q: string, state: StateFilter): boolean {
  if (q && !`${d.name} ${d.namespace} ${d.source}`.toLowerCase().includes(q.toLowerCase())) return false
  if (state === 'paused') return d.paused
  if (!state) return true
  const s = readiness(d).state
  return state === 'ready' ? s === 'Ready' : s !== 'Ready' && s !== 'NoTargets'
}

export default function Fleet() {
  const [params, setParams] = useSearchParams()
  const { data, error, loading, reload } = useLoad((r) => loadDelivery(r), [])
  const act = useAction()
  const [adding, setAdding] = useState(false)
  const [pageSize, setPageSize] = useState(readPageSize)
  const q = params.get('q') || ''
  const state = (params.get('state') || '') as StateFilter
  const cat = CATEGORIES.find((c) => c.value === params.get('category')) || CATEGORIES[0]
  const item = cat.item ? params.get('item') || '' : ''
  const page = Math.max(0, Number(params.get('page') || 1) - 1)
  // Any change but the page's own goes back to the first page.
  const set = (changes: Record<string, string>) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (!('page' in changes)) n.delete('page')
        for (const [k, v] of Object.entries(changes)) {
          if (v) n.set(k, v)
          else n.delete(k)
        }
        return n
      },
      { replace: true },
    )
  const filtered = !!(q || state || item)
  const items = data && cat.item ? itemsOf(data, cat.value) : []

  return (
    <div>
      <PageHeader title="Fleet" subtitle="What goes on clusters: Helm charts, git paths and manifests, and where each goes.">
        {can(TYPES.FLEET_HELM_OP, 'create') && (
          <Link className="btn btn-primary" to={paths.newDeployment()}>
            New deployment
          </Link>
        )}
      </PageHeader>
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="w-full sm:w-52">
          <label className="label" htmlFor="fleet-category">
            Show
          </label>
          <select id="fleet-category" value={cat.value} onChange={(e) => set({ category: e.target.value === 'all' ? '' : e.target.value, item: '' })}>
            {CATEGORIES.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
                {data && ` (${data.deployments.filter((d) => inCategory(data, d, c.value)).length})`}
              </option>
            ))}
          </select>
        </div>
        {cat.item && (
          <div className="w-full sm:w-52">
            <label className="label" htmlFor="fleet-item">
              {cat.item}
            </label>
            <select id="fleet-item" value={item} onChange={(e) => set({ item: e.target.value })}>
              <option value="">Every {cat.item.toLowerCase()}</option>
              {items.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
        )}
        <TextField className="w-full sm:w-56" label="Search" placeholder="Name or source" value={q} onChange={(v) => set({ q: v })} />
        <div className="w-full sm:w-40">
          <label className="label" htmlFor="fleet-state">
            State
          </label>
          <select id="fleet-state" value={state} onChange={(e) => set({ state: e.target.value })}>
            <option value="">Any</option>
            <option value="ready">Ready</option>
            <option value="problem">Not ready</option>
            <option value="paused">Paused</option>
          </select>
        </div>
        {filtered && (
          <button type="button" className="btn btn-tertiary" onClick={() => set({ q: '', state: '', item: '' })}>
            Clear
          </button>
        )}
        {cat.value === 'groups' && can(CLUSTER_GROUP, 'create') && !adding && (
          <button type="button" className="btn btn-secondary ml-auto" onClick={() => setAdding(true)}>
            New group
          </button>
        )}
      </div>
      <ErrorBanner error={error} />
      {/* While adding, the dialog shows it. */}
      {!adding && <ErrorBanner error={act.error} />}
      {cat.value === 'groups' && adding && (
        <NewGroup
          busy={act.busy}
          error={act.error}
          onCancel={() => {
            setAdding(false)
            act.setError('')
          }}
          onCreate={(name, labels) =>
            act.run(async () => {
              await save(CLUSTER_GROUP, newObject(CLUSTER_GROUP, { metadata: { name, namespace: DEFAULT_TEMPLATE_NAMESPACE }, spec: { selector: { matchLabels: labels } } }))
              setAdding(false)
              await reload(true)
            })
          }
        />
      )}
      {loading || !data ? (
        <Loading />
      ) : (
        <DeploymentTable
          dl={data}
          rows={data.deployments.filter((d) => matches(d, q, state))}
          category={cat.value}
          item={item}
          filtered={filtered}
          page={page}
          pageSize={pageSize}
          onPage={(p) => set({ page: p ? String(p + 1) : '' })}
          onPageSize={(n) => {
            setPageSize(n)
            set({})
            try {
              localStorage.setItem(PAGE_SIZE_KEY, String(n))
            } catch {
              // No storage: the size lasts until the page reloads.
            }
          }}
          groupAction={(g, aimed) =>
            can(CLUSTER_GROUP, 'delete') && (
              <ConfirmButton
                label="Delete group"
                confirm={`Delete group ${g.metadata.name}?${aimed ? ` ${aimed} deployment(s) target it and would reach no cluster through it.` : ''}`}
                disabled={act.busy}
                onConfirm={() =>
                  act.run(async () => {
                    await remove(CLUSTER_GROUP, g)
                    await reload(true)
                  })
                }
              />
            )
          }
        />
      )}
    </div>
  )
}

const PAGE_SIZES = [25, 50, 100]
const PAGE_SIZE_KEY = 'hyve.fleet.pageSize'

function readPageSize(): number {
  try {
    const n = Number(localStorage.getItem(PAGE_SIZE_KEY))
    return PAGE_SIZES.includes(n) ? n : PAGE_SIZES[0]
  } catch {
    return PAGE_SIZES[0]
  }
}

type Row = { d: Deployment; here?: ClusterState | null }
type Section = { key: string; header: ReactNode; rows: Row[]; empty?: string; action?: ReactNode }

// One page of sections: pageSize rows, an empty section counting as one. A
// section split across pages repeats its header, marked continued.
function pageOf(sections: Section[], page: number, size: number): { pages: number; page: number; total: number; first: number; shown: (Section & { continued: boolean })[] } {
  const units: { s: Section; row?: Row; i: number }[] = sections.flatMap((s) => (s.rows.length ? s.rows.map((row, i) => ({ s, row, i })) : [{ s, i: 0 }]))
  const pages = Math.max(1, Math.ceil(units.length / size))
  const cur = Math.min(page, pages - 1)
  const shown: (Section & { continued: boolean })[] = []
  for (const u of units.slice(cur * size, (cur + 1) * size)) {
    let last = shown[shown.length - 1]
    if (!last || last.key !== u.s.key) {
      last = { ...u.s, rows: [], continued: u.i > 0 }
      shown.push(last)
    }
    if (u.row) last.rows.push(u.row)
  }
  return { pages, page: cur, total: units.length, first: cur * size, shown }
}

// A section per template, environment, class or group (only `item`, if
// given): what's aimed at it. Unfiltered, those with nothing aimed at them
// show too.
function targetSections(dl: Delivery, rows: Deployment[], c: Category, item: string, filtered: boolean, groupAction?: (g: Obj, aimed: number) => ReactNode): Section[] {
  const kind = CATEGORIES.find((x) => x.value === c)!.kind!
  const names = item ? [item] : itemsOf(dl, c).map((o) => o.value)
  // Aimed at one this list doesn't know (a group since deleted): still shown.
  for (const d of rows) for (const t of d.targets) if (t.kind === kind && !item && !names.includes(t.name!)) names.push(t.name!)
  return names
    .map((name): Section => {
      const t: Target = { kind, name }
      const here = rows.filter((d) => d.targets.some((x) => x.kind === kind && x.name === name))
      const g = kind === 'group' ? dl.groups.find((x) => x.metadata.namespace === DEFAULT_TEMPLATE_NAMESPACE && x.metadata.name === name) : undefined
      const tpl = kind === 'template' ? dl.templates.find((x) => x.metadata.name === name) : undefined
      return {
        key: targetKey(t),
        header: (
          <>
            <span className="font-semibold">{tpl ? <Link to={paths.template(tpl.metadata.namespace!, tpl.metadata.name)}>{targetLabel(t)}</Link> : targetLabel(t)}</span>
            {g && (
              <span className="text-muted ml-2 text-xs">
                {Object.entries(g.spec?.selector?.matchLabels || {}).map(([lk, lv]) => (
                  <code key={lk} className="mr-1">
                    {lk}={String(lv)}
                  </code>
                ))}
              </span>
            )}
            <span className="text-muted ml-3 text-xs font-normal">
              <Members clusters={dl.clusters.filter((x) => reaches(t, x, dl.groups))} />
            </span>
          </>
        ),
        rows: here.map((d) => ({ d })),
        empty: 'Nothing is aimed here.',
        action: g && groupAction?.(g, here.length),
      }
    })
    .filter((s) => s.rows.length || (!filtered && !item) || item)
}

// A section per cluster (only `item`, if given): what reached it, and its
// state there.
function clusterSections(dl: Delivery, rows: Deployment[], item: string, filtered: boolean): Section[] {
  const ids = item ? [item] : itemsOf(dl, 'clusters').map((o) => o.value)
  return ids
    .map((id): Section => {
      const here = rows.filter((d) => reachesCluster(dl, d, id))
      const [cns, cname] = id.split('/')
      return {
        key: id,
        header: (
          <>
            <span className="font-semibold">
              {id === HOST_ID ? <Link to={paths.localCluster('deployments')}>local (host)</Link> : <Link to={paths.cluster(cns, cname, 'deployments')}>{id}</Link>}
            </span>
            {dl.detailed && here.length > 0 && (
              <span className="text-muted ml-3 text-xs">
                {here.filter((d) => d.states.find((s) => s.cluster === id)?.state === 'Ready').length}/{here.length} ready here
              </span>
            )}
          </>
        ),
        rows: here.map((d) => ({ d, here: d.states.find((s) => s.cluster === id) || null })),
        empty: 'Nothing is deployed here.',
      }
    })
    .filter((s) => s.rows.length || item || (!filtered && s.key !== HOST_ID))
}

function DeploymentTable({
  dl,
  rows,
  category,
  item,
  filtered,
  page,
  pageSize,
  onPage,
  onPageSize,
  groupAction,
}: {
  dl: Delivery
  rows: Deployment[]
  category: Category
  item: string
  filtered: boolean
  page: number
  pageSize: number
  onPage: (p: number) => void
  onPageSize: (n: number) => void
  groupAction: (g: Obj, aimed: number) => ReactNode
}) {
  const navigate = useNavigate()
  if (!dl.deployments.length)
    return (
      <EmptyState>
        Nothing deployed yet. A deployment delivers a Helm chart, git paths or manifests to the clusters of a template, a class, an environment, or
        one cluster.
      </EmptyState>
    )
  const flat = (rs: Deployment[]): Section[] => [{ key: '', header: null, rows: rs.map((d) => ({ d })) }]
  const sections: Section[] =
    category === 'clusters'
      ? clusterSections(dl, rows, item, filtered)
      : category === 'none'
        ? flat(rows.filter((d) => reachesNoCluster(dl, d)))
        : category === 'all'
          ? flat(rows)
          : targetSections(dl, rows, category, item, filtered, groupAction)
  if (!sections.some((s) => s.rows.length) && !sections.some((s) => s.header)) {
    return <EmptyState>{category === 'none' && !filtered ? 'Every deployment reaches a cluster.' : 'No deployment matches.'}</EmptyState>
  }
  const perCluster = category === 'clusters' && dl.detailed
  const pg = pageOf(sections, page, pageSize)
  return (
    <div className="overflow-x-auto">
      {category === 'none' && <p className="text-muted mt-0 mb-3 text-sm">Their targets select no cluster yet, or Fleet hasn't made their bundles.</p>}
      {category === 'clusters' && !dl.detailed && <p className="text-muted mt-0 mb-3 text-sm">Each cluster's own state needs access to Fleet's per-cluster namespaces; the states shown are overall.</p>}
      <Table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Source</th>
            <th>Delivered to</th>
            <th>Rollout</th>
            <th>Clusters ready</th>
            <th>{perCluster ? 'State here' : 'State'}</th>
          </tr>
        </thead>
        {pg.shown.map((s) => (
          <tbody key={s.key}>
            {s.header && (
              <tr>
                <td colSpan={6} className="!border-t !border-neutral-200 !bg-neutral-50 dark:!border-neutral-700 dark:!bg-neutral-800/60">
                  <div className="flex flex-wrap items-center gap-x-3">
                    <div className="min-w-0 flex-1">
                      {s.header}
                      {s.continued && <span className="text-muted ml-3 text-xs italic">continued</span>}
                      {!s.rows.length && <span className="text-muted ml-3 text-xs italic">{s.empty}</span>}
                    </div>
                    {s.action}
                  </div>
                </td>
              </tr>
            )}
            {s.rows.map(({ d, here }) => {
              const r = readiness(d)
              const overall = here === undefined || !dl.detailed
              const state = overall ? r.state : here?.state || 'Not yet'
              const message = overall ? r.message : here?.message || ''
              return (
                <tr key={`${d.namespace}/${d.name}`} className="cursor-pointer" onClick={() => navigate(paths.deployment(d.namespace, d.name))}>
                  <td className="min-w-40 break-words">
                    <Link to={paths.deployment(d.namespace, d.name)} onClick={(e) => e.stopPropagation()}>
                      {d.name}
                    </Link>
                    {d.namespace !== DEFAULT_TEMPLATE_NAMESPACE && <div className="text-muted text-xs">{d.namespace === HOST_WORKSPACE ? 'host' : `in ${d.namespace}`}</div>}
                  </td>
                  <td className="max-w-80 break-all">
                    <span className="text-muted text-xs">{SOURCE_LABEL[d.sourceKind]}</span> {d.source}
                  </td>
                  <td>
                    <div className="flex flex-wrap gap-1">
                      {d.targets.map((t) => (
                        <Pill key={targetKey(t)} tone="info" label={targetLabel(t)} />
                      ))}
                    </div>
                  </td>
                  <td className="text-sm">{ROLLOUTS.find((x) => x.value === d.rollout)?.label || (d.sourceKind === 'git' ? 'fleet.yaml' : 'Custom')}</td>
                  <td>{r.ready || '-'}</td>
                  <td>
                    {d.paused && <Pill tone="busy" label="Paused" />} {state && <Pill tone={state === 'Not yet' ? 'idle' : stateTone(state)} label={state} />}
                    {message && <div className="text-muted mt-1 line-clamp-2 max-w-md text-xs">{message}</div>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        ))}
      </Table>
      {pg.total > PAGE_SIZES[0] && (
        <div className="mt-3 flex flex-wrap items-center justify-end gap-2 text-sm">
          <select className="mr-auto w-auto" value={pageSize} onChange={(e) => onPageSize(Number(e.target.value))} aria-label="Rows per page">
            {PAGE_SIZES.map((n) => (
              <option key={n} value={n}>
                {n} per page
              </option>
            ))}
          </select>
          <span className="text-muted mr-1">
            {pg.first + 1}–{Math.min(pg.first + pageSize, pg.total)} of {pg.total}
          </span>
          <button type="button" className="btn btn-secondary btn-sm" disabled={pg.page === 0} onClick={() => onPage(pg.page - 1)}>
            Previous
          </button>
          <button type="button" className="btn btn-secondary btn-sm" disabled={pg.page >= pg.pages - 1} onClick={() => onPage(pg.page + 1)}>
            Next
          </button>
        </div>
      )}
    </div>
  )
}
