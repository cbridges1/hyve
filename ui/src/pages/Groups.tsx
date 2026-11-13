// Groups: the Fleet ClusterGroups Deployments can be aimed at. hyve keeps
// one per template (template-NAME) and environment (env-NAME), CAAPF one
// per ClusterClass; the rest are people's own (custom), clusters by their
// labels, which hyve copies to every environment. Each shows what it
// selects, the clusters in it and the Deployments aimed at it.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Members, NewGroup } from '../components/GroupParts'
import { ConfirmButton, EmptyState, ErrorBanner, Loading, PageHeader, Pill } from '../components/ui'
import { CLUSTER_GROUP, groupKind, loadDelivery, reaches, targetKey, type Delivery, type Target } from '../lib/delivery'
import { useAction, useLoad } from '../lib/hooks'
import { can, newObject, remove, save, type Obj } from '../lib/kube'
import { paths } from '../lib/routes'
import { DEFAULT_TEMPLATE_NAMESPACE } from '../lib/types'
import { Table } from '../components/Table'

const KIND_LABEL = { template: 'Template', environment: 'Environment', class: 'Cluster class', custom: 'Custom' }
const ORDER = ['custom', 'template', 'environment', 'class']

// The target a group stands for: hyve's and CAAPF's groups are their
// template, environment or class.
function targetOf(g: Obj): Target {
  const name = g.metadata.name
  switch (groupKind(g)) {
    case 'template':
      return { kind: 'template', name: name.replace(/^template-/, '') }
    case 'environment':
      return { kind: 'env', name: name.replace(/^env-/, '') }
    case 'class':
      return { kind: 'class', name }
    default:
      return { kind: 'group', name }
  }
}

// The Fleet list of what's aimed at a group.
function fleetLink(t: Target): string {
  const category = { template: 'templates', env: 'environments', class: 'classes', group: 'groups' }[t.kind as 'template']
  return `${paths.fleet}?category=${category}&item=${encodeURIComponent(t.name || '')}`
}

function selects(g: Obj): string {
  const ml: Record<string, string> = g.spec?.selector?.matchLabels || {}
  const parts = Object.entries(ml).map(([k, v]) => `${k}=${v}`)
  return parts.join(', ') || (g.spec?.selector ? 'a selector (see its YAML)' : 'every cluster')
}

export default function Groups() {
  const { data, error, loading, reload } = useLoad((r) => loadDelivery(r), [])
  const [adding, setAdding] = useState(false)
  const act = useAction()

  if (loading || !data) return <Loading />
  const dl: Delivery = data
  const groups = dl.groups
    .filter((g) => g.metadata.namespace === DEFAULT_TEMPLATE_NAMESPACE)
    .sort((a, b) => ORDER.indexOf(groupKind(a)) - ORDER.indexOf(groupKind(b)) || a.metadata.name.localeCompare(b.metadata.name))

  return (
    <div>
      <PageHeader
        title="Groups"
        subtitle="Sets of clusters Deployments can be aimed at. hyve keeps one for each template and environment, and one for each cluster class; make your own to pick clusters by their labels."
      >
        {can(CLUSTER_GROUP, 'create') && !adding && (
          <button type="button" className="btn btn-primary" onClick={() => setAdding(true)}>
            New group
          </button>
        )}
      </PageHeader>
      <ErrorBanner error={error} />
      <ErrorBanner error={act.error} />
      {adding && (
        <NewGroup
          busy={act.busy}
          onCancel={() => setAdding(false)}
          onCreate={(name, labels) =>
            act.run(async () => {
              await save(CLUSTER_GROUP, newObject(CLUSTER_GROUP, { metadata: { name, namespace: DEFAULT_TEMPLATE_NAMESPACE }, spec: { selector: { matchLabels: labels } } }))
              setAdding(false)
              await reload(true)
            })
          }
        />
      )}
      {!groups.length ? (
        <EmptyState>No groups yet: hyve makes one for each template and environment.</EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Kind</th>
                <th>Selects</th>
                <th>Clusters</th>
                <th>Deployments</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => {
                const t = targetOf(g)
                const kind = groupKind(g)
                const aimed = dl.deployments.filter((d) => d.targets.some((x) => targetKey(x) === targetKey(t))).length
                return (
                  <tr key={g.metadata.name}>
                    <td>
                      <Link to={paths.yaml(CLUSTER_GROUP, g.metadata.namespace, g.metadata.name)}>{g.metadata.name}</Link>
                    </td>
                    <td>
                      <Pill tone={kind === 'custom' ? 'good' : 'idle'} label={KIND_LABEL[kind]} />
                    </td>
                    <td className="text-xs">
                      <code className="break-all">{selects(g)}</code>
                    </td>
                    <td className="text-sm">
                      <Members clusters={dl.clusters.filter((c) => reaches(t, c, dl.groups))} />
                    </td>
                    <td>
                      <Link to={fleetLink(t)}>{aimed}</Link>
                    </td>
                    <td className="text-right">
                      {kind === 'custom' && can(CLUSTER_GROUP, 'delete') && (
                        <ConfirmButton
                          label="Delete"
                          confirm={`Delete group ${g.metadata.name}?${aimed ? ` ${aimed} deployment(s) target it and would reach no cluster through it.` : ''}`}
                          disabled={act.busy}
                          onConfirm={() =>
                            act.run(async () => {
                              await remove(CLUSTER_GROUP, g)
                              await reload(true)
                            })
                          }
                        />
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        </div>
      )}
    </div>
  )
}
