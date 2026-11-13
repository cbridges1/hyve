// One Cluster API provider (/providers/<namespace>/<provider>): its
// controller's health and where it came from, the ClusterClasses it
// serves and the templates on them, and the credentials their clusters
// read (a ClusterClass's credentialsRef), in each environment, to make
// before the first cluster there. Removing it does what 'hyve provider
// delete' does.
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { SecretsSection } from '../components/SecretsSection'
import { SecretView } from '../components/SecretView'
import { BackLink, Card, ConfirmButton, EmptyState, ErrorBanner, Field, Loading, PageHeader, Pill } from '../components/ui'
import { ViewDialog, type Section } from '../components/ViewDialog'
import { useEnvironment, useEnvironments } from '../lib/environment'
import { useAction, useLoad } from '../lib/hooks'
import { remove, typeOf, type Obj } from '../lib/kube'
import { classesOf, loadProviders, operatorObject, providerObjects } from '../lib/providers'
import { paths } from '../lib/routes'
import { providerCredentials } from '../lib/secrets'
import { TYPES } from '../lib/types'
import { Table } from '../components/Table'

export default function ProviderPage() {
  const { namespace = '', provider = '' } = useParams()
  const navigate = useNavigate()
  const act = useAction()
  const [viewing, setViewing] = useState<{ title: string; sections: Section[] } | null>(null)
  const [showVars, setShowVars] = useState(false)
  const [env] = useEnvironment()
  const envs = useEnvironments()
  const { data, error, loading } = useLoad(
    async (r) => {
      const p = (await loadProviders(r)).find((x) => x.ns === namespace && x.full === provider)
      if (!p) return null
      const [objects, classes, templates, vars] = await Promise.all([
        providerObjects(r),
        p.type === 'infrastructure' ? classesOf(r, p) : Promise.resolve([] as Obj[]),
        r.findAll(TYPES.TEMPLATE).catch(() => [] as Obj[]),
        r.maybe(TYPES.SECRET, p.ns, `${p.name}-variables`).catch(() => null),
      ])
      return { p, op: operatorObject(objects, p), classes, templates, vars }
    },
    [namespace, provider],
  )

  if (loading) return <Loading />
  const back = <BackLink to={paths.providers} label="Providers" />
  if (!data)
    return (
      <div>
        {back}
        <ErrorBanner error={error} />
        {!error && <EmptyState>There's no provider {provider} in {namespace}.</EmptyState>}
      </div>
    )
  const { p, op, classes, templates, vars } = data
  const shown = env ? [env] : envs.data || []
  const url = op?.spec?.fetchConfig?.url

  const removeIt = () =>
    act.run(async () => {
      await remove(typeOf(op!)!, op!)
      navigate(paths.providers)
    })

  return (
    <div>
      {back}
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            {p.name}
            <Pill tone={p.healthy ? 'good' : 'busy'} label={p.healthy ? 'Ready' : `Not ready (${p.state})`} />
          </span>
        }
        subtitle={`The ${p.type} provider in ${p.ns}.`}
      >
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => setViewing({ title: `${p.type} ${p.name}`, sections: [...(op ? [{ label: op.kind || 'Provider', object: op }] : []), { label: 'Deployment', object: p.d }] })}
        >
          View YAML
        </button>
        {op && <ConfirmButton variant="danger" label="Remove" confirm={`Remove ${p.name}? Delete its clusters first.`} disabled={act.busy} onConfirm={removeIt} />}
      </PageHeader>
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />

      <Card title="Details">
        <Field label="Type">{p.type}</Field>
        <Field label="Namespace">{p.ns}</Field>
        <Field label="Version">{p.version || '-'}</Field>
        <Field label="Controller">
          {p.state} ready <span className="text-muted text-xs">({p.d.metadata.name})</span>
        </Field>
        <Field label="Image">
          <code className="text-xs break-all">{p.image}</code>
        </Field>
        <Field label="Installed by">{op ? `the Cluster API Operator (${op.kind})` : "hyve's own manifests (local mode)"}</Field>
        {url && (
          <Field label="Components">
            <code className="text-xs break-all">{url}</code>
          </Field>
        )}
        <Field label="Variables">
          {vars ? (
            <>
              {Object.keys(vars.data || {}).sort().join(', ') || 'none'} in {vars.metadata.name}{' '}
              <button type="button" className="btn btn-tertiary btn-sm" onClick={() => setShowVars(true)}>
                View
              </button>
            </>
          ) : (
            <span className="text-muted">none: set when it's added</span>
          )}
        </Field>
      </Card>

      {p.type === 'infrastructure' && (
        <>
          <Card title="ClusterClasses" className="mt-4">
            {!classes.length ? (
              <EmptyState>It doesn't publish a ClusterClass here. Add one with 'hyve provider add {p.name} --clusterclass NAME'.</EmptyState>
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <thead>
                    <tr>
                      <th>Class</th>
                      <th>Namespace</th>
                      <th>Templates</th>
                    </tr>
                  </thead>
                  <tbody>
                    {classes.map((c) => {
                      const on = templates.filter((t) => t.spec?.clusterClassRef?.name === c.metadata.name && t.metadata.namespace === c.metadata.namespace)
                      return (
                        <tr key={`${c.metadata.namespace}/${c.metadata.name}`}>
                          <td>{c.metadata.name}</td>
                          <td>{c.metadata.namespace}</td>
                          <td>
                            {on.length
                              ? on.map((t, i) => (
                                  <span key={t.metadata.name}>
                                    {i > 0 && ', '}
                                    <Link to={paths.template(t.metadata.namespace!, t.metadata.name)}>{t.metadata.name}</Link>
                                  </span>
                                ))
                              : '-'}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </Table>
              </div>
            )}
          </Card>

          <Card title="Credentials" className="mt-4">
            <p className="text-muted mt-0 text-sm">
              The Secrets its clusters read their credentials from (the ClusterClass's credentialsRef), in the cluster's environment: make one in each
              environment before the first cluster there.{env ? ` Showing ${env}; pick all environments in the header to see each.` : ''}
            </p>
            {envs.loading ? (
              <Loading />
            ) : (
              <SecretsSection
                load={(r) => providerCredentials(r, shown, classes)}
                deps={[shown.join(','), classes.map((c) => c.metadata.name).join(',')]}
                hint={`In the environment: ${p.name} reads it for every cluster made there.`}
                empty={`Its clusters don't read credentials from a Secret: ${p.name} takes them another way, such as its variables.`}
                showNamespace={shown.length > 1}
              />
            )}
          </Card>
        </>
      )}
      {showVars && vars && <SecretView namespace={vars.metadata.namespace!} name={vars.metadata.name} onClose={() => setShowVars(false)} />}
      {viewing && <ViewDialog title={viewing.title} sections={viewing.sections} onClose={() => setViewing(null)} />}
    </div>
  )
}
