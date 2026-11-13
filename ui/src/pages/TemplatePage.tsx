// A ClusterTemplate: view, edit or create it, preview what a cluster from
// it gets, or make one; and the Deployments every cluster from it gets.
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { RenderDialog } from '../components/RenderDialog'
import { TemplateForm, type Mode } from '../components/TemplateForm'
import { BackLink, Banner, Card, EmptyState, ErrorBanner, Loading, PageHeader, Pill } from '../components/ui'
import { forTemplate, loadDelivery, readiness, stateTone, targetKey, targetLabel } from '../lib/delivery'
import { useLoad } from '../lib/hooks'
import { can, newObject, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE, TYPES } from '../lib/types'
import { Table } from '../components/Table'

export default function TemplatePage() {
  const { namespace, name } = useParams()
  const [query] = useSearchParams()
  const navigate = useNavigate()
  const mode: Mode = !name ? 'create' : query.get('mode') === 'edit' ? 'edit' : 'view'
  const [previewing, setPreviewing] = useState(false)

  const { data: value, error, loading } = useLoad(
    async (r) =>
      mode === 'create'
        ? newObject(TYPES.TEMPLATE, { metadata: { name: '', namespace: query.get('namespace') || DEFAULT_TEMPLATE_NAMESPACE }, spec: {} })
        : r.find(TYPES.TEMPLATE, namespace, name!),
    [mode, namespace, name],
    { live: mode === 'view' },
  )

  if (loading) return <Loading />
  if (!value) return <ErrorBanner error={error} />
  const ns = value.metadata.namespace!

  return (
    <div>
      <BackLink to={paths.templates} label="Templates" />
      <PageHeader title={mode === 'create' ? 'New template' : value.metadata.name}>
        {mode === 'view' && (
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setPreviewing(true)}>
              Preview
            </button>
            <Link className="btn btn-secondary" to={paths.yaml(TYPES.TEMPLATE, ns, value.metadata.name)}>
              YAML
            </Link>
            {can(TYPES.TEMPLATE, 'update') && (
              <Link className="btn btn-secondary" to={paths.template(ns, value.metadata.name, true)}>
                Edit
              </Link>
            )}
            <button type="button" className="btn btn-primary" onClick={() => navigate(paths.createCluster({ namespace: ns, name: value.metadata.name }))}>
              New cluster
            </button>
          </>
        )}
      </PageHeader>
      {value.metadata.deletionTimestamp && <Banner tone="warning">Being deleted.</Banner>}
      {previewing && <RenderDialog template={value} onClose={() => setPreviewing(false)} />}
      <Card>
        <TemplateForm
          // generation, not resourceVersion: a status update would close an open resource form.
          key={mode === 'view' ? `view-${value.metadata.generation}` : mode}
          value={value}
          mode={mode}
          onSaved={(o) => navigate(paths.template(o.metadata.namespace!, o.metadata.name))}
          onCancel={() => navigate(mode === 'create' ? paths.templates : paths.template(namespace!, name!))}
        />
      </Card>
      {mode === 'view' && <DeployedHere template={value} />}
    </div>
  )
}

// The Deployments every cluster from the template gets: aimed at it, its
// class or all clusters.
function DeployedHere({ template }: { template: Obj }) {
  const { data, error, loading } = useLoad((r) => loadDelivery(r), [])
  const name = template.metadata.name
  const mine = data ? forTemplate(data, template) : []
  return (
    <Card
      title="Deployed to its clusters"
      className="mt-4"
      action={
        can(TYPES.FLEET_HELM_OP, 'create') && (
          <Link className="btn btn-secondary btn-sm" to={paths.newDeployment([`template/${name}`])}>
            Deploy to this template
          </Link>
        )
      }
    >
      <ErrorBanner error={error} />
      {loading ? (
        <Loading />
      ) : !mine.length ? (
        <EmptyState>Nothing yet: a Deployment aimed at this template reaches every cluster made from it, now and later.</EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <thead>
              <tr>
                <th>Deployment</th>
                <th>Source</th>
                <th>Delivered to</th>
                <th>Clusters ready</th>
              </tr>
            </thead>
            <tbody>
              {mine.map((d) => {
                const { ready, state } = readiness(d)
                return (
                  <tr key={`${d.namespace}/${d.name}`}>
                    <td>
                      <Link to={paths.deployment(d.namespace, d.name)}>{d.name}</Link>
                    </td>
                    <td className="max-w-80 break-all">{d.source}</td>
                    <td>
                      <div className="flex flex-wrap gap-1">
                        {d.targets.map((t) => (
                          <Pill key={targetKey(t)} tone={t.kind === 'template' && t.name === name ? 'info' : 'idle'} label={targetLabel(t)} />
                        ))}
                      </div>
                    </td>
                    <td>
                      {ready || '-'} {state && <Pill tone={stateTone(state)} label={state} />}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        </div>
      )}
    </Card>
  )
}
