// The console's cluster list: one row per cluster, its environment,
// expiry and state as pills; a row opens the cluster.
import { Link, useNavigate } from 'react-router-dom'
import { EnvironmentPicker } from '../components/EnvironmentPicker'
import { ErrorBanner, Loading, PageHeader, Pill, StatusPill } from '../components/ui'
import { useEnvironment } from '../lib/environment'
import { clusterStatus, expiry } from '../lib/format'
import { useLoad } from '../lib/hooks'
import { idOf } from '../lib/kube'
import { paths } from '../lib/routes'
import { ANNOTATIONS, LABELS, TYPES } from '../lib/types'

export default function ClusterList() {
  const [environment] = useEnvironment()
  const navigate = useNavigate()
  const { data: clusters, error, loading } = useLoad(
    async (r) => (await r.findAll(TYPES.CLUSTER, { force: true })).sort((a, b) => idOf(a).localeCompare(idOf(b))),
    [],
  )

  const rows = (clusters || [])
    .filter((c) => !environment || c.metadata.namespace === environment)
    .map((c) => {
      const l = c.metadata.labels || {}
      const topo = c.spec?.topology || {}
      const workers = [...(topo.workers?.machineDeployments || []), ...(topo.workers?.machinePools || [])].reduce(
        (n: number, w: any) => n + (w.replicas || 0),
        0,
      )
      return {
        c,
        sub: [
          l[LABELS.TEMPLATE] ? `template ${l[LABELS.TEMPLATE]}` : '',
          topo.classRef?.name || topo.class,
          topo.version,
          `${workers} worker${workers === 1 ? '' : 's'}`,
        ]
          .filter(Boolean)
          .join(' · '),
        status: clusterStatus(c),
        expires: expiry(c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT]),
      }
    })

  return (
    <div>
      <PageHeader title="Clusters">
        <EnvironmentPicker />
        <Link className="btn btn-primary" to={paths.createCluster()}>
          New cluster
        </Link>
      </PageHeader>
      <ErrorBanner error={error} />
      {loading ? (
        <Loading />
      ) : (
        <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-sm dark:border-neutral-800 dark:bg-neutral-900">
          {!rows.length && <p className="p-6 text-center text-sm text-neutral-500">No clusters yet.</p>}
          <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
            {rows.map((r) => {
              const to = paths.cluster(r.c.metadata.namespace!, r.c.metadata.name)
              return (
                <div
                  key={idOf(r.c)}
                  role="link"
                  tabIndex={0}
                  className="flex cursor-pointer flex-col gap-2 px-4 py-3 transition-colors hover:bg-neutral-50 sm:flex-row sm:items-center sm:justify-between dark:hover:bg-neutral-800/50"
                  onClick={() => navigate(to)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') navigate(to)
                  }}
                >
                  <div className="min-w-0">
                    <div className="font-medium text-neutral-900 dark:text-neutral-100">{r.c.metadata.name}</div>
                    <div className="truncate text-xs text-neutral-500">{r.sub}</div>
                  </div>
                  <div className="flex shrink-0 items-center gap-3">
                    <Pill tone="env" label={r.c.metadata.namespace!} />
                    <StatusPill status={r.expires} />
                    <StatusPill status={r.status} />
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
