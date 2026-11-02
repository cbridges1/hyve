// The host cluster, local: hyve's management cluster, where hyve, Cluster
// API and Fleet run. It's listed with the clusters, as Rancher lists its
// own, and reached the same way (kubeconfig, shell, explorer), but it's in
// no environment and hyve doesn't create, scale, upgrade or delete it:
// 'hyve install' changes what runs on it. Its Resources tab is the git
// repos that deploy to it, managed as on the Repos page, and their
// bundles; hyve's own resources go to environments' clusters.
import { lazy, Suspense } from 'react'
import { useParams } from 'react-router-dom'
import { GitSection } from '../components/GitBundles'
import { BackLink, Banner, Card, ErrorBanner, Field, Loading, Pill, Tabs } from '../components/ui'
import { request } from '../lib/api'
import { LOCAL_CLUSTER, clusterBase } from '../lib/explorer'
import { download, kubeconfig } from '../lib/format'
import { useAction, useLoad } from '../lib/hooks'
import { type Obj } from '../lib/kube'
import { openShell } from '../lib/kubectlShell'
import { LOCAL_FLEET_CLUSTER, repoBundles } from '../lib/repos'
import { FLEET } from '../lib/resources'
import { paths } from '../lib/routes'
import { useSession } from '../lib/session'

const Explorer = lazy(() => import('../components/explorer/Explorer').then((m) => ({ default: m.Explorer })))

const nodeReady = (n: Obj) => (n.status?.conditions || []).some((c: any) => c.type === 'Ready' && c.status === 'True')

export default function LocalCluster() {
  const { tab } = useParams()
  const { canExplore } = useSession()
  const action = useAction()
  const { data, error, loading } = useLoad(
    async (r) => {
      const [version, nodes, fleet, bundles] = await Promise.all([
        request('GET', `${clusterBase(LOCAL_CLUSTER)}/version`).catch(() => null),
        r.findAll('node').catch(() => null),
        r.maybe(FLEET.CLUSTER, 'fleet-local', 'local').catch(() => null),
        repoBundles(r),
      ])
      return { version: version?.gitVersion as string | undefined, nodes, fleet, bundles: bundles.filter((g) => g.on[LOCAL_FLEET_CLUSTER]) }
    },
    [],
  )

  const tabs = [
    { label: 'Overview', to: paths.localCluster(), active: !tab },
    { label: 'Resources', to: paths.localCluster('resources'), active: tab === 'resources' },
    { label: 'Explorer', to: paths.localCluster('explorer'), active: tab === 'explorer' },
  ]
  const downloadKubeconfig = () => action.run(async () => download('local.kubeconfig', await kubeconfig(LOCAL_CLUSTER)))

  if (!canExplore)
    return (
      <div>
        <BackLink to={paths.clusters} label="Clusters" />
        <Banner tone="warning">You don't have access to the host cluster.</Banner>
      </div>
    )
  if (loading) return <Loading />

  const nodes = data?.nodes
  const fleetReady = data?.fleet?.status?.display?.readyBundles

  return (
    <div className="space-y-4">
      <div>
        <BackLink to={paths.clusters} label="Clusters" />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <h1 className="mb-0 truncate">local</h1>
            <Pill tone="info" label="Host" title="hyve's management cluster" />
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="btn btn-secondary"
              title="kubectl, helm and k9s in your browser, as you, on the host cluster"
              onClick={() => openShell({ cluster: LOCAL_CLUSTER, label: 'local' })}
            >
              Kubectl shell
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              title="A kubeconfig that runs 'hyve token' for your CLI session; it holds no secret"
              onClick={downloadKubeconfig}
            >
              Kubeconfig
            </button>
          </div>
        </div>
      </div>
      <ErrorBanner error={error} />
      <ErrorBanner error={action.error} />

      <Tabs tabs={tabs} />

      {tab === 'resources' ? (
        <Card>
          <p className="text-muted mb-0 text-sm">Only git repos deliver to the host cluster; hyve's own resources go to environments' clusters.</p>
          <GitSection fleetId={LOCAL_FLEET_CLUSTER} scope={{ workspace: 'fleet-local', fleetId: LOCAL_FLEET_CLUSTER }} bundles={data?.bundles || []} backLabel="local" />
        </Card>
      ) : tab === 'explorer' ? (
        <Suspense fallback={<Loading />}>
          <Explorer clusterId={LOCAL_CLUSTER} />
        </Suspense>
      ) : (
        <Card title="Details">
          <Field label="Role">Host: hyve, Cluster API and Fleet run here</Field>
          <Field label="Kubernetes">{data?.version || '—'}</Field>
          <Field label="Nodes">{nodes ? `${nodes.filter(nodeReady).length}/${nodes.length} ready` : '—'}</Field>
          <Field label="Fleet">{fleetReady ? `${fleetReady} bundles ready` : data?.fleet === null ? 'Not registered' : '—'}</Field>
          <Field label="CLI">
            <code>hyve cluster auth local</code>
          </Field>
          <p className="hint mt-3">
            hyve doesn't create, scale, upgrade or delete the cluster it runs on. <code>hyve install</code> changes what hyve runs here.
          </p>
        </Card>
      )}
    </div>
  )
}
