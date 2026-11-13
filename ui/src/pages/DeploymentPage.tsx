// One Deployment: where it comes from and where it goes, its state on
// each cluster (from its BundleDeployments), and, opened, what each of its
// bundles deployed. Pause, resume, deploy again, edit (the form, or its
// Fleet object as YAML). Then the Secrets it copies, and, last, deleting it.
import { Fragment, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { BundleDetail } from '../components/BundleDetail'
import { Icon } from '../components/Icon'
import { SecretsSection } from '../components/SecretsSection'
import { BackLink, Banner, Card, ConfirmButton, EmptyState, ErrorBanner, Field, Loading, PageHeader, Pill } from '../components/ui'
import { loadDelivery, readiness, ROLLOUTS, stateTone, targetLabel, targetKey, type Deployment } from '../lib/delivery'
import { age } from '../lib/format'
import { useAction, useLoad } from '../lib/hooks'
import { can, patch, remove } from '../lib/kube'
import { bundleMessage, bundleState, shortBundleName } from '../lib/repos'
import { backFrom, paths } from '../lib/routes'
import { deploymentSecrets } from '../lib/secrets'

const SOURCE_LABEL = { git: 'Git', helm: 'Helm chart', manifests: 'Manifests' }

export default function DeploymentPage() {
  const { namespace, name } = useParams() as { namespace: string; name: string }
  const location = useLocation()
  const navigate = useNavigate()
  const [open, setOpen] = useState<Set<string>>(new Set())
  const act = useAction()
  const { data, error, loading, reload } = useLoad(async (r) => {
    const dl = await loadDelivery(r)
    return { dl, d: dl.deployments.find((x) => x.namespace === namespace && x.name === name) }
  }, [namespace, name])

  if (loading) return <Loading />
  if (!data?.d) return <ErrorBanner error={error || new Error(`No Deployment ${namespace}/${name}.`)} />
  const d: Deployment = data.d
  const { ready, state, message } = readiness(d)
  const spec = d.model.spec || {}
  const update = (body: unknown) =>
    act.run(async () => {
      await patch(d.type, namespace, name, body)
      await reload(true)
    })
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  const editable = can(d.type, 'update')
  const back = backFrom(location, name)

  return (
    <div>
      <BackLink to={paths.fleet} label="Fleet" />
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            {name}
            {d.paused && <Pill tone="busy" label="Paused" />}
          </span>
        }
        subtitle={`${SOURCE_LABEL[d.sourceKind]} · ${d.source}`}
      >
        {editable && (
          <>
            <button type="button" className="btn btn-secondary" disabled={act.busy} title={d.paused ? 'Let changes reach clusters again' : "Stop changes reaching clusters; what's deployed stays"} onClick={() => update({ spec: { paused: !d.paused } })}>
              {d.paused ? 'Resume' : 'Pause'}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={act.busy || d.paused}
              title="Deploy again to every cluster, even where nothing changed (a release stuck after a failed upgrade, objects changed by hand)"
              onClick={() => update({ spec: { forceSyncGeneration: (spec.forceSyncGeneration || 0) + 1 } })}
            >
              Deploy again
            </button>
            <Link className="btn btn-secondary" to={paths.yaml(d.type, namespace, name)} state={back}>
              YAML
            </Link>
            <Link className="btn btn-primary" to={paths.editDeployment(namespace, name)}>
              Edit
            </Link>
          </>
        )}
      </PageHeader>
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />
      {d.paused && <Banner tone="warning">Paused: changes wait until it's resumed; clusters keep what they have. Waiting changes show as OutOfSync.</Banner>}
      <Card className="mb-5">
        <Field label="Delivered to">
          <span className="flex flex-wrap gap-1.5">
            {d.targets.map((t) => (
              <Pill key={targetKey(t)} tone="info" label={targetLabel(t)} title={t.kind === 'custom' ? JSON.stringify(t.raw) : undefined} />
            ))}
          </span>
        </Field>
        <Field label="Clusters ready">
          {ready || '-'} {state && <Pill tone={stateTone(state)} label={state} />}
        </Field>
        {message && <Field label="Problem">{message}</Field>}
        <Field label="Rollout">{ROLLOUTS.find((r) => r.value === d.rollout)?.label || (d.sourceKind === 'git' ? "Per bundle, in its fleet.yaml" : 'Custom (see its YAML)')}</Field>
        {d.kind === 'GitRepo' && (
          <Field label="Commit">
            <span className="font-mono">{(d.model.status?.commit || '').slice(0, 12) || '-'}</span>
            {d.model.status?.lastPollingTriggered && <span className="text-muted"> · checked {age(d.model.status.lastPollingTriggered)} ago</span>}
          </Field>
        )}
        <Field label="Namespace">{namespace}</Field>
        <Field label="Undo changes on clusters">{spec.correctDrift?.enabled ? 'Yes' : 'No'}</Field>
      </Card>

      <Card title="Clusters" className="mb-5">
        {!data.dl.detailed && <p className="text-muted mt-0 text-sm">Each cluster's state needs access to Fleet's per-cluster namespaces; the totals above are Fleet's.</p>}
        {!d.states.length ? (
          <EmptyState>{data.dl.detailed ? 'No cluster has it yet.' : 'Not shown.'}</EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th>Cluster</th>
                  <th>State</th>
                </tr>
              </thead>
              <tbody>
                {d.states.map((s) => {
                  const [cns, cname] = s.cluster.split('/')
                  return (
                    <tr key={s.cluster}>
                      <td>{cns === 'fleet-local' ? <Link to={paths.localCluster('deployments')}>local (host)</Link> : <Link to={paths.cluster(cns, cname, 'deployments')}>{s.cluster}</Link>}</td>
                      <td>
                        <Pill tone={stateTone(s.state)} label={s.state} />
                        {s.message && <div className="text-muted mt-1 max-w-2xl text-xs break-words">{s.message}</div>}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title={d.kind === 'GitRepo' ? 'Bundles (one per fleet.yaml)' : 'What it deployed'} className="mb-5">
        {!d.bundles.length ? (
          <EmptyState>Fleet hasn't made its bundle yet.</EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th />
                  <th>Bundle</th>
                  <th>Clusters ready</th>
                  <th>State</th>
                </tr>
              </thead>
              <tbody>
                {d.bundles.map((b) => {
                  const id = b.metadata.name
                  const st = bundleState(b)
                  const msg = bundleMessage(b)
                  return (
                    <Fragment key={id}>
                      <tr className="cursor-pointer" onClick={() => toggle(id)}>
                        <td className="w-6">
                          <span className={`inline-block transition-transform ${open.has(id) ? 'rotate-90' : ''}`}>
                            <Icon name="chevronRight" size={14} />
                          </span>
                        </td>
                        <td>
                          {d.kind === 'GitRepo' ? shortBundleName(d.model, b) : id}
                          {msg && <div className="text-muted line-clamp-1 max-w-xl text-xs">{msg}</div>}
                        </td>
                        <td>{b.status?.display?.readyClusters || '-'}</td>
                        <td>
                          <Pill tone={stateTone(st)} label={st} />
                        </td>
                      </tr>
                      {open.has(id) && (
                        <tr>
                          <td />
                          <td colSpan={3}>
                            <BundleDetail bundle={b} bds={d.bds.filter((bd) => bd.metadata.labels?.['fleet.cattle.io/bundle-name'] === id)} backLabel={name} />
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="Secrets">
        <p className="text-muted mt-0 text-sm">What it has Fleet copy to its clusters, from {namespace}. Clusters get a change the next time Fleet copies the Secret.</p>
        <SecretsSection
          load={(r) => deploymentSecrets(r, d)}
          deps={[namespace, name, d.model.metadata.resourceVersion]}
          hint={`In ${namespace}, beside the Deployment: Fleet copies it to every cluster ${name} reaches.`}
          empty="It doesn't copy a Secret."
        />
      </Card>

      {can(d.type, 'delete') && (
        <Card className="mt-5 border-red-200 dark:border-red-900/60">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="mb-0.5">Delete this deployment</h2>
              <p className="text-muted my-0 text-sm">
                Fleet removes what it deployed from every cluster it reaches{d.states.length ? ` (${d.states.length} now)` : ''}. To stop changes but keep what's there, pause it instead.
              </p>
            </div>
            <ConfirmButton
              label="Delete deployment"
              confirm={`Delete ${name} from ${d.states.length || 'its'} cluster${d.states.length === 1 ? '' : 's'}? Click to confirm`}
              variant="danger"
              disabled={act.busy}
              onConfirm={() =>
                act.run(async () => {
                  await remove(d.type, d.model)
                  navigate(paths.fleet)
                })
              }
            />
          </div>
        </Card>
      )}
    </div>
  )
}
