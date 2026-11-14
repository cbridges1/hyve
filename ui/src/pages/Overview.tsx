// Every cluster the user can see, in every environment (the overview
// isn't filtered by one), with its provider, template, environment, phase
// and expiry, the host cluster (local) first for whoever can reach it; each
// running cluster's CPU, memory and pods (components/ResourceMonitor.tsx);
// Deployments failing anywhere (the host's included); and clusters
// expiring soon.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApplyYaml } from '../components/ApplyYaml'
import { ResourceSummaryCells } from '../components/ResourceMonitor'
import { ErrorBanner, Loading, PageHeader } from '../components/ui'
import { classOf, extendExpiry, providerOfClass } from '../lib/clusters'
import { useAction, useLoad } from '../lib/hooks'
import { LOCAL_CLUSTER } from '../lib/explorer'
import { idOf, type Obj } from '../lib/kube'
import { HOST_WORKSPACE, loadDelivery, readiness } from '../lib/delivery'
import { stateColor } from '../lib/resources'
import { paths } from '../lib/routes'
import { useSession } from '../lib/session'
import { ANNOTATIONS, LABELS, NEVER, TYPES } from '../lib/types'
import { LinkRow, Table } from '../components/Table'

const SOON_HOURS = 24
// Clusters whose resources the overview reads at once (each every 30s).
const MAX_MONITORED = 25

function Stat({ count, label, tone = '' }: { count: number; label: string; tone?: string }) {
  return (
    <div className="min-w-[160px] rounded-xl border border-neutral-200 bg-white px-5 py-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
      <div className={`text-3xl leading-tight font-semibold ${tone}`}>{count}</div>
      <div className="text-sm text-neutral-500">{label}</div>
    </div>
  )
}

export default function Overview() {
  const [applying, setApplying] = useState(false)
  const { canExplore } = useSession()
  const showLocal = canExplore
  const action = useAction()
  const { data, error, loading, reload } = useLoad(async (r) => {
    const [dl, classes] = await Promise.all([loadDelivery(r), r.findAll(TYPES.CLUSTER_CLASS).catch(() => [] as Obj[])])
    return { dl, clusters: dl.clusters, templates: dl.templates, classes, now: Date.now() }
  }, [])

  const view = useMemo(() => {
    if (!data) return null
    const { clusters, templates, classes, dl, now } = data
    const shown = clusters
    const rows = shown.map((c) => {
      const l = c.metadata.labels || {}
      const t = templates.find(
        (x) => x.metadata.name === l[LABELS.TEMPLATE] && x.metadata.namespace === (l[LABELS.TEMPLATE_NAMESPACE] || c.metadata.namespace),
      )
      const ref = c.spec?.topology?.classRef
      const cls =
        classes.find((x) => x.metadata.name === ref?.name && x.metadata.namespace === (ref?.namespace || c.metadata.namespace)) ||
        classOf(t, classes)
      const expires = c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT] || ''
      const at = expires && expires !== NEVER ? Date.parse(expires) : NaN
      return {
        cluster: c,
        provider: providerOfClass(cls) || '-',
        template: l[LABELS.TEMPLATE] || '-',
        phase: c.status?.phase || '-',
        expires: Number.isNaN(at) ? '' : new Date(at).toLocaleString(),
        soon: !Number.isNaN(at) && at - now < SOON_HOURS * 3600 * 1000,
      }
    })
    // Each failing Deployment, with the clusters shown (and the host) it's
    // failing on.
    const shownIds = new Set(shown.map(idOf))
    const failing = dl.deployments.flatMap((d) => {
      const { state, message } = readiness(d)
      const on = d.states
        .filter((x) => x.state !== 'Ready' && (x.cluster.startsWith(`${HOST_WORKSPACE}/`) || shownIds.has(x.cluster)))
        .map((x) => ({ ...x, c: clusters.find((c) => idOf(c) === x.cluster) }))
      const own = !d.states.length && state && state !== 'Ready' && state !== 'NoTargets' ? `${state}${message ? `: ${message}` : ''}` : ''
      return own || on.length ? [{ d, own, on }] : []
    })
    // Running clusters, whose API can be read for the resource summary.
    const live = shown.filter((c) => c.status?.phase === 'Provisioned' && !c.metadata.deletionTimestamp)
    return { shown, rows, failing, live, expiringSoon: rows.filter((r) => r.soon) }
  }, [data])

  if (loading) return <Loading />

  return (
    <div>
      <PageHeader title="Overview">
        <button type="button" className="btn btn-secondary" onClick={() => setApplying(true)}>
          Apply YAML
        </button>
      </PageHeader>
      {applying && <ApplyYaml onClose={() => setApplying(false)} onApplied={() => reload(true)} />}
      <ErrorBanner error={error} />
      <ErrorBanner error={action.error} />

      {view && (
        <>
          <div className="mb-5 flex flex-wrap gap-5">
            <Stat count={view.shown.length + (showLocal ? 1 : 0)} label="clusters" />
            <Stat
              count={view.failing.length}
              label="deployments failing"
              tone={view.failing.length ? 'text-error' : 'text-success'}
            />
            <Stat count={view.expiringSoon.length} label="expiring within a day" tone={view.expiringSoon.length ? 'text-warning' : ''} />
          </div>

          {view.failing.length > 0 && (
            <>
              <h2>Failing deployments</h2>
              <div className="overflow-x-auto mb-5">
                <Table>
                  <thead>
                    <tr>
                      <th>Deployment</th>
                      <th>Where it's failing</th>
                    </tr>
                  </thead>
                  <tbody>
                    {view.failing.map((f) => (
                      <tr key={`${f.d.namespace}/${f.d.name}`}>
                        <td>
                          <Link to={paths.deployment(f.d.namespace, f.d.name)}>{f.d.name}</Link>
                          <div className="text-muted text-xs">{f.d.source}</div>
                        </td>
                        <td>
                          {f.own && <div className="text-error">{f.own}</div>}
                          {f.on.map((x) => (
                            <div key={x.cluster}>
                              {x.c ? (
                                <Link to={paths.cluster(x.c.metadata.namespace!, x.c.metadata.name, 'deployments')}>{x.cluster}</Link>
                              ) : x.cluster.startsWith(`${HOST_WORKSPACE}/`) ? (
                                <Link to={paths.localCluster('deployments')}>local</Link>
                              ) : (
                                <span>{x.cluster}</span>
                              )}
                              : <span className={stateColor(x.state)}>{x.state}</span>
                              {x.message && (
                                <div className="text-muted line-clamp-2 max-w-xl text-xs" title={x.message}>
                                  {x.message}
                                </div>
                              )}
                            </div>
                          ))}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </Table>
              </div>
            </>
          )}

          {(showLocal || view.live.length > 0) && (
            <>
              <h2>Resources</h2>
              <div className="overflow-x-auto mb-5">
                <Table>
                  <thead>
                    <tr>
                      <th>Cluster</th>
                      <th>CPU</th>
                      <th>Memory</th>
                      <th>Pods</th>
                    </tr>
                  </thead>
                  <tbody>
                    {showLocal && (
                      <LinkRow to={paths.localCluster()}>
                        <td>
                          <Link to={paths.localCluster()}>local</Link>
                        </td>
                        <ResourceSummaryCells clusterId={LOCAL_CLUSTER} />
                      </LinkRow>
                    )}
                    {view.live.slice(0, MAX_MONITORED).map((c) => (
                      <LinkRow key={idOf(c)} to={paths.cluster(c.metadata.namespace!, c.metadata.name)}>
                        <td>
                          <Link to={paths.cluster(c.metadata.namespace!, c.metadata.name)}>{c.metadata.name}</Link>
                          <span className="text-muted ml-1 text-xs">{c.metadata.namespace}</span>
                        </td>
                        <ResourceSummaryCells clusterId={`${c.metadata.namespace}.${c.metadata.name}`} />
                      </LinkRow>
                    ))}
                  </tbody>
                </Table>
              </div>
              {view.live.length > MAX_MONITORED && (
                <p className="hint -mt-3 mb-5">
                  The first {MAX_MONITORED} of {view.live.length} running clusters; open a cluster for its own.
                </p>
              )}
            </>
          )}

          <h2>Clusters</h2>
          <div className="overflow-x-auto">
            <Table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Environment</th>
                  <th>Provider</th>
                  <th>Template</th>
                  <th>Phase</th>
                  <th>Expires</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {showLocal && (
                  <LinkRow to={paths.localCluster()}>
                    <td>
                      <Link to={paths.localCluster()}>local</Link>
                    </td>
                    <td className="text-muted">-</td>
                    <td>Host</td>
                    <td className="text-muted">-</td>
                    <td className="text-muted">-</td>
                    <td>Never</td>
                    <td />
                  </LinkRow>
                )}
                {!view.rows.length && !canExplore && (
                  <tr>
                    <td colSpan={7} className="text-muted">
                      No clusters yet.
                    </td>
                  </tr>
                )}
                {view.rows.map((r) => (
                  <LinkRow key={idOf(r.cluster)} to={paths.cluster(r.cluster.metadata.namespace!, r.cluster.metadata.name)}>
                    <td>
                      <Link to={paths.cluster(r.cluster.metadata.namespace!, r.cluster.metadata.name)}>{r.cluster.metadata.name}</Link>
                    </td>
                    <td>{r.cluster.metadata.namespace}</td>
                    <td>{r.provider}</td>
                    <td>{r.template}</td>
                    <td>{r.phase}</td>
                    <td className={r.soon ? 'text-warning' : ''}>{r.expires || 'Never'}</td>
                    <td className="text-right">
                      {r.expires && (
                        <button
                          type="button"
                          className="btn btn-tertiary btn-sm"
                          disabled={action.busy}
                          onClick={() =>
                            action.run(async () => {
                              await extendExpiry(r.cluster)
                              await reload(true)
                            })
                          }
                        >
                          Extend expiry by 8 hours
                        </button>
                      )}
                    </td>
                  </LinkRow>
                ))}
              </tbody>
            </Table>
          </div>
        </>
      )}
    </div>
  )
}
