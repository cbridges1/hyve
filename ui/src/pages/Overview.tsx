// Every hyve cluster with its provider, template, environment, phase and
// expiry; resources failing anywhere; and clusters expiring soon.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApplyYaml } from '../components/ApplyYaml'
import { EnvironmentPicker } from '../components/EnvironmentPicker'
import { ErrorBanner, Loading, PageHeader } from '../components/ui'
import { classOf, extendExpiry, providerOfClass } from '../lib/clusters'
import { useEnvironment } from '../lib/environment'
import { useAction, useLoad } from '../lib/hooks'
import { idOf, type Obj } from '../lib/kube'
import { allClusterStates, allItems, problem, stateColor, stateOn } from '../lib/resources'
import { paths } from '../lib/routes'
import { ANNOTATIONS, LABELS, NEVER, TYPES } from '../lib/types'

const SOON_HOURS = 24

function Stat({ count, label, tone = '' }: { count: number; label: string; tone?: string }) {
  return (
    <div className="min-w-[160px] rounded-xl border border-neutral-200 bg-white px-5 py-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
      <div className={`text-3xl leading-tight font-semibold ${tone}`}>{count}</div>
      <div className="text-sm text-neutral-500">{label}</div>
    </div>
  )
}

export default function Overview() {
  const [environment] = useEnvironment()
  const [applying, setApplying] = useState(false)
  const action = useAction()
  const { data, error, loading, reload } = useLoad(async (r) => {
    const [all, states, classes] = await Promise.all([
      allItems(r),
      allClusterStates(r),
      r.findAll(TYPES.CLUSTER_CLASS).catch(() => [] as Obj[]),
    ])
    return { ...all, states, classes, now: Date.now() }
  }, [])

  const view = useMemo(() => {
    if (!data) return null
    const { clusters, templates, classes, items, reaches, states, now } = data
    const inEnv = (c: Obj) => !environment || c.metadata.namespace === environment
    const shown = clusters.filter(inEnv)
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
    // Each failing resource, with the clusters it's failing on.
    const failing = items.flatMap((it) => {
      const own = problem(it.model)
      const on = reaches(it)
        .filter(inEnv)
        .map((c) => ({ c, state: stateOn(it, states[`${c.metadata.namespace}/${c.metadata.name}`] || null) }))
        .filter((x) => x.state && x.state !== 'Ready')
      return own || on.length ? [{ it, own, on }] : []
    })
    return { shown, rows, failing, expiringSoon: rows.filter((r) => r.soon) }
  }, [data, environment])

  if (loading) return <Loading />

  return (
    <div>
      <PageHeader title="Overview">
        <EnvironmentPicker />
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
            <Stat count={view.shown.length} label="clusters" />
            <Stat count={view.failing.length} label="resources failing" tone={view.failing.length ? 'text-error' : 'text-success'} />
            <Stat count={view.expiringSoon.length} label="expiring within a day" tone={view.expiringSoon.length ? 'text-warning' : ''} />
          </div>

          {view.failing.length > 0 && (
            <>
              <h2>Failing resources</h2>
              <table className="table mb-5">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>From</th>
                    <th>Where it's failing</th>
                  </tr>
                </thead>
                <tbody>
                  {view.failing.map((f) => (
                    <tr key={`${idOf(f.it.model)}/${f.it.kind}`}>
                      <td>{f.it.name}</td>
                      <td>
                        {f.it.ownerKind === 'ClusterTemplate' ? (
                          <Link to={paths.template(f.it.model.metadata.namespace!, f.it.owner)}>Template {f.it.owner}</Link>
                        ) : (
                          <span>This cluster</span>
                        )}
                      </td>
                      <td>
                        {f.own && <div className="text-error">{f.own}</div>}
                        {f.on.map((x) => (
                          <div key={idOf(x.c)}>
                            <Link to={paths.cluster(x.c.metadata.namespace!, x.c.metadata.name)}>{idOf(x.c)}</Link>:{' '}
                            <span className={stateColor(x.state)}>{x.state}</span>
                          </div>
                        ))}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}

          <h2>Clusters</h2>
          <table className="table">
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
              {!view.rows.length && (
                <tr>
                  <td colSpan={7} className="text-muted">
                    No clusters yet.
                  </td>
                </tr>
              )}
              {view.rows.map((r) => (
                <tr key={idOf(r.cluster)}>
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
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  )
}
