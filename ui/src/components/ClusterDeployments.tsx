// A cluster's Deployments tab: every Deployment that reaches it, why
// (through its template, class, environment, a group, or directly), and
// its state here. "Deploy to this cluster" starts one aimed at it alone.
// Then the Secrets its provider and its environment's Deployments need.
import { Fragment, useState } from 'react'
import { Link } from 'react-router-dom'
import { forCluster, HOST_WORKSPACE, loadDelivery, stateTone, targetArg, type Target } from '../lib/delivery'
import { useLoad } from '../lib/hooks'
import { can, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { clusterSecrets } from '../lib/secrets'
import { LABELS, TYPES } from '../lib/types'
import { BundleDetail } from './BundleDetail'
import { Icon } from './Icon'
import { SecretsSection } from './SecretsSection'
import { Banner, EmptyState, ErrorBanner, Loading, Pill } from './ui'

const SOURCE_LABEL = { git: 'Git', helm: 'Helm', manifests: 'Manifests' }

export function ClusterDeployments({ cluster }: { cluster: Obj | 'host' }) {
  const host = cluster === 'host'
  const ns = host ? HOST_WORKSPACE : cluster.metadata.namespace!
  const name = host ? 'local' : cluster.metadata.name
  const [open, setOpen] = useState<Set<string>>(new Set())
  const { data, error, loading } = useLoad(
    async (r) => {
      const dl = await loadDelivery(r)
      return { dl, rows: forCluster(dl, cluster) }
    },
    // The labels decide which groups it's in.
    [ns, name, host ? '' : JSON.stringify(cluster.metadata.labels || {})],
  )
  if (loading) return <Loading />
  const rows = data?.rows || []
  const fleetId = host ? 'fleet-local/local' : `${ns}/${name}`
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  const tpl = host ? '' : cluster.metadata.labels?.[LABELS.TEMPLATE]
  const here: Target = host ? { kind: 'host' } : { kind: 'cluster', env: ns, name }

  return (
    <div>
      <ErrorBanner error={error} />
      {data && !data.dl.detailed && <Banner>Each Deployment's state here needs access to Fleet's per-cluster namespaces; its own page has its totals.</Banner>}
      {!rows.length ? (
        <EmptyState>Nothing is deployed here yet.</EmptyState>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th />
              <th>Deployment</th>
              <th>Source</th>
              <th>Why it's here</th>
              <th>State here</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ d, why, state }) => {
              const id = `${d.namespace}/${d.name}`
              const bds = d.bds.filter((bd) => `${bd.metadata.labels?.['fleet.cattle.io/cluster-namespace']}/${bd.metadata.labels?.['fleet.cattle.io/cluster']}` === fleetId)
              return (
                <Fragment key={id}>
                  <tr className="cursor-pointer" onClick={() => toggle(id)}>
                    <td className="w-6">
                      <span className={`inline-block transition-transform ${open.has(id) ? 'rotate-90' : ''}`}>
                        <Icon name="chevronRight" size={14} />
                      </span>
                    </td>
                    <td>
                      <Link to={paths.deployment(d.namespace, d.name)} onClick={(e) => e.stopPropagation()}>
                        {d.name}
                      </Link>
                      {d.paused && (
                        <span className="ml-1">
                          <Pill tone="busy" label="Paused" />
                        </span>
                      )}
                    </td>
                    <td className="max-w-80 break-all">
                      <span className="text-muted text-xs">{SOURCE_LABEL[d.sourceKind]}</span> {d.source}
                    </td>
                    <td className="text-sm">{why.join(', ') || <span className="text-muted">no longer targeted</span>}</td>
                    <td>
                      {state ? <Pill tone={stateTone(state.state)} label={state.state} /> : <Pill tone="idle" label={data?.dl.detailed ? 'Not yet' : '?'} />}
                      {state?.message && <div className="text-muted mt-1 line-clamp-2 max-w-md text-xs">{state.message}</div>}
                    </td>
                  </tr>
                  {open.has(id) && (
                    <tr>
                      <td />
                      <td colSpan={4}>
                        {d.bundles.map((b) => (
                          <BundleDetail
                            key={b.metadata.name}
                            bundle={b}
                            bds={bds.filter((bd) => bd.metadata.labels?.['fleet.cattle.io/bundle-name'] === b.metadata.name)}
                            backLabel={name}
                          />
                        ))}
                      </td>
                    </tr>
                  )}
                </Fragment>
              )
            })}
          </tbody>
        </table>
      )}
      {can(TYPES.FLEET_HELM_OP, 'create') && (
        <div className="mt-4 flex flex-wrap gap-2">
          <Link className="btn btn-secondary" to={paths.newDeployment([targetArg(here)])}>
            Deploy to this cluster
          </Link>
          {tpl && (
            <Link className="btn btn-tertiary" to={paths.newDeployment([`template/${tpl}`])}>
              Deploy to every {tpl} cluster
            </Link>
          )}
        </div>
      )}
      <p className="text-muted mt-2.5 text-sm">
        CLI: <code>hyve cluster deployments {name}</code>, <code>hyve deploy create NAME ... --to {targetArg(here)}</code>
      </p>
      {!host && (
        <>
          <h3 className="mt-6">Secrets</h3>
          <p className="text-muted text-sm">The credentials its Cluster API provider reads, and what Deployments living in {ns} copy to clusters, from {ns}.</p>
          <SecretsSection
            load={(r) => clusterSecrets(r, cluster, data?.dl.deployments || [])}
            deps={[ns, name, data?.dl.deployments.length]}
            hint={`In ${ns}, the environment: providers read credentials here for every cluster in it.`}
            empty="Its provider and its environment's Deployments don't need a Secret here."
          />
        </>
      )}
    </div>
  )
}
